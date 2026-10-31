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

const templateHelp = `Read and edit an Agent's ticker templates. Reading requires access to the
Agent; writing requires its editor (Agent ADMIN), not merely an App manager.

Templates are snapshots at cast: edits and deletion affect future casts only.
Existing workers and their tickers are untouched. To repair one existing worker,
use "hadron ticker apply-templates --worker <worker-ref>" separately.

The displayed cadence is the template's requested cadence in seconds; casting
raises it to the App's floor. The client never clamps it. Identity is the opaque
template id. Read its revision, then supply --expected-revision to update or
delete: a stale revision exits 5 with no automatic fetch or retry.`

func newCmdAgentTickerTemplates(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "ticker-templates", Aliases: []string{"ticker-template"}, Short: "Read and edit Agent ticker templates for future casts", Long: templateHelp}
	group.AddCommand(newTemplateList(f), newTemplateWrite(f, false), newTemplateWrite(f, true), newTemplateDelete(f))
	return group
}
func templateTable(w io.Writer, rows []tickerTemplateDTO) error {
	table := output.NewTable(w, "ID", "NAME", "TICK CODE", "CADENCE (s)", "RELEVANCE", "REASONS", "REVISION", "QUIET HOURS", "AGENT")
	for _, r := range rows {
		quiet := "-"
		if r.QuietHours != nil {
			quiet = string(*r.QuietHours)
		}
		table.Row(output.SafeCell(r.ID), output.SafeCell(r.Name), output.SafeCell(r.TickCode), fmt.Sprint(r.CadenceSeconds), output.SafeCell(r.Relevance), output.SafeCell(strings.Join(r.Reasons, ",")), fmt.Sprint(r.Revision), output.SafeCell(quiet), output.SafeCell(r.Agent.Name))
	}
	return table.Flush()
}
func writeTemplate(f *cmdutil.Factory, row gen.AgentTickerTemplateFields) error {
	dto, err := templateDTO(row)
	if err != nil {
		return err
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return templateTable(w, []tickerTemplateDTO{dto}) })
}
func newTemplateList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "list <agent-ref>", Short: "List an Agent's ticker templates in server order", Long: templateHelp + "\n\nList prints a JSON array ([] when empty), in name/id order from the server.", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd := &cobra.Command{Use: "create <agent-ref>", Short: "Create a ticker template for future casts", Long: templateHelp + "\n\nOmitted options use server defaults. Returns the template including tickCode and revision.", Args: cobra.ExactArgs(1)}
	keep := ""
	if update {
		cmd.Use = "update <template-id>"
		cmd.Short = "Update only the named fields at the revision you read"
		cmd.Long = templateHelp + "\n\nOmitted fields are preserved; use --clear-quiet-hours to remove quiet hours.\nPass at least one field flag; --expected-revision alone changes nothing."
		keep = "; omitted preserves the current value"
	}
	cmd.Flags().StringVar(&v.name, "name", "", "Template name"+keep)
	cmd.Flags().IntVar(&v.cadence, "cadence-seconds", 0, "Requested cadence in whole seconds; server validates bounds"+keep)
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
			anyField := false
			for _, flag := range []string{"name", "cadence-seconds", "relevance", "reasons", "quiet-hours", "clear-quiet-hours"} {
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
	cmd := &cobra.Command{Use: "delete <template-id>", Short: "Delete one template without touching existing tickers", Long: templateHelp + "\n\nSoft-deletes the template; existing generated tickers and subscriptions are untouched.\nNon-interactive callers must pass --yes. Returns {id,deleted:true} on success.", Args: cobra.ExactArgs(1)}
	cmd.Flags().IntVar(&revision, "expected-revision", 0, "Revision read from the template (required)")
	_ = cmd.MarkFlagRequired("expected-revision")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm template deletion without prompting")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "Agent ticker template "+args[0]); err != nil {
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
			_, err := fmt.Fprintf(w, "Deleted ticker template %s\n", output.SafeCell(args[0]))
			return err
		})
	}
	return cmd
}
