package team

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// workerTerminalDTO is the stable --json shape of Worker.terminal (hadron-server
// #2771, cli#1375): a host's latest terminal report for a worker. Stored and
// advisory — it decides nothing; judge its age from checkedAt. The key is
// present on `team roster` and `team worker get` and null when the worker never
// reported or the server masked it on the working-state gate.
type workerTerminalDTO struct {
	Host       terminalHostDTO `json:"host"`
	Status     string          `json:"status"`
	Prompt     string          `json:"prompt"`
	Reason     *string         `json:"reason"`
	LastTurnAt *string         `json:"lastTurnAt"`
	CheckedAt  string          `json:"checkedAt"`
	ReportedAt string          `json:"reportedAt"`
}

type terminalHostDTO struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func workerTerminalFromFields(t gen.WorkerTerminalFields) *workerTerminalDTO {
	dto := &workerTerminalDTO{
		Status:     string(t.Status),
		Prompt:     string(t.Prompt),
		LastTurnAt: t.LastTurnAt,
		CheckedAt:  t.CheckedAt,
		ReportedAt: t.ReportedAt,
	}
	if t.Host != nil {
		dto.Host = terminalHostDTO{ID: t.Host.Id, Label: t.Host.Label}
	}
	if t.Reason != nil {
		r := string(*t.Reason)
		dto.Reason = &r
	}
	return dto
}

// terminalUnknown stands in for a value outside the closed sets below: a newer
// server's enum value is never passed through as if it were known text.
const terminalUnknown = "?"

var (
	terminalStatuses = map[string]bool{"busy": true, "idle": true, "locked": true, "dark": true}
	terminalReasons  = map[string]bool{
		"bare_shell": true, "guardrail_triggered": true, "menu_or_question": true,
		"model_at_capacity": true, "no_agent": true, "no_terminal": true,
		"no_turn_for_due_ticker": true, "rate_limited": true,
		"shed_staged_not_relaunched": true, "token_budget_exhausted": true,
		"unrecognized": true,
	}
)

// terminalLabel is the TERMINAL cell: `idle (3m)`, `dark (no_turn_for_due_ticker,
// 12m)`, or "—" when null. The age is on EVERY reported row so a stale report
// from a stopped sweep is visible; it is computed from checkedAt (when the host
// classified the terminal, not when the server stored it), for display only.
// Status and reason come from closed sets; anything else renders "?".
func terminalLabel(t *workerTerminalDTO, now time.Time) string {
	if t == nil {
		return "—"
	}
	status := t.Status
	if !terminalStatuses[status] {
		status = terminalUnknown
	}
	age := terminalAge(t.CheckedAt, now)
	if t.Reason == nil {
		return status + " (" + age + ")"
	}
	reason := *t.Reason
	if !terminalReasons[reason] {
		reason = terminalUnknown
	}
	return status + " (" + reason + ", " + age + ")"
}

// terminalAge is a compact age: "<1m", "3m", "5h", "2d". An unparseable time is
// "?", never a guess; a time ahead of the local clock reads "<1m".
func terminalAge(checkedAt string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, checkedAt)
	if err != nil {
		return terminalUnknown
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// terminalDetail is the `terminal:` line of `worker get`: the label, then the
// host and the prompt shape. The host label is registration text, so it is
// neutralised like any other server-supplied cell; the prompt is a closed code.
func terminalDetail(t *workerTerminalDTO, now time.Time) string {
	if t == nil {
		return "—"
	}
	prompt := t.Prompt
	switch prompt {
	case "empty", "menu", "not_empty", "unknown":
	default:
		prompt = terminalUnknown
	}
	return fmt.Sprintf("%s — host %s, prompt %s", terminalLabel(t, now), output.SafeCell(t.Host.Label), prompt)
}

// readWorkerTerminal reads Worker.terminal in its own operation, so a server
// that predates #2771 (it rejects an unknown field before execution) still
// serves the ordinary lookup. Only that documented capability absence falls
// back to nil ("not reported"); an auth, transport or server error is returned,
// never turned into an old-server-looking success.
func readWorkerTerminal(ctx context.Context, client graphql.Client, id string) (*workerTerminalDTO, error) {
	resp, err := gen.GetWorkerTerminal(ctx, client, id)
	if err != nil {
		if api.IsGraphQLValidationFor(err, "terminal") {
			return nil, nil
		}
		return nil, api.MapError(err)
	}
	if resp.Worker == nil || resp.Worker.Terminal == nil {
		return nil, nil
	}
	return workerTerminalFromFields(resp.Worker.Terminal.WorkerTerminalFields), nil
}
