package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/ticker"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type templateAgentDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type tickerTemplateDTO struct {
	ID             string           `json:"id"`
	Revision       int              `json:"revision"`
	ShapeVersion   int              `json:"shapeVersion"`
	Name           string           `json:"name"`
	TickCode       string           `json:"tickCode"`
	CadenceSeconds int              `json:"cadenceSeconds"`
	QuietHours     *json.RawMessage `json:"quietHours"`
	Relevance      string           `json:"relevance"`
	Reasons        []string         `json:"reasons"`
	CreatedAt      string           `json:"createdAt"`
	UpdatedAt      *string          `json:"updatedAt"`
	Agent          templateAgentDTO `json:"agent"`
}

func templateDTO(row gen.AgentTickerTemplateFields) (tickerTemplateDTO, error) {
	if row.Id == "" || row.Agent == nil {
		return tickerTemplateDTO{}, exitcode.Newf(exitcode.Error, "server returned an incomplete ticker template")
	}
	reasons := []string{}
	for _, r := range row.Reasons {
		reasons = append(reasons, strings.ReplaceAll(strings.ToLower(string(r)), "_", "-"))
	}
	return tickerTemplateDTO{row.Id, row.Revision, row.ShapeVersion, row.Name, row.TickCode, row.CadenceSeconds, row.QuietHours, strings.ToLower(string(row.Relevance)), reasons, row.CreatedAt, row.UpdatedAt, templateAgentDTO{row.Agent.Id, row.Agent.Name}}, nil
}

const templateHelp = `An Agent's ticker template declares a periodic wake created when a worker is
cast (created from that Agent into an App). Reading requires access to the
Agent; writing requires its editor (Agent ADMIN), not merely an App manager.
Use the same Agent reference as "agent get" for list/create. Update/delete take
only the opaque template id returned by list. Uses signed-in credentials; a
bound worker session is not sent for these writes.

Templates are snapshots at cast: edits and deletion affect future casts only.
Existing workers and their tickers are untouched. To repair one existing worker,
use "hadron ticker apply-templates --worker <worker-ref>" separately.

The displayed cadence is the template's requested cadence in seconds; casting
raises it to the App's floor. The client never clamps it. Identity is the opaque
template id. Read its revision, then supply --expected-revision to update or
delete: a stale revision exits 5 with no automatic fetch or retry.
Unreadable Agent/template: exit 4; non-editor: exit 8; name/cap/revision conflict:
exit 5. Invalid input is exit 2. tickCode is server-derived from the name.`

func newCmdAgentTickerTemplates(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "ticker-templates", Aliases: []string{"ticker-template"}, Short: "Read and edit Agent ticker templates for future casts", Long: templateHelp}
	group.AddCommand(newTemplateList(f), newTemplateWrite(f, false), newTemplateWrite(f, true), newTemplateDelete(f))
	return group
}
func templateTable(w io.Writer, rows []tickerTemplateDTO) error {
	table := output.NewTable(w, "ID", "REVISION", "NAME", "TICK CODE", "REQUESTED CADENCE (s)", "RELEVANCE", "REASONS", "QUIET HOURS", "AGENT")
	for _, r := range rows {
		table.Row(output.SafeCell(r.ID), fmt.Sprint(r.Revision), output.SafeCell(r.Name), output.SafeCell(r.TickCode), fmt.Sprint(r.CadenceSeconds), output.SafeCell(r.Relevance), output.SafeCell(strings.Join(r.Reasons, ",")), output.SafeCell(templateQuietSummary(r.QuietHours)), output.SafeCell(r.Agent.Name))
	}
	return table.Flush()
}
func templateQuietSummary(raw *json.RawMessage) string {
	if raw == nil {
		return "-"
	}
	var q struct {
		Timezone string            `json:"timezone"`
		Windows  []json.RawMessage `json:"windows"`
	}
	if json.Unmarshal(*raw, &q) != nil || q.Timezone == "" || len(q.Windows) == 0 {
		return "configured (see --json)"
	}
	suffix := "windows"
	if len(q.Windows) == 1 {
		suffix = "window"
	}
	return fmt.Sprintf("%s, %d %s", q.Timezone, len(q.Windows), suffix)
}

