package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Rules. Named like `spec lint`'s so the two linters' --json is interchangeable.
const (
	ruleUnresolved    = "unresolved"
	ruleSuperseded    = "superseded"
	ruleStaleAbstract = "stale-abstract"
)

// citationFindingDTO is the --json contract: one finding per OCCURRENCE, so a
// CI annotation can point at the line. Replacement carries the superseding
// citation when there is one — the fix, not just the complaint.
type citationFindingDTO struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Citation    string `json:"citation"`
	Rule        string `json:"rule"`
	Severity    string `json:"severity"` // error | warning
	Message     string `json:"message"`
	Replacement string `json:"replacement,omitempty"`
}

// citationVerdict is the per-citation result, before it is fanned back out over
// the occurrences that cite it.
type citationVerdict struct {
	Rule        string
	Severity    string
	Message     string
	Replacement string
}

func newCmdCitations(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var srcs, excludes []string
	var loose, strict, staleAbstracts bool
	cmd := &cobra.Command{
		Use:     "citations [-m <memory>]",
		Aliases: []string{"check-citations"},
		Short:   "Verify that `Spec:` citations in source still resolve",
		Long: `Scan source for spec citations and check each one against the corpus.

The add-spec workflow tells authors to point at a spec from the code it
governs — ` + "`// Spec: <citation>`" + ` near the load-bearing
constant/query/handler — so citations exist outside the graph, where
` + "`spec lint`" + ` cannot see them. ` + "`spec supersede`" + ` then retires a
number and every pointer to it silently documents a replaced contract.

Reported: a citation that does not resolve (typo, or a spec deleted rather
than superseded), and one that resolves to a superseded spec — the message
names its replacement.

--stale-abstracts adds a warning when a cited spec's body differs from the
version its abstract was written against. Read it narrowly: it is a hash
comparison, so it fires whenever the two differ — including on an edit that
changed nothing the abstract says — and NOT on a body edited and then restored
byte-for-byte. Measured over hadronmemory.com::specs it separates
almost nothing (Cohen's d = 0.01 at the rule tier against embedding
similarity), so it is off by default: it is a property of the SPEC rather than
of the pointer, and two thirds of a live corpus trips it. For the corpus-wide
view use ` + "`hadron memory validate <memory> --check stale-abstract`" + `.

Matching is anchored on the prescribed ` + "`Spec:`" + ` prefix, and takes every
citation on that line — real pointers often list several. --loose drops the
anchor and scans every line for citation-shaped tokens, which finds pointers
written some other way at the cost of false positives in prose.

Errors exit 5 (like ` + "`spec lint`" + `), so this can gate CI; --strict promotes
warnings too.`,
		Example: `  hadron spec citations -m hadronmemory.com::specs --src src/
  hadron spec citations -m micromentor.org::platform-specs --src . --exclude '*_test.go' --json
  hadron spec citations --src src/ --loose --strict`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scan, err := scanCitations(scanOptions{Roots: srcs, Loose: loose, Exclude: excludes})
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "scanning source: %v", err)
			}

			// Nothing to resolve ⇒ nothing to resolve it AGAINST. Requiring a
			// configured memory here failed a repo that simply has no
			// pointers, with a usage error about -m rather than the honest
			// answer (Copilot review on #351). Short-circuit before the client
			// and the memory lookup.
			if len(scan.Refs) == 0 {
				return output.Write(f.IOStreams, f.JSON, []citationFindingDTO{}, func(w io.Writer) error {
					// Reported rather than a checkmark: pointing --src at the
					// wrong path otherwise looks exactly like a clean repo.
					fmt.Fprintf(w, "no spec citations found in %d file(s) under %s%s\n",
						scan.Files, strings.Join(rootsOrDot(srcs), ", "), looseHint(loose))
					return nil
				})
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			verdicts, err := resolveCitations(cmd, client, memURN, distinctCitations(scan.Refs), staleAbstracts)
			if err != nil {
				return err
			}

			findings := []citationFindingDTO{}
			for _, ref := range scan.Refs {
				v, ok := verdicts[ref.Citation]
				if !ok {
					continue // resolves and is healthy
				}
				findings = append(findings, citationFindingDTO{
					File: ref.File, Line: ref.Line, Citation: ref.Citation,
					Rule: v.Rule, Severity: v.Severity, Message: v.Message, Replacement: v.Replacement,
				})
			}
			if strict {
				for i := range findings {
					if findings[i].Severity == sevWarning {
						findings[i].Severity = sevError
					}
				}
			}
			hasError := false
			for _, fnd := range findings {
				if fnd.Severity == sevError {
					hasError = true
					break
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, findings, func(w io.Writer) error {
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ %d citation(s) in %d file(s) resolve\n",
						len(scan.Refs), countFiles(scan.Refs))
					return nil
				}
				t := output.NewTable(w, "LOCATION", "CITATION", "SEVERITY", "RULE", "MESSAGE")
				for _, fnd := range findings {
					t.Row(fmt.Sprintf("%s:%d", fnd.File, fnd.Line), fnd.Citation, fnd.Severity, fnd.Rule, fnd.Message)
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	cmd.Flags().StringArrayVar(&srcs, "src", nil, "file or directory to scan (repeatable; default \".\")")
	cmd.Flags().StringArrayVar(&excludes, "exclude", nil, "skip paths matching this glob (repeatable)")
	cmd.Flags().BoolVar(&loose, "loose", false, "match citation-shaped tokens anywhere, not just after `Spec:`")
	cmd.Flags().BoolVar(&staleAbstracts, "stale-abstracts", false,
		"also warn when a cited spec's body differs from the version its abstract was written against (a hash comparison, not proof the rule changed)")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	return cmd
}

// distinctCitations collapses occurrences to the set that has to be read.
func distinctCitations(refs []citationRef) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range refs {
		if seen[r.Citation] {
			continue
		}
		seen[r.Citation] = true
		out = append(out, r.Citation)
	}
	sort.Strings(out)
	return out
}

func countFiles(refs []citationRef) int {
	seen := map[string]bool{}
	for _, r := range refs {
		seen[r.File] = true
	}
	return len(seen)
}

func rootsOrDot(srcs []string) []string {
	if len(srcs) == 0 {
		return []string{"."}
	}
	return srcs
}

func looseHint(loose bool) string {
	if loose {
		return ""
	}
	return ` (matching "Spec: <citation>"; --loose widens it)`
}

// resolveCitations batch-reads the cited specs and returns a verdict per
// UNHEALTHY citation — a healthy one is absent from the map.
//
// The read is bounded by the number of distinct citations in source, not by the
// corpus size: the alternative (list the whole corpus, match locs) reads
// hundreds of nodes to check a handful of pointers.
func resolveCitations(cmd *cobra.Command, client graphql.Client, memURN string, citations []string, checkStale bool) (map[string]citationVerdict, error) {
	out := map[string]citationVerdict{}
	if len(citations) == 0 {
		return out, nil
	}
	refs := make([]string, 0, len(citations))
	citByRef := make(map[string]string, len(citations))
	for _, c := range citations {
		ref := specNodeRef(memURN, c)
		refs = append(refs, ref)
		citByRef[ref] = c
	}
	nodes, unavailable, err := api.CollectNodeBatch(refs, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, ferr := gen.NodeBatch(cmd.Context(), client, chunk, nil, nil)
		if ferr != nil {
			return nil, api.MapError(ferr)
		}
		if resp == nil {
			return nil, nil
		}
		return resp.NodeBatch, nil
	})
	if err != nil {
		return nil, err
	}

	found := map[string]bool{}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		found[n.Loc] = true
		if v, bad := judgeCitation(nodeFromBatch(n), checkStale); bad {
			out[n.Loc] = v
		}
	}
	for _, ref := range unavailable {
		cit := citByRef[ref]
		if cit == "" {
			cit = ref
		}
		out[cit] = unresolvedVerdict(memURN)
	}
	// A ref the server neither returned nor listed as unavailable would
	// otherwise pass silently as healthy — the false-clean this check exists to
	// prevent, so it is reported like any other unreadable citation.
	for _, c := range citations {
		if !found[c] {
			if _, reported := out[c]; !reported {
				out[c] = unresolvedVerdict(memURN)
			}
		}
	}
	return out, nil
}

