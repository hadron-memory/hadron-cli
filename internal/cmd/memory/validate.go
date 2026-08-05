package memory

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// serverMaxFindings is the largest `limit` validateMemory accepts (default
// 200). Requested automatically when --check is used, so the client-side
// filter sees as much of the result set as the API will return.
const serverMaxFindings = 1000

// checkKinds maps the CLI's kebab-case --check values to the server enum. The
// CLI spells them kebab-case to match every other flag value in the tool; the
// SCREAMING_CASE wire form is accepted too so a value copied out of `--json`
// or the GraphQL schema works without translation.
var checkKinds = map[string]gen.MemoryValidationCheck{
	"broken-ref":     gen.MemoryValidationCheckBrokenRef,
	"embed-failed":   gen.MemoryValidationCheckEmbedFailed,
	"schema":         gen.MemoryValidationCheckSchema,
	"sparse":         gen.MemoryValidationCheckSparse,
	"stale-abstract": gen.MemoryValidationCheckStaleAbstract,
}

// checkBlurb is the one-line gloss printed above the findings table, so a
// reader knows what a kind actually asserts before acting on a count.
//
// stale-abstract deliberately does NOT repeat the schema's wording ("the
// abstract no longer reflects current content"). The check is a hash
// comparison — abstractOriginHash against the current content hash — so it
// fires on any body edit since the abstract was last written, whether or not
// the edit touched anything the abstract says. Measured over the 271-node
// hadronmemory.com::specs corpus, the flag carries essentially no signal about
// whether the abstract still describes the body (Cohen's d = 0.01 at the rule
// tier against embedding similarity), so overstating it here would send people
// rewriting 176 abstracts that mostly do not need it. See hadron-cli#352.
var checkBlurb = map[gen.MemoryValidationCheck]string{
	gen.MemoryValidationCheckBrokenRef:     "edge points at a missing or soft-deleted node",
	gen.MemoryValidationCheckEmbedFailed:   "node is absent from vector search — indistinguishable from \"no hits matched\"",
	gen.MemoryValidationCheckSchema:        "objectType/properties violate the memory's declared schema",
	gen.MemoryValidationCheckSparse:        "node has no description, content, or abstract",
	gen.MemoryValidationCheckStaleAbstract: "body edited since the abstract was authored (a hash comparison — not proof the abstract is wrong)",
}

// validateFindingDTO is one audit finding in the stable --json shape.
type validateFindingDTO struct {
	Kind    string `json:"kind"`
	NodeID  string `json:"nodeId"`
	NodeLoc string `json:"nodeLoc"`
	NodeURN string `json:"nodeUrn"`
	Detail  string `json:"detail"`
}

// validateSkippedDTO is one check the server did not run, and why.
type validateSkippedDTO struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

// validateResultDTO is the stable --json shape for a memory audit.
//
// Both counts are present on purpose. TotalFindings is the server's true count
// across every check, before `--limit` truncation — the number to gate CI on.
// MatchedFindings counts the findings actually listed, after any --check
// filter. They differ whenever the result was truncated or filtered, and
// conflating them is how a partial audit gets read as a clean one.
type validateResultDTO struct {
	Memory          string               `json:"memory"`
	MemoryID        string               `json:"memoryId"`
	NodesChecked    int                  `json:"nodesChecked"`
	OK              bool                 `json:"ok"`
	TotalFindings   int                  `json:"totalFindings"`
	MatchedFindings int                  `json:"matchedFindings"`
	Truncated       bool                 `json:"truncated"`
	Filtered        []string             `json:"filtered"`
	Counts          map[string]int       `json:"counts"`
	Findings        []validateFindingDTO `json:"findings"`
	SkippedChecks   []validateSkippedDTO `json:"skippedChecks"`
}

