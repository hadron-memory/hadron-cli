package coding

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// runCheckDTO is one returned check — the --json contract.
//
// Content ships WITH the check on purpose. #551 measured the alternative: the
// reviewer selected the applicable checks, then fetched each node individually,
// and a multi-node read was large enough to blow the surrounding agent's output
// budget. One response with the bodies in it is the feature.
type runCheckDTO struct {
	Loc         string   `json:"loc"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Trigger     string   `json:"trigger"`
	Verdict     string   `json:"verdict"` // matched | undecided
	Patterns    []string `json:"patterns"`
	// MatchedOn says which pattern fired on which file. A bare "matched" is a
	// claim; this is the evidence, which is what #551 means by auditable.
	MatchedOn []runHitDTO `json:"matchedOn"`
	// PortalURL is the server's own link, omitted when this deployment has no
	// portal — never composed from the URN.
	PortalURL string `json:"portalUrl,omitempty"`
	Content   string `json:"content"`
}

type runHitDTO struct {
	Pattern string `json:"pattern"`
	File    string `json:"file"`
}

// runExcludedDTO is a check the diff positively rules out. Reported rather than
// dropped: the reviewer can see what was taken away and why, which is the only
// thing that makes an automatic filter trustworthy.
type runExcludedDTO struct {
	Loc      string   `json:"loc"`
	Name     string   `json:"name"`
	Patterns []string `json:"patterns"`
	Reason   string   `json:"reason"`
}

// runResultDTO is the whole response. An OBJECT, not an array, so the scope and
// the paging counters have somewhere to live — `review list` stays an array and
// this is a different command, so nothing moves under an existing consumer.
type runResultDTO struct {
	Memory       string           `json:"memory"`
	MemorySource string           `json:"memorySource"`
	Base         string           `json:"base"`
	Head         string           `json:"head,omitempty"`
	DiffSource   string           `json:"diffSource"` // git | file | stdin
	ChangedFiles []string         `json:"changedFiles"`
	Total        int              `json:"total"`    // applicable checks before paging
	Returned     int              `json:"returned"` // in this response
	NextOffset   *int             `json:"nextOffset"`
	Checks       []runCheckDTO    `json:"checks"`
	Excluded     []runExcludedDTO `json:"excluded"`
	// Unavailable is a check that lists but cannot be read. Surfaced, never
	// dropped: the nodes listing can return ids that nodeBatch then refuses, and
	// a client-side fan-out that stays quiet about them reports a shorter
	// checklist as if it were the whole one.
	Unavailable []string `json:"unavailable"`
}

func newCmdReviewRun(f *cmdutil.Factory) *cobra.Command {
	var memory, root, base, head, diffSpec string
	var all bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Apply the review checklist to this repository's diff",
		Long: `Return the review checks a diff could fire, with their full bodies.

The memory is resolved from the repository (see ` + "`hadron coding --help`" + `),
the diff from git — or from --diff, which takes a file or ` + "`-`" + ` for stdin.

Applicability is decided STRUCTURALLY, never by interpreting the trigger's
prose, and checks fall into three buckets:

  matched     a concrete path the check names was changed; the pair that
              fired is reported, so the decision is auditable
  undecided   the check names no paths, so nothing about the diff can speak
              to it — RETURNED IN FULL, because "cannot tell" is not "does
              not apply"
  excluded    the check names paths and the diff touches none of them; listed
              separately with its patterns, never silently dropped

Only the third bucket removes anything, and only on positive evidence. A false
positive costs a skim; a false negative ships the defect, so the tie goes to
returning the check.

Bodies ship with the checks: fetching them one node at a time is the cost this
command exists to remove. Use --limit/--offset to page; the response always
reports the total, so a truncated read is never silent.`,
		Example: `  hadron coding review run
  hadron coding review run --base origin/main --json
  git diff origin/main | hadron coding review run --diff -
  hadron coding review run --all --json | jq '.checks[].loc'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must not be negative")
			}
			if diffSpec != "" && (base != "" || head != "") {
				return exitcode.Newf(exitcode.Usage,
					"--diff supplies the change set, so --base/--head have nothing to compare — pass one or the other")
			}

			// Pure flag validation first, above the client build: a caller who
			// mistyped a flag must be told about their flag, not their session.
			rm, err := codingScopeDetailed(cmd, f, memory)
			if err != nil {
				return err
			}
			mem := rm.codingMemory

			var files []string
			var usedBase, diffSource string
			switch {
			case diffSpec == "-":
				diffSource = "stdin"
				files, err = openDiff(diffSpec, f.IOStreams.In)
			case diffSpec != "":
				diffSource = "file"
				files, err = openDiff(diffSpec, f.IOStreams.In)
			default:
				diffSource = "git"
				files, usedBase, err = changedFiles(cmd.Context(), base, head)
			}
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			in, err := collectReview(cmd.Context(), client, mem, root)
			if err != nil {
				return err
			}

			result := buildRunResult(in, files, all)
			result.Memory, result.MemorySource = mem.raw, rm.source.String()
			result.Base, result.Head, result.DiffSource = usedBase, head, diffSource
			result.Total = len(result.Checks)
			result.Checks, result.NextOffset = pageChecks(result.Checks, limit, offset)
			result.Returned = len(result.Checks)

			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				return renderRun(w, result)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory holding the checklist (defaults to this repository's)")
	cmd.Flags().StringVar(&root, "root", reviewRootLoc, "loc of the review parent node")
	cmd.Flags().StringVar(&base, "base", "", "git ref to diff from (default: the merge base with the default branch)")
	cmd.Flags().StringVar(&head, "head", "", "git ref to diff to (default: the working tree)")
	cmd.Flags().StringVar(&diffSpec, "diff", "", "read a unified diff from this `path` (- for stdin) instead of from git")
	cmd.Flags().BoolVar(&all, "all", false, "include excluded checks in the returned set too")
	cmd.Flags().IntVar(&limit, "limit", 0, "return at most this many checks (0 = all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many checks")
	return cmd
}