func writeTemplate(f *cmdutil.Factory, row gen.AgentTickerTemplateFields) error {
	dto, err := templateDTO(row)
	if err != nil {
		return err
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if err := templateTable(w, []tickerTemplateDTO{dto}); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w, "Applies to future casts only; existing workers and tickers stay untouched.")
		return err
	})
}
func newTemplateList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "list <agent-ref>", Aliases: []string{"ls"}, Short: "List an Agent's ticker templates in server order", Long: `List one Agent's ticker templates in the server's name/id order.
By default, prints a table with id, revision and requested cadence; --json prints
an array ([] when empty) with full quiet hours and all template fields.
Use the same Agent reference as "agent get". Requires read access to the Agent.
See "hadron agent ticker-templates --help" for shared rules and permissions.`,
		Example: `  # Set AGENT_REF to the Agent reference you use with agent get.
  hadron agent ticker-templates list "$AGENT_REF" --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentTickerTemplates(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no ticker-template list")
			}
			rows := []tickerTemplateDTO{}
			for _, r := range resp.AgentTickerTemplates {
				if r == nil {
					return exitcode.Newf(exitcode.Error, "server returned an incomplete ticker template")
				}
				dto, e := templateDTO(r.AgentTickerTemplateFields)
				if e != nil {
					return e
				}
				rows = append(rows, dto)
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error { return templateTable(w, rows) })
		}}
}

type templateFlags struct {
	name, quiet, relevance, reasons string
	cadence, revision               int
	clearQuiet                      bool
}

func (v *templateFlags) patch(cmd *cobra.Command) (*gen.AgentTickerTemplatePatch, error) {
	p := &gen.AgentTickerTemplatePatch{}
	changed := cmd.Flags().Changed
	if changed("name") {
		if strings.TrimSpace(v.name) == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--name must not be empty")
		}
		p.Name = &v.name
	}
	if changed("cadence-seconds") {
		p.CadenceSeconds = &v.cadence
	}
	if changed("quiet-hours") {
		q, err := ticker.ParseQuietHours(v.quiet)
		if err != nil {
			return nil, err
		}
		p.QuietHours = q
	}
	if changed("clear-quiet-hours") {
		p.ClearQuietHours = &v.clearQuiet
	}
	if changed("relevance") {
		r, err := ticker.ParseRelevance(v.relevance)
		if err != nil {
			return nil, err
		}
		p.Relevance = &r
	}
	if changed("reasons") {
		r, err := ticker.ParseReasons(v.reasons)
		if err != nil {
			return nil, err
		}
		p.Reasons = r
	}
	return p, nil
}
func newTemplateWrite(f *cmdutil.Factory, update bool) *cobra.Command {
	v := &templateFlags{}
	cmd := &cobra.Command{Use: "create <agent-ref>", Short: "Create a ticker template for future casts", Long: `Create one ticker template for future casts; existing workers and tickers stay
untouched. Requires the Agent's editor, not merely an App manager.
Name and requested cadence in whole seconds are required. Omitted relevance is
all, reasons read-team-channel, quiet hours none. The cast applies the App floor.
Returns the template, including id, tickCode and revision (one object with --json).
Quiet hours use an IANA timezone and windows with ISO days (1 Monday to 7 Sunday),
from/to in HH:MM. The server validates the schedule.
See "hadron agent ticker-templates --help" for shared rules and permissions.`,
		Example: `  # Set AGENT_REF to the Agent reference you use with agent get.
  hadron agent ticker-templates create "$AGENT_REF" --name heartbeat --cadence-seconds 3600 --reasons every-tick --quiet-hours '{"timezone":"UTC","windows":[{"days":[1,2,3,4,5],"from":"22:00","to":"07:00"}]}' --json`, Args: cobra.ExactArgs(1)}
	keep := ""
	if update {
		cmd.Use = "update <template-id>"
		cmd.Short = "Update only the named fields at the revision you read"
		cmd.Long = `Change only the named fields of one template, at the revision read from list.