func unresolvedVerdict(memURN string) citationVerdict {
	return citationVerdict{
		Rule: ruleUnresolved, Severity: sevError,
		// "Does not exist" and "exists but is not readable by this principal"
		// arrive identically from nodeBatch, so the message claims neither.
		Message: fmt.Sprintf("does not resolve in %s — a typo, a spec deleted rather than superseded, or not visible to you", memURN),
	}
}

// judgeCitation applies the per-spec rules to a resolved citation. The bool is
// false when the citation is healthy.
//
// checkStale gates the stale-abstract rule, which is OFF by default — see
// staleAbstract and docs/plans/spec-citations.md, Deviation 2.
func judgeCitation(n specNode, checkStale bool) (citationVerdict, bool) {
	// Retirement first: it is the reason this command exists, and a superseded
	// spec's abstract drifting is beside the point.
	if hasTag(n.Tags, supersededTag) {
		v := citationVerdict{Rule: ruleSuperseded, Severity: sevError}
		if repl, ok := supersededByLoc(n); ok {
			v.Replacement = repl
			v.Message = fmt.Sprintf("cites a superseded spec — replaced by %s; re-read it and update the pointer", repl)
		} else {
			v.Message = "cites a superseded spec, with no superseded-by edge naming its replacement"
		}
		return v, true
	}
	if checkStale && staleAbstract(n) {
		return citationVerdict{
			Rule: ruleStaleAbstract, Severity: sevWarning,
			Message: "the spec's body differs from the version its abstract was written against — a hash comparison, NOT proof the rule changed; re-read it if your code leans on the detail",
		}, true
	}
	return citationVerdict{}, false
}