// buildRunResult buckets every checklist member against the changed files.
func buildRunResult(in reviewInput, files []string, all bool) runResultDTO {
	out := runResultDTO{
		ChangedFiles: files,
		Checks:       []runCheckDTO{},
		Excluded:     []runExcludedDTO{},
		Unavailable:  append([]string{}, in.Unavailable...),
	}
	locs := make([]string, 0, len(in.Members))
	for loc := range in.Members {
		locs = append(locs, loc)
	}
	sort.Strings(locs)

	for _, loc := range locs {
		n := in.Members[loc]
		trigger := in.Edges[loc].Label
		m := classify(trigger, n.Content, files)
		if m.Verdict == verdictExcluded && !all {
			out.Excluded = append(out.Excluded, runExcludedDTO{
				Loc: loc, Name: n.Name, Patterns: m.Patterns,
				Reason: "names " + pluralPaths(len(m.Patterns)) + " the diff does not touch",
			})
			continue
		}
		// Both slices explicitly non-nil: the DTO's arrays are [] in the
		// contract, and Go marshals a nil slice as null.
		patterns := m.Patterns
		if patterns == nil {
			patterns = []string{}
		}
		hits := make([]runHitDTO, 0, len(m.On))
		for _, h := range m.On {
			hits = append(hits, runHitDTO(h))
		}
		out.Checks = append(out.Checks, runCheckDTO{
			Loc: loc, Name: n.Name, Description: n.Description, Trigger: trigger,
			Verdict: string(m.Verdict), Patterns: patterns, MatchedOn: hits,
			PortalURL: n.PortalURL, Content: n.Content,
		})
	}
	// matched first, then undecided, each already loc-ordered: the reviewer
	// reads the evidence-backed ones before the ones they must judge.
	sort.SliceStable(out.Checks, func(i, j int) bool {
		return out.Checks[i].Verdict == string(verdictMatched) && out.Checks[j].Verdict != string(verdictMatched)
	})
	return out
}

func pluralPaths(n int) string {
	if n == 1 {
		return "a path"
	}
	return "paths"
}

// pageChecks applies --limit/--offset and reports where to resume.
//
// nextOffset is non-nil ONLY when checks were withheld, so a caller can tell
// "that is everything" from "there is more" without comparing counts — the
// distinction #551 asks for, and the one a truncating reader gets wrong.
func pageChecks(checks []runCheckDTO, limit, offset int) ([]runCheckDTO, *int) {
	if offset >= len(checks) {
		return []runCheckDTO{}, nil
	}
	rest := checks[offset:]
	if limit <= 0 || limit >= len(rest) {
		return rest, nil
	}
	next := offset + limit
	return rest[:limit], &next
}

func renderRun(w io.Writer, r runResultDTO) error {
	fmt.Fprintf(w, "%d file(s) changed, %d check(s) to read", len(r.ChangedFiles), r.Total)
	if r.Returned != r.Total {
		fmt.Fprintf(w, " (showing %d)", r.Returned)
	}
	fmt.Fprintln(w)
	if len(r.Excluded) > 0 {
		fmt.Fprintf(w, "%d excluded — named paths this diff does not touch\n", len(r.Excluded))
	}
	if len(r.Unavailable) > 0 {
		fmt.Fprintf(w, "%d check(s) could not be read: %s\n", len(r.Unavailable), strings.Join(r.Unavailable, ", "))
	}
	if len(r.Checks) == 0 {
		fmt.Fprintln(w, "\nno checks to read for this diff")
		return nil
	}
	fmt.Fprintln(w)
	t := output.NewTable(w, "CHECK", "WHY", "TRIGGER")
	for _, c := range r.Checks {
		why := "judge it"
		if c.Verdict == string(verdictMatched) && len(c.MatchedOn) > 0 {
			why = c.MatchedOn[0].File
		}
		t.Row(c.Loc, why, c.Trigger)
	}
	return t.Flush()
}
