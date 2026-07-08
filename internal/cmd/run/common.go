// Package run implements `hadron run ...` — the headless App-run audit and
// control surface (spec-040, cor:agt:010:02). Trigger a run now, list and
// inspect past runs, and cancel a live one. Complements the recurring/webhook
// triggers in `hadron schedule` / `hadron webhook`.
package run

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// appRunDTO is the stable --json shape for an AppRun. It carries every field the
// server exposes — the full "what ran, why, what did it cost" record. Built from
// the AppRunFields fragment, never marshaled from a genqlient struct.
type appRunDTO struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organizationId"`
	AppID          string           `json:"appId"`
	AgentID        *string          `json:"agentId"`
	Status         string           `json:"status"`
	TriggerKind    string           `json:"triggerKind"`
	TriggerID      *string          `json:"triggerId"`
	EntryNodeURN   string           `json:"entryNodeUrn"`
	CurNodeURN     *string          `json:"curNodeUrn"`
	UserID         *string          `json:"userId"`
	CreatedBy      *string          `json:"createdBy"`
	ParentRunID    *string          `json:"parentRunId"`
	Attempts       int              `json:"attempts"`
	BudgetActions  *int             `json:"budgetActions"`
	BudgetTokens   *int             `json:"budgetTokens"`
	TimeoutMs      *int             `json:"timeoutMs"`
	Policy         *json.RawMessage `json:"policy"`
	EventData      *json.RawMessage `json:"eventData"`
	Data           *json.RawMessage `json:"data"`
	Failure        *json.RawMessage `json:"failure"`
	CreatedAt      string           `json:"createdAt"`
	StartedAt      *string          `json:"startedAt"`
	FinishedAt     *string          `json:"finishedAt"`
}

func dtoFromFields(f gen.AppRunFields) appRunDTO {
	return appRunDTO{
		ID:             f.Id,
		OrganizationID: f.OrganizationId,
		AppID:          f.AppId,
		AgentID:        f.AgentId,
		Status:         string(f.Status),
		TriggerKind:    string(f.TriggerKind),
		TriggerID:      f.TriggerId,
		EntryNodeURN:   f.EntryNodeUrn,
		CurNodeURN:     f.CurNodeUrn,
		UserID:         f.UserId,
		CreatedBy:      f.CreatedBy,
		ParentRunID:    f.ParentRunId,
		Attempts:       f.Attempts,
		BudgetActions:  f.BudgetActions,
		BudgetTokens:   f.BudgetTokens,
		TimeoutMs:      f.TimeoutMs,
		Policy:         f.Policy,
		EventData:      f.EventData,
		Data:           f.Data,
		Failure:        f.Failure,
		CreatedAt:      f.CreatedAt,
		StartedAt:      f.StartedAt,
		FinishedAt:     f.FinishedAt,
	}
}

// terminalStatuses are the AppRun states from which a run never advances — the
// stop condition for `run trigger --wait`.
var terminalStatuses = map[string]bool{
	"COMPLETED": true,
	"FAILED":    true,
	"CANCELLED": true,
	"TIMED_OUT": true,
}

// statusValues is the full AppRunStatus enum, ordered for a legible usage error.
var statusValues = []string{"PENDING", "RUNNING", "COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT"}

func isValidStatus(s string) bool {
	for _, v := range statusValues {
		if v == s {
			return true
		}
	}
	return false
}

func isTerminal(status string) bool { return terminalStatuses[status] }

// statusOr returns the run's status, or a placeholder when nothing was polled
// yet (the wait timed out before the first read landed).
func statusOr(r appRunDTO) string {
	if r.Status == "" {
		return "unknown"
	}
	return r.Status
}

// writeRunDetail renders the human view of a single run — the full record,
// including the failure payload when present.
func writeRunDetail(w io.Writer, r appRunDTO) error {
	if _, err := fmt.Fprintf(w, "run %s  %s  (%s)\n", r.ID, r.Status, r.TriggerKind); err != nil {
		return err
	}
	fmt.Fprintf(w, "  app %s  org %s\n", r.AppID, r.OrganizationID)
	fmt.Fprintf(w, "  entry %s\n", r.EntryNodeURN)
	if r.CurNodeURN != nil && *r.CurNodeURN != "" {
		fmt.Fprintf(w, "  at %s\n", *r.CurNodeURN)
	}
	fmt.Fprintf(w, "  attempts %d\n", r.Attempts)
	if r.BudgetActions != nil || r.BudgetTokens != nil {
		fmt.Fprintf(w, "  budget %s actions / %s tokens\n", optInt(r.BudgetActions), optInt(r.BudgetTokens))
	}
	fmt.Fprintf(w, "  created %s", r.CreatedAt)
	if r.StartedAt != nil {
		fmt.Fprintf(w, "  started %s", *r.StartedAt)
	}
	if r.FinishedAt != nil {
		fmt.Fprintf(w, "  finished %s", *r.FinishedAt)
	}
	fmt.Fprintln(w)
	if r.Data != nil {
		fmt.Fprintf(w, "  data %s\n", string(*r.Data))
	}
	if r.Failure != nil {
		fmt.Fprintf(w, "  failure %s\n", string(*r.Failure))
	}
	return nil
}

func optInt(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v)
}