Pass at least one field flag: omitted fields are preserved, and
--expected-revision alone changes nothing. Use --clear-quiet-hours to remove
quiet hours, or --quiet-hours to set them; the flags exclude each other.
A stale revision exits 5 without fetching or retrying. Returns the template
(one object with --json). Edits reach future casts only; existing workers and
tickers stay untouched. Cadence is requested; casting applies the App floor.
See "hadron agent ticker-templates --help" for shared rules and permissions.`
		cmd.Example = `  # Set TEMPLATE_ID and REVISION from agent ticker-templates list.
  hadron agent ticker-templates update "$TEMPLATE_ID" --expected-revision "$REVISION" --cadence-seconds 7200 --clear-quiet-hours --json`
		keep = "; omitted preserves the current value"
	}
	required := " (required)"
	if update {
		required = ""
	}
	cmd.Flags().StringVar(&v.name, "name", "", "Template name"+required+keep)
	cmd.Flags().IntVar(&v.cadence, "cadence-seconds", 0, "Requested cadence in whole seconds; server validates bounds"+required+keep)
	cmd.Flags().StringVar(&v.relevance, "relevance", "", "all or mentions"+keep)
	cmd.Flags().StringVar(&v.reasons, "reasons", "", "Nonempty reasons: read-team-channel,every-tick,live-check"+keep)
	cmd.Flags().StringVar(&v.quiet, "quiet-hours", "", "Quiet-hours JSON object with timezone and weekly windows"+keep)
	if update {
		cmd.Flags().IntVar(&v.revision, "expected-revision", 0, "Revision read from the template (required)")
		_ = cmd.MarkFlagRequired("expected-revision")
		cmd.Flags().BoolVar(&v.clearQuiet, "clear-quiet-hours", false, "Remove quiet hours")
		cmd.MarkFlagsMutuallyExclusive("quiet-hours", "clear-quiet-hours")
	} else {
		_ = cmd.MarkFlagRequired("name")
		_ = cmd.MarkFlagRequired("cadence-seconds")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if update {
			anyField := v.clearQuiet
			for _, flag := range []string{"name", "cadence-seconds", "relevance", "reasons", "quiet-hours"} {
				anyField = anyField || cmd.Flags().Changed(flag)
			}
			if !anyField {
				return exitcode.Newf(exitcode.Usage, "nothing to update; pass at least one field flag")
			}
		}
		patch, err := v.patch(cmd)
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		if update {
			resp, err := gen.UpdateAgentTickerTemplate(cmd.Context(), client, args[0], v.revision, patch)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateAgentTickerTemplate == nil {
				return exitcode.Newf(exitcode.Error, "server returned no ticker template")
			}
			return writeTemplate(f, resp.UpdateAgentTickerTemplate.AgentTickerTemplateFields)
		}
		input := &gen.AgentTickerTemplateInput{Name: v.name, CadenceSeconds: v.cadence, QuietHours: patch.QuietHours, Reasons: patch.Reasons, Relevance: patch.Relevance}
		resp, err := gen.CreateAgentTickerTemplate(cmd.Context(), client, args[0], input)
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || resp.CreateAgentTickerTemplate == nil {
			return exitcode.Newf(exitcode.Error, "server returned no ticker template")
		}
		return writeTemplate(f, resp.CreateAgentTickerTemplate.AgentTickerTemplateFields)
	}
	return cmd
}
func newTemplateDelete(f *cmdutil.Factory) *cobra.Command {
	var revision int
	var yes bool
	cmd := &cobra.Command{Use: "delete <template-id>", Short: "Delete one template without touching existing tickers", Long: `Remove one template from future casts. Existing workers, generated tickers and
subscriptions stay untouched. This command group has no restore command.
Supply the id and revision read from list; a stale revision exits 5 without retry.
Requires the Agent's editor. Prompts on a TTY; non-interactive callers need --yes.
Prints confirmation, or {id,deleted:true} with --json.
See "hadron agent ticker-templates --help" for shared rules and permissions.`,
		Example: `  # Set TEMPLATE_ID and REVISION from agent ticker-templates list.
  hadron agent ticker-templates delete "$TEMPLATE_ID" --expected-revision "$REVISION" --yes --json`, Args: cobra.ExactArgs(1)}
	cmd.Flags().IntVar(&revision, "expected-revision", 0, "Revision read from the template (required)")
	_ = cmd.MarkFlagRequired("expected-revision")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm template deletion without prompting")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cmdutil.Confirm(f.IOStreams, yes, "Delete Agent ticker template "+output.SafeCell(args[0])+"? Existing workers and tickers stay untouched."); err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.DeleteAgentTickerTemplate(cmd.Context(), client, args[0], revision)
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || !resp.DeleteAgentTickerTemplate {
			return exitcode.Newf(exitcode.Error, "server did not confirm template deletion")
		}
		dto := struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		}{args[0], true}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "Removed ticker template %s from future casts; existing workers and tickers stay untouched.\n", output.SafeCell(args[0]))
			return err
		})
	}
	return cmd
}