func newCmdValidate(f *cmdutil.Factory) *cobra.Command {
	var checks []string
	var limit int
	var failOnFindings bool
	cmd := &cobra.Command{
		Use:     "validate <memoryRef>",
		Aliases: []string{"audit", "check"},
		Short:   "Audit a memory's health: broken edges, sparse nodes, failed embeddings, stale abstracts, schema drift",
		Long: `Run the server's memory health audit and report its findings.

Five checks run server-side: broken-ref (an edge pointing at a missing or
soft-deleted node), embed-failed (a node absent from the vector index),
sparse (a node with no description, content, or abstract), schema
(objectType/properties violating the memory's declared schema), and
stale-abstract.

Read stale-abstract narrowly. It compares the abstract's origin hash against
the current content hash, so it fires on ANY body edit made since the abstract
was last written — including one that changed nothing the abstract says. It is
"the body moved under this abstract", not "this abstract is wrong".

Two numbers, deliberately: totalFindings is the true count across every check
before truncation — gate CI on that — while the listing is capped by --limit
and narrowed by --check. The server caps its findings list before --check can
filter it, so --check requests the server maximum unless you pin --limit
yourself; if the result is still truncated the report says so rather than
letting a short list read as a complete one. And "ok" is false whenever a check was SKIPPED, even
with zero findings, because health cannot be claimed for a check that did not
run; skippedChecks says which and why (the stale-abstract check is skipped on
an encrypted memory — the validator does not decrypt).

Exits 0 whatever the findings unless --fail-on-findings is passed. A corpus
that has never been audited will light up, and a check that fires on most of
the memory on day one teaches people to ignore the whole report — so gate CI
deliberately, and preferably on a narrowed --check.`,
		Example: `  hadron memory validate hadronmemory.com::specs
  hadron memory validate hadronmemory.com::specs --check broken-ref --check embed-failed
  hadron memory validate hadronmemory.com::specs --json
  hadron memory validate hadronmemory.com::specs --check broken-ref --fail-on-findings`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			want, err := parseChecks(checks)
			if err != nil {
				return err
			}
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must not be negative")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}

			// --check narrows client-side, but the server caps `findings`
			// BEFORE we ever see them — so filtering a default-capped result
			// can report zero matches while matches exist past the cap. (On
			// the live specs corpus that is not hypothetical: the one
			// broken-ref sits past finding 200.) When the caller filters
			// without pinning a limit, ask for the server maximum so the
			// filter runs over as much of the result as the API will give.
			var limitArg *int
			switch {
			case cmd.Flags().Changed("limit"):
				limitArg = &limit
			case len(want) > 0:
				max := serverMaxFindings
				limitArg = &max
			}
			resp, err := gen.ValidateMemory(cmd.Context(), client, memID, limitArg)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.ValidateMemory
			if r == nil {
				return notFoundMemory(args[0])
			}

			dto := buildValidateDTO(cmdutil.CanonicalMemoryRef(args[0]), r, want)

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeValidateText(w, dto)
			}); err != nil {
				return err
			}
			if failOnFindings && dto.MatchedFindings > 0 {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&checks, "check", nil,
		"only report these checks (repeatable): broken-ref, embed-failed, schema, sparse, stale-abstract")
	cmd.Flags().IntVar(&limit, "limit", 0, "max findings to list (server default 200, max 1000; --check implies the max); totalFindings is always the true count")
	cmd.Flags().BoolVar(&failOnFindings, "fail-on-findings", false, "exit 5 when any listed finding matches — for CI gating")
	return cmd
}

// parseChecks maps --check values onto the server enum, accepting either the
// kebab-case CLI spelling or the SCREAMING_CASE wire form. Returns nil for no
// filter, which the rest of the command treats as "every kind".
func parseChecks(vals []string) (map[gen.MemoryValidationCheck]bool, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	out := map[gen.MemoryValidationCheck]bool{}
	for _, v := range vals {
		key := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), "_", "-"))
		k, ok := checkKinds[key]
		if !ok {
			return nil, exitcode.Newf(exitcode.Usage,
				"unknown --check %q — expected one of: %s", v, strings.Join(sortedCheckNames(), ", "))
		}
		out[k] = true
	}
	return out, nil
}

