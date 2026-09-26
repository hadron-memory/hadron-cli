package memory

import (
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

// applyEntryDTO is one template rule's outcome. `rule` is the rule in force
// for the role afterwards; on a dry run, the existing rule, or null for a
// would-be APPLIED.
type applyEntryDTO struct {
	Role    string           `json:"role"`
	Outcome string           `json:"outcome"`
	Rule    *nodeRoleRuleDTO `json:"rule"`
}

// applyResultDTO is `template apply`'s --json shape (cli#716 slice 3, over
// hadron-server#1334's applyMemoryConfigTemplate).
type applyResultDTO struct {
	MemoryID         string             `json:"memoryId"`
	Template         sourceTemplateDTO  `json:"template"`
	TemplateRevision int                `json:"templateRevision"`
	Required         bool               `json:"required"`
	DryRun           bool               `json:"dryRun"`
	Entries          []applyEntryDTO    `json:"entries"`
	Warnings         []configWarningDTO `json:"warnings"`
}

func newCmdTemplateApply(f *cmdutil.Factory) *cobra.Command {
	var (
		dryRun   bool
		yes      bool
		expected int
	)
	cmd := &cobra.Command{
		Use:   "apply <templateId> <memoryRef>",
		Short: "Copy a template's rules into one memory's config",
		Long: `Copy a template's rules into ONE memory's config. It is a one-off copy: later
edits to the template never reach the memory; re-applying is explicit. You
must manage both the memory and the template (exit 4 otherwise, exactly as for
one that does not exist).

The report has one entry per template rule:
  APPLIED           the memory had no rule for the role; the template's was
                    copied in (locked, if the template is required)
  SKIPPED_CONFLICT  the memory already had one, and a non-required template
                    never overrides it
  REPLACED          a REQUIRED template overwrote the memory's rule, which is
                    now locked
  SKIPPED_LOCKED    a required template met a locked rule you have no
                    authority to move; the rule stands

The command previews first. When the preview would REPLACE a rule, it asks
before applying (a prompt on a terminal; --yes non-interactively), then applies
exactly the template revision it showed you: a template changed in between is
refused (exit 5) rather than applied unseen. That guards the TEMPLATE; the
memory's own rules are re-read by the apply, and its report is authoritative.

--dry-run prints the preview and writes nothing. --expected-revision pins the
template revision yourself, e.g. one a teammate approved from a dry run.

A rule the template references by a task that was since deleted refuses the
whole apply (exit 5; fix the template with template update). Skipped entries
are part of the report, not failures: the exit code is 0.`,
		Example: `  hadron memory config template apply tmpl_123 hrn:mem:acme.com:kb --dry-run
  hadron memory config template apply tmpl_123 hrn:mem:acme.com:kb --yes --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			templateRef, memoryRef := strings.TrimSpace(args[0]), cmdutil.CanonicalMemoryRef(args[1])
			if templateRef == "" {
				return exitcode.Newf(exitcode.Usage, "a template id is required")
			}
			var pinned *int
			if cmd.Flags().Changed("expected-revision") {
				if expected < 1 {
					return exitcode.Newf(exitcode.Usage, "--expected-revision must be a positive revision, got %d", expected)
				}
				pinned = &expected
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			preview, err := applyTemplate(cmd, client, templateRef, memoryRef, pinned, true)
			if err != nil {
				return err
			}
			if dryRun {
				return writeApplyResult(f, preview)
			}
			if replaced := rolesWith(preview, "REPLACED"); len(replaced) > 0 {
				if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
					"Apply required template %q to %s? It REPLACES the memory's rule for %s, and the replaced rules are locked.",
					preview.Template.Name, preview.MemoryID, strings.Join(replaced, ", "))); err != nil {
					return err
				}
			}
			// Apply exactly the revision the preview showed (or the one the
			// caller pinned, which the preview already checked).
			rev := preview.TemplateRevision
			result, err := applyTemplate(cmd, client, templateRef, memoryRef, &rev, false)
			if err != nil {
				return err
			}
			return writeApplyResult(f, result)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what applying would do, writing nothing")
	cmd.Flags().BoolVar(&yes, "yes", false, "apply without asking when the preview replaces a rule (required non-interactively)")
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "apply only this template revision (exit 5 if the template has changed)")
	return cmd
}

func applyTemplate(cmd *cobra.Command, client graphql.Client, templateRef, memoryRef string, expected *int, dryRun bool) (applyResultDTO, error) {
	resp, err := gen.ApplyMemoryConfigTemplate(cmd.Context(), client, templateRef, memoryRef, expected, dryRun)
	if err != nil {
		return applyResultDTO{}, api.MapError(err)
	}
	r := resp.ApplyMemoryConfigTemplate
	if r == nil {
		return applyResultDTO{}, exitcode.Newf(exitcode.Error, "the server returned no apply result")
	}
	dto := applyResultDTO{
		MemoryID: r.MemoryId, TemplateRevision: r.TemplateRevision, Required: r.Required, DryRun: r.DryRun,
		Entries: []applyEntryDTO{}, Warnings: warningsFrom(r.Warnings),
	}
	if t := r.Template; t != nil {
		dto.Template = sourceTemplateDTO{ID: t.Id, Name: t.Name, Deleted: t.Deleted}
	}
	for _, e := range r.Entries {
		if e == nil {
			continue
		}
		entry := applyEntryDTO{Role: e.Role, Outcome: string(e.Outcome)}
		if e.Rule != nil {
			rule := dtoFromRule(&e.Rule.NodeRoleRuleFields)
			entry.Rule = &rule
		}
		dto.Entries = append(dto.Entries, entry)
	}
	return dto, nil
}

// rolesWith lists the roles whose outcome is `outcome`, sorted.
func rolesWith(r applyResultDTO, outcome string) []string {
	var roles []string
	for _, e := range r.Entries {
		if e.Outcome == outcome {
			roles = append(roles, e.Role)
		}
	}
	sort.Strings(roles)
	return roles
}

func writeApplyResult(f *cmdutil.Factory, r applyResultDTO) error {
	return output.Write(f.IOStreams, f.JSON, r, func(w io.Writer) error {
		verb := "Applied"
		if r.DryRun {
			verb = "Would apply"
		}
		kind := "template"
		if r.Required {
			kind = "required template"
		}
		fmt.Fprintf(w, "%s %s %s (%s, revision %d) to memory %s\n\n", verb, kind, r.Template.Name, r.Template.ID, r.TemplateRevision, r.MemoryID)
		if len(r.Entries) == 0 {
			fmt.Fprintln(w, "The template has no rules; nothing to apply.")
		} else {
			t := output.NewTable(w, "ROLE", "OUTCOME", "LOCKED")
			for _, e := range r.Entries {
				locked := "—"
				if e.Rule != nil {
					locked = yesNo(e.Rule.Locked)
				}
				t.Row(e.Role, e.Outcome, locked)
			}
			if err := t.Flush(); err != nil {
				return err
			}
		}
		if r.DryRun {
			fmt.Fprintf(w, "\ndry run: nothing was written. To apply exactly this, rerun without --dry-run and with --expected-revision %d.\n", r.TemplateRevision)
		}
		renderWarnings(f.IOStreams, r.Warnings)
		return nil
	})
}
