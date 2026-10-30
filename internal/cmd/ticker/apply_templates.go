package ticker

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// appliedTemplateDTO is a template as the apply reports it: id and name only
// (the manager need not be able to read the Agent that declares it).
type appliedTemplateDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type appliedTickerDTO struct {
	Ticker                  tickerDTO          `json:"ticker"`
	Template                appliedTemplateDTO `json:"template"`
	EffectiveCadenceSeconds int                `json:"effectiveCadenceSeconds"`
}

// applyTemplatesDTO mirrors applyAgentTickerTemplates' result. The floor label
// is text-only: the server does not say whether its floor raised a cadence.
type applyTemplatesDTO struct {
	Created        []appliedTickerDTO   `json:"created"`
	AlreadyPresent []appliedTemplateDTO `json:"alreadyPresent"`
}

// applyRefusalCodes are the apply's documented refusals (hadron-server#2860),
// kept as the message prefix. The last two refuse the whole call.
var applyRefusalCodes = []string{"FORBIDDEN", "WORKER_NOT_FOUND", "WORKER_AGENT_NOT_INSTALLED", "TICKER_LIMIT_REACHED", "TICKER_NAME_TAKEN"}

func applyFailure(err error) error {
	mapped := api.MapError(err)
	for _, code := range applyRefusalCodes {
		if api.HasErrorCode(err, code) {
			// The server's own message usually says so already (#1296).
			suffix := ""
			if (code == "TICKER_LIMIT_REACHED" || code == "TICKER_NAME_TAKEN") && !strings.Contains(strings.ToLower(mapped.Error()), "nothing was created") {
				suffix = "; nothing was created"
			}
			return exitcode.Newf(exitcode.FromError(mapped), "%s: %s%s", code, mapped.Error(), suffix)
		}
	}
	return mapped
}

func newApplyTemplates(f *cmdutil.Factory) *cobra.Command {
	worker := ""
	c := &cobra.Command{
		Use:   "apply-templates --worker <id-or-URN>",
		Short: "Create the Agent's template tickers a worker is missing",
		Long: `Create the tickers its Agent's ticker templates declare that one worker is
missing: the repair for a worker cast before its Agent had templates
(cor:agt:080:33, :34; hadron-server#2096). Each created ticker is owned by and
subscribed to the worker. Nothing existing is edited or deleted: a template the
worker already has a live ticker for is listed as already present
(alreadyPresent in --json) and left as it is, so a second run creates nothing.

Run it as whoever manages the App, acting as yourself. It applies to one worker
per call, never across workers, and never sends a worker session, even in a
bound worktree: the server refuses a worker session for it.

A created ticker shows its effective cadence, labelled "the App's floor" when
it equals the App's cadence floor. If the App's live-ticker cap cannot take
every missing ticker (TICKER_LIMIT_REACHED), or a generated name is already a
live ticker's (TICKER_NAME_TAKEN), the whole call is refused:
nothing was created.

--json prints the server's result: created (ticker, template,
effectiveCadenceSeconds) and alreadyPresent (id, name).`,
		Example: `  hadron ticker apply-templates --worker hrn:worker:acme.com:ops:iris
  hadron ticker apply-templates --worker <worker-id> --json`,
		Args: cobra.NoArgs,
	}
	c.Flags().StringVar(&worker, "worker", "", "Worker ID or fully-qualified worker URN (required)")
	_ = c.MarkFlagRequired("worker")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		wref, err := qualifiedRef(worker, "worker")
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.ApplyAgentTickerTemplates(cmd.Context(), client, wref)
		if err != nil {
			return applyFailure(err)
		}
		if r == nil || r.ApplyAgentTickerTemplates == nil {
			return exitcode.Newf(exitcode.Error, "server returned no apply result; verify the worker's tickers before retrying")
		}
		dto := applyTemplatesDTO{Created: []appliedTickerDTO{}, AlreadyPresent: []appliedTemplateDTO{}}
		for _, x := range r.ApplyAgentTickerTemplates.Created {
			if x == nil || x.Ticker == nil {
				continue
			}
			a := appliedTickerDTO{Ticker: tickerFrom(x.Ticker.TickerFields), EffectiveCadenceSeconds: x.EffectiveCadenceSeconds}
			if x.Template != nil {
				a.Template = appliedTemplateDTO{x.Template.Id, x.Template.Name}
			}
			dto.Created = append(dto.Created, a)
		}
		for _, x := range r.ApplyAgentTickerTemplates.AlreadyPresent {
			if x != nil {
				dto.AlreadyPresent = append(dto.AlreadyPresent, appliedTemplateDTO{x.Id, x.Name})
			}
		}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			// The floor is read only to label the receipt, and only when
			// something was created. The write already happened, so an
			// unreadable floor drops the label instead of failing.
			floor := 0
			if len(dto.Created) > 0 && dto.Created[0].Ticker.AppID != "" {
				if s, e := gen.TickerAppFloor(cmd.Context(), client, dto.Created[0].Ticker.AppID); e == nil && s.AppTickerSettings != nil {
					floor = s.AppTickerSettings.MinCadenceSeconds
				}
			}
			return writeApplied(w, wref, dto, floor)
		})
	}
	return c
}

func writeApplied(w io.Writer, wref string, dto applyTemplatesDTO, floor int) error {
	head := fmt.Sprintf("created %d tickers for worker %s:", len(dto.Created), wref)
	if len(dto.Created) == 0 {
		head = fmt.Sprintf("nothing to create for worker %s", wref)
	}
	lines := []string{head}
	for _, a := range dto.Created {
		ref := a.Ticker.ID
		if a.Ticker.URN != nil {
			ref = *a.Ticker.URN
		}
		cadence := "every " + cadenceCell(a.EffectiveCadenceSeconds)
		if floor > 0 && a.EffectiveCadenceSeconds == floor {
			cadence += " (the App's floor)"
		}
		lines = append(lines, fmt.Sprintf("  %s · %s · from template %s · %s", output.SafeCell(a.Ticker.Name), output.SafeCell(ref), output.SafeCell(a.Template.Name), cadence))
	}
	lines = append(lines, fmt.Sprintf("already present %d:", len(dto.AlreadyPresent)))
	for _, t := range dto.AlreadyPresent {
		lines = append(lines, fmt.Sprintf("  %s (%s)", output.SafeCell(t.Name), t.ID))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}
