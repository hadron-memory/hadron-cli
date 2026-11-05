package schedule

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type previewFlowDTO struct {
	Name   string   `json:"name"`
	URN    string   `json:"urn"`
	Tools  []string `json:"tools"`
	Writes []string `json:"writes"`
}
type previewSummaryDTO struct {
	Name     string           `json:"name"`
	Schedule *string          `json:"schedule"`
	Reads    []string         `json:"reads"`
	Writes   []string         `json:"writes"`
	CannotDo []string         `json:"cannotDo"`
	Tools    []string         `json:"tools"`
	Limits   []string         `json:"limits"`
	Flows    []previewFlowDTO `json:"flows"`
}
type previewDTO struct {
	Outcome string             `json:"outcome"`
	Hash    *string            `json:"hash"`
	Reason  *string            `json:"reason"`
	Summary *previewSummaryDTO `json:"summary"`
}

var previewHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

type previewError struct{ error }

func (e previewError) Unwrap() error        { return e.error }
func (e previewError) HumanMessage() string { return output.SafeLine(e.Error(), 1024) }

func newCmdPreview(f *cmdutil.Factory) *cobra.Command {
	var app, entry, cron, runAt, tz, agent, aiConfig string
	var args []string
	var asSelf, disabled bool
	var budget cmdutil.BudgetFlags
	cmd := &cobra.Command{Use: "preview --app <ref> --entry <node-urn> (--cron '<expr>' | --run-at <RFC3339>)", Short: "Preview a schedule's automation without creating it", Long: `Preview a schedule before it exists (cor:agt:010:01). This is one read: no
schedule, adoption or run is created, and there is no automatic create or retry.
Requires a provider containing hadron-server#3463 (b7cf14b0).

The result is a snapshot: no reservation and no authority to create. A changed
automation can change the hash. Scheduling by a worker requires human authorization
or the applicable grant; the server decides admission.

preview does not validate policy or scope; create may refuse them even after an ADOPTABLE preview.
--name, --scope and --policy are not preview inputs and are not accepted here.

App and entry references, timing, budgets and args follow schedule create:
--app takes an id or URN, defaulting to your App context; --entry is a fully-
qualified node URN (legacy org::memory::loc also accepted). Exactly one of
--cron or --run-at is required. Cron is five fields in --tz (default UTC);
run-at is an RFC3339 instant. Empty timing flags are refused locally. The
server validates the expression/instant, permissions, Agent and configuration.
--agent selects an installed Agent; omission lets the server resolve it.
--as-self uses the authenticated person as run principal; an App key cannot.
--arg key=value repeats, parsing JSON values or preserving a string; omitted
args/config/Agent/timezone/budgets are not sent. Budget values must be positive
and the server enforces the platform maximum. --disabled previews switched-off
timing (the default is enabled). There is no prompt or write to confirm.

ADOPTABLE exits 0 with the hash and complete summary: reads, writes, cannot-do,
tools, flows, schedule and limits. The schedule line is the server's own string,
not a local timing rewrite. CANNOT_BE_ADOPTED exits 5 with only its closed reason
(MEMORY_UNREADABLE or OTHER), never an entry/memory identity. --json returns
{outcome,hash,reason,summary}; hash/summary are null for CANNOT_BE_ADOPTED and
reason is null for ADOPTABLE. Summary lists are arrays, including when empty.
Text cells are terminal-safe and bounded; --json preserves the server values.
Malformed/nil replies or unsupported providers fail nonzero with no write
fallback. Server refusals keep create's mapping (for example 2 bad input,
3 unauthenticated, 4 not found, 8 forbidden); transport failures exit 7.`, Example: `  hadron schedule preview --app acme.com:ops --entry hrn:node:acme.com:ops:tasks:digest --cron '0 6 * * *' --json`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("cron") == cmd.Flags().Changed("run-at") {
			return exitcode.Newf(exitcode.Usage, "give exactly one of --cron or --run-at")
		}
		if (cmd.Flags().Changed("cron") && strings.TrimSpace(cron) == "") || (cmd.Flags().Changed("run-at") && strings.TrimSpace(runAt) == "") {
			return exitcode.Newf(exitcode.Usage, "--cron/--run-at cannot be empty")
		}
		appRef, err := cmdutil.ResolveAppRef(f, app)
		if err != nil {
			return err
		}
		entryURN, err := cmdutil.CanonicalNodeURN(entry)
		if err != nil {
			return err
		}
		eventData, err := cmdutil.KeyValsToJSON(args, "arg")
		if err != nil {
			return err
		}
		tokens, actions, err := budget.Values(cmd)
		if err != nil {
			return err
		}
		enabled := !disabled
		in := &gen.PreviewAgentScheduleInput{AppRef: appRef, EntryNodeUrn: entryURN, Enabled: &enabled, EventData: eventData, BudgetTokens: tokens, BudgetActions: actions}
		if cmd.Flags().Changed("cron") {
			in.Cron = &cron
		}
		if cmd.Flags().Changed("run-at") {
			in.RunAt = &runAt
		}
		if tz != "" {
			in.Timezone = &tz
		}
		if agent != "" {
			in.AgentRef = &agent
		}
		if aiConfig != "" {
			in.AiConfigName = &aiConfig
		}
		if asSelf {
			in.RunAsSelf = &asSelf
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.PreviewAgentSchedule(cmd.Context(), client, in)
		if err != nil {
			return previewError{api.WithServerCode(api.MapError(err), err)}
		}
		if resp == nil || resp.PreviewAgentSchedule == nil {
			return malformedPreview()
		}
		v := resp.PreviewAgentSchedule
		dto := previewDTO{Outcome: string(v.Outcome), Hash: v.Hash}
		switch dto.Outcome {
		case "CANNOT_BE_ADOPTED":
			if v.Hash != nil || v.Summary != nil || v.Reason == nil || (*v.Reason != "MEMORY_UNREADABLE" && *v.Reason != "OTHER") {
				return malformedPreview()
			}
			reason := string(*v.Reason)
			dto.Reason = &reason
		case "ADOPTABLE":
			if v.Hash == nil || !previewHash.MatchString(*v.Hash) || v.Reason != nil || v.Summary == nil {
				return malformedPreview()
			}
			s := v.Summary
			if s.Name == "" || s.Schedule == nil || (s.Schedule != nil && *s.Schedule == "") || s.Reads == nil || s.Writes == nil || s.CannotDo == nil || s.Tools == nil || s.Limits == nil || s.Flows == nil {
				return malformedPreview()
			}
			dto.Summary = &previewSummaryDTO{Name: s.Name, Schedule: s.Schedule, Reads: s.Reads, Writes: s.Writes, CannotDo: s.CannotDo, Tools: s.Tools, Limits: s.Limits, Flows: []previewFlowDTO{}}
			for _, fl := range s.Flows {
				if fl == nil || fl.Name == "" || fl.Urn == "" || fl.Tools == nil || fl.Writes == nil {
					return malformedPreview()
				}
				dto.Summary.Flows = append(dto.Summary.Flows, previewFlowDTO{Name: fl.Name, URN: fl.Urn, Tools: fl.Tools, Writes: fl.Writes})
			}
		default:
			return malformedPreview()
		}
		if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writePreview(w, dto) }); err != nil {
			return err
		}
		if dto.Outcome == "CANNOT_BE_ADOPTED" {
			return exitcode.Silent(exitcode.Conflict)
		}
		return nil
	}}
	cmd.Flags().StringVar(&app, "app", "", "App id or URN (defaults to the App context)")
	cmd.Flags().StringVar(&entry, "entry", "", "fully-qualified entry node URN (required)")
	cmd.Flags().StringVar(&cron, "cron", "", "5-field cron expression, instead of --run-at")
	cmd.Flags().StringVar(&runAt, "run-at", "", "one-shot RFC3339 instant, instead of --cron")
	cmd.Flags().StringVar(&tz, "tz", "", "IANA timezone (default UTC)")
	cmd.Flags().StringVar(&agent, "agent", "", "specific installed Agent id or URN")
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "named AI config")
	cmd.Flags().BoolVar(&asSelf, "as-self", false, "preview running on behalf of you (authenticated person only)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "preview switched-off timing")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "static template arg key=value (repeatable; JSON or string)")
	budget.Add(cmd, false)
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}
func malformedPreview() error {
	return exitcode.Newf(exitcode.Unavailable, "server returned a malformed schedule preview; no write was attempted")
}
func writePreview(w io.Writer, d previewDTO) error {
	if d.Outcome == "CANNOT_BE_ADOPTED" {
		_, err := fmt.Fprintf(w, "CANNOT_BE_ADOPTED: %s\n", *d.Reason)
		return err
	}
	p := previewText{w: w}
	p.line("ADOPTABLE")
	p.line("Hash: %s", *d.Hash)
	s := d.Summary
	p.line("Name: %s", output.SafeLine(s.Name, 4096))
	p.line("Schedule: %s", output.SafeLine(*s.Schedule, 4096))
	p.list("Reads", s.Reads)
	p.list("Writes", s.Writes)
	p.list("Cannot do", s.CannotDo)
	p.list("Tools", s.Tools)
	p.list("Limits", s.Limits)
	p.line("Flows:")
	for _, fl := range s.Flows {
		p.line("  %s (%s)", output.SafeLine(fl.Name, 4096), output.SafeLine(fl.URN, 4096))
		p.list("  Tools", fl.Tools)
		p.list("  Writes", fl.Writes)
	}
	return p.err
}

type previewText struct {
	w   io.Writer
	err error
}

func (p *previewText) line(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format+"\n", args...)
	}
}
func (p *previewText) list(label string, values []string) {
	p.line("%s:", label)
	if len(values) == 0 {
		p.line("  (none listed)")
	}
	for _, v := range values {
		p.line("  - %s", output.SafeLine(v, 4096))
	}
}