// supersededByLoc returns the replacement citation from the retirement edge
// `spec supersede` writes.
func supersededByLoc(n specNode) (string, bool) {
	for _, e := range n.OutEdges {
		if e.Name == supersededByLabel && e.Loc != "" {
			return e.Loc, true
		}
	}
	return "", false
}

// staleAbstract reports whether the current body differs from the version the
// abstract was written against. That is all it reports: it compares content
// hashes, so it fires on an edit that changed nothing the abstract says, and
// does NOT fire on a body edited and then restored byte-for-byte. Do not phrase
// a finding as "the abstract is wrong".
//
// The comparison is the server's own, computed client-side: the schema defines
// abstractOriginHash as "SHA-256 of plaintext content, truncated to 8 hex
// chars", compared at read time against the current content, and
// hadron-server's computeContentHash is exactly that. So the batch read already
// carries everything needed and no extra query is required.
//
// OFF by default (--stale-abstracts), for two reasons that compound. It fires
// on 174 of 271 live specs — including every one of the 26 citations in the
// sibling repos — so default-on it buried the two rules that name an
// actually-broken pointer. And it does not measure what its name suggests:
// against embedding similarity it separates the stale cohort from the clean one
// at Cohen's d = 0.11 overall and d = 0.01 at the rule tier, while the same
// metric detects a genuinely mismatched abstract at d = 3.29 (hadron-cli#352).
// So most of what it flags is a body edit the abstract never needed to reflect.
//
// It is kept because "the spec you cite has been edited" is still worth an
// opt-in warning next to a code pointer. It is a property of the SPEC, not of
// the citation, so the corpus-wide view belongs to
// `hadron memory validate <memory> --check stale-abstract`.
//
// NOT delegated to that server audit, deliberately: validateMemory caps its
// findings (default 200, max 1000), so on a memory with more findings the stale
// set comes back silently incomplete and a cited spec reads as fresh. This hash
// is exact and rides a batch read the command already performs — deduplicating
// onto a capped source would trade a correct answer for a tidier one
// (hadron-cli#355).
//
// Silent unless BOTH values are present: a spec with no abstract, or one whose
// abstract predates the hash, is a `spec lint` concern, not a citation defect.
func staleAbstract(n specNode) bool {
	if n.AbstractOriginHash == nil || *n.AbstractOriginHash == "" || n.Content == nil {
		return false
	}
	return contentHash(*n.Content) != *n.AbstractOriginHash
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
}