func sortedCheckNames() []string {
	names := make([]string, 0, len(checkKinds))
	for k := range checkKinds {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// kindName renders a server enum value as the CLI's kebab-case spelling,
// falling back to the raw wire value so a kind added server-side still prints
// legibly instead of vanishing from the report.
func kindName(k gen.MemoryValidationCheck) string {
	for name, v := range checkKinds {
		if v == k {
			return name
		}
	}
	return strings.ToLower(strings.ReplaceAll(string(k), "_", "-"))
}

func buildValidateDTO(memRef string, r *gen.ValidateMemoryValidateMemoryMemoryValidationResult, want map[gen.MemoryValidationCheck]bool) validateResultDTO {
	dto := validateResultDTO{
		Memory:        memRef,
		MemoryID:      r.MemoryId,
		NodesChecked:  r.NodesChecked,
		OK:            r.Ok,
		TotalFindings: r.TotalFindings,
		Truncated:     r.Truncated,
		Filtered:      []string{},
		Counts:        map[string]int{},
		Findings:      []validateFindingDTO{},
		SkippedChecks: []validateSkippedDTO{},
	}
	for _, name := range sortedCheckNames() {
		if want[checkKinds[name]] {
			dto.Filtered = append(dto.Filtered, name)
		}
	}
	for _, fnd := range r.Findings {
		if fnd == nil || (want != nil && !want[fnd.Kind]) {
			continue
		}
		name := kindName(fnd.Kind)
		dto.Counts[name]++
		urn := ""
		if fnd.NodeUrn != nil {
			urn = *fnd.NodeUrn
		}
		dto.Findings = append(dto.Findings, validateFindingDTO{
			Kind: name, NodeID: fnd.NodeId, NodeLoc: fnd.NodeLoc, NodeURN: urn, Detail: fnd.Detail,
		})
	}
	dto.MatchedFindings = len(dto.Findings)
	for _, s := range r.SkippedChecks {
		if s == nil {
			continue
		}
		dto.SkippedChecks = append(dto.SkippedChecks, validateSkippedDTO{Check: kindName(s.Check), Reason: s.Reason})
	}
	sort.Slice(dto.SkippedChecks, func(i, j int) bool { return dto.SkippedChecks[i].Check < dto.SkippedChecks[j].Check })
	return dto
}

func writeValidateText(w io.Writer, dto validateResultDTO) error {
	fmt.Fprintf(w, "%s — %d node(s) checked\n", dto.Memory, dto.NodesChecked)

	// Skipped checks come first: they are the reason `ok` can be false with an
	// empty findings list, and reading the findings without them invites
	// "nothing found" to be mistaken for "nothing wrong".
	if len(dto.SkippedChecks) > 0 {
		fmt.Fprintf(w, "\nchecks NOT run (health is unknown for these):\n")
		for _, s := range dto.SkippedChecks {
			fmt.Fprintf(w, "  %-16s %s\n", s.Check, s.Reason)
		}
	}

	if dto.TotalFindings == 0 && len(dto.SkippedChecks) == 0 {
		fmt.Fprintf(w, "\n✓ no findings; every check ran\n")
		return nil
	}

	if len(dto.Counts) > 0 {
		fmt.Fprintln(w)
		t := output.NewTable(w, "KIND", "COUNT", "MEANS")
		for _, name := range sortedCheckNames() {
			n, ok := dto.Counts[name]
			if !ok {
				continue
			}
			t.Row(name, fmt.Sprint(n), checkBlurb[checkKinds[name]])
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintf(w, "\n%d finding(s) across all checks", dto.TotalFindings)
	if len(dto.Filtered) > 0 {
		fmt.Fprintf(w, "; %d listed after --check %s", dto.MatchedFindings, strings.Join(dto.Filtered, ","))
	}
	fmt.Fprintln(w)

	// Truncation and filtering compose badly and silently: the server caps the
	// findings list BEFORE --check narrows it, so a filtered view of a
	// truncated result can omit matches that exist beyond the cap. Say so
	// rather than letting a short list read as a complete one.
	if dto.Truncated {
		fmt.Fprintf(w, "note: the listing was truncated — raise --limit to see the rest\n")
		if len(dto.Filtered) > 0 {
			fmt.Fprintf(w, "      the server caps findings before --check filters them, so this\n")
			fmt.Fprintf(w, "      filtered view may be missing matches past the cap\n")
		}
	}

	if len(dto.Findings) > 0 {
		fmt.Fprintln(w)
		t := output.NewTable(w, "KIND", "LOC", "DETAIL")
		for _, fnd := range dto.Findings {
			t.Row(fnd.Kind, fnd.NodeLoc, truncateDetail(fnd.Detail))
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// truncateDetail keeps the findings table readable. Some details are whole
// provider error payloads — the SageMaker embedding errors run to several
// hundred characters with a CloudWatch URL — which would make the table
// unusable. The full string is always intact in --json.
func truncateDetail(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	const max = 96
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}
