package spec

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// defaultLedgerRegistry is where the registry lives in a hadron-concept checkout.
const defaultLedgerRegistry = "design-discussions/_registry/decisions.md"

func newCmdLedger(f *cmdutil.Factory) *cobra.Command {
	var memory, registryPath string
	cmd := &cobra.Command{
		Use:   "ledger [-m <memory>] [--registry <file>]",
		Short: "Check the decision registry against the spec corpus, both ways",
		Long: `Cross-check the decision registry (hadron-concept's
design-discussions/_registry/decisions.md) against the spec corpus and report
four things:

  1  Decided entries with no Corpus: disposition (from 2026-09-22, by the date
     in the entry's ID).
  2  Codified citations that do not resolve or resolve to a superseded spec;
     a Retired line whose every resolvable target is still live; a relative
     citation with no full citation before it.
  3  Decision IDs the corpus cites that the registry does not have. Slash lists
     (D-2026-06-30-003/004) and ranges (D-2026-08-11-001…008, …-002–004) are
     expanded, so a range cannot hide an unregistered member.
  4  Pending codification entries, as the work queue. Informational: they do
     not fail the command.

Report lines (category 0) say the input could not be read as the grammar
expects: a status or disposition the ledger does not recognise, a shorthand it
cannot expand, a malformed or repeated registry heading, or a spec that listed
but could not be read. They are reported, never guessed.

Only registry IDs (D-YYYY-MM-DD-<NNN|X>) are read. Platform decision codes
(DA, DU-1; cor:dmo:140), ruling numbers (rulings:0061) and R-codes (R218) are
never unregistered decisions. The registry FILE is the authority, not a Hadron
node: pass --registry, or run from a hadron-concept checkout. Read-only: it
mints, rewrites and changes nothing. Pending entries are re-evaluated on every
run; nothing is pinned to a count.

Exit code 5 when any error-level line is reported (so it can gate CI), 0 when
only Pending entries remain. The grammar is
hrn:node:hadronmemory.com:hadron-dev-team-shared:designs:decision-ledger-grammar.`,
		Example: `  hadron spec ledger -m hrn:mem:hadronmemory.com:specs --registry ~/src/hadron-concept/design-discussions/_registry/decisions.md
  hadron spec ledger -m hrn:mem:hadronmemory.com:specs --json
  cd hadron-concept && hadron spec ledger -m hrn:mem:hadronmemory.com:specs`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(registryPath)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "reading the decision registry %s: %v (pass --registry <path to decisions.md>)", registryPath, err)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			all, err := scanAllSpecNodes(cmd.Context(), client, &memURN, nil)
			if err != nil {
				return err
			}
			var cands []replacedCandidate
			var ids []string
			locByID := map[string]string{}
			for _, n := range all {
				if n == nil {
					continue
				}
				ids = append(ids, n.Id)
				locByID[n.Id] = n.Loc
				cands = append(cands, replacedCandidate{ID: n.Id, Loc: n.Loc, MemoryID: n.MemoryId, Tags: n.Tags})
			}
			// An empty corpus would make every citation "unresolved": that is a
			// wrong memory or no read access, not a ledger finding.
			if len(ids) == 0 {
				return exitcode.Newf(exitcode.NotFound, "no specs found in %s (wrong memory, or not visible to you)", memURN)
			}
			repl, err := loadReplacements(cmd.Context(), client, cands)
			if err != nil {
				return err
			}
			specs := make(map[string]bool, len(cands))
			for _, c := range cands {
				specs[c.Loc] = repl[c.ID].Replaced
			}

			nodes, unavailable, err := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
				resp, ferr := api.ReadNodeBatchContent(cmd.Context(), client, chunk)
				if ferr != nil {
					return nil, api.MapError(ferr)
				}
				if resp == nil {
					return nil, nil
				}
				return resp.NodeBatch, nil
			})
			if err != nil {
				return err
			}
			var docs []ledgerDoc
			for _, n := range nodes {
				if n == nil {
					continue
				}
				if n.Content != nil {
					docs = append(docs, ledgerDoc{Citation: n.Loc, Field: "content", Text: *n.Content})
				}
				if n.Abstract != nil {
					docs = append(docs, ledgerDoc{Citation: n.Loc, Field: "abstract", Text: *n.Abstract})
				}
			}

			findings := checkLedger(string(raw), docs, specs)
			// A spec that listed but could not be read leaves category 3 short:
			// say so, or the run reads as cleaner than it is.
			for _, ref := range unavailable {
				cit := locByID[ref]
				if cit == "" {
					cit = ref
				}
				findings = append(findings, ledgerFinding{Rule: ledgerRuleUnreadableSpec, Severity: ledgerError, Citation: cit,
					Locations: []string{}, Message: "listed but unreadable; its text was not searched for decision IDs"})
			}

			hasError := false
			for _, fd := range findings {
				if fd.Severity == ledgerError {
					hasError = true
				}
			}
			if err := output.Write(f.IOStreams, f.JSON, nonNilFindings(findings), func(w io.Writer) error {
				return writeLedgerTable(w, findings, len(specs), len(docs))
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	cmd.Flags().StringVar(&registryPath, "registry", defaultLedgerRegistry, "the decision registry file (hadron-concept: "+defaultLedgerRegistry+")")
	return cmd
}

func nonNilFindings(fs []ledgerFinding) []ledgerFinding {
	if fs == nil {
		return []ledgerFinding{}
	}
	return fs
}

var ledgerCategoryTitle = map[int]string{
	0: "Report lines (input the ledger could not read)",
	1: "1  Decided entries with no Corpus: disposition",
	2: "2  Codified citations that do not hold",
	3: "3  Corpus decision IDs missing from the registry",
	4: "4  Pending codification (work queue)",
}

func writeLedgerTable(w io.Writer, findings []ledgerFinding, specCount, docCount int) error {
	if len(findings) == 0 {
		fmt.Fprintf(w, "✓ the registry and %d spec(s) agree (%d text(s) read)\n", specCount, docCount)
		return nil
	}
	for cat := 1; cat <= 4; cat++ {
		writeLedgerSection(w, cat, findings)
	}
	writeLedgerSection(w, 0, findings)
	return nil
}

func writeLedgerSection(w io.Writer, cat int, findings []ledgerFinding) {
	var rows []ledgerFinding
	for _, f := range findings {
		if f.Category == cat {
			rows = append(rows, f)
		}
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "%s: %d\n", ledgerCategoryTitle[cat], len(rows))
	t := output.NewTable(w, "RULE", "ID", "CITATION", "WHERE", "MESSAGE")
	for _, f := range rows {
		t.Row(f.Rule, output.SafeCell(f.ID), output.SafeCell(f.Citation),
			output.SafeCell(strings.Join(firstN(f.Locations, 3), ", ")+moreSuffix(len(f.Locations), 3)), output.SafeCell(f.Message))
	}
	_ = t.Flush()
	fmt.Fprintln(w)
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func moreSuffix(total, shown int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf(" (+%d; --json lists all)", total-shown)
}
