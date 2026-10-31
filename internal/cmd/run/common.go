// Package run implements `hadron run ...` — the headless App-run audit and
// control surface (spec-040, cor:agt:010:02). Trigger a run now, list and
// inspect past runs, and cancel a live one. Complements the recurring/webhook
// triggers in `hadron schedule` / `hadron webhook`.
package run

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/output"
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

	// Additive (#1180): null stays null (unknown), never 0.
	FailureReason        *failureReasonDTO `json:"failureReason"`
	CostMicroCents       *string           `json:"costMicroCents"`
	BudgetActionsInitial *int              `json:"budgetActionsInitial"`
	BudgetTokensInitial  *int              `json:"budgetTokensInitial"`
}

// failureReasonDTO is the server's plain-words failure cause, verbatim.
type failureReasonDTO struct {
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
	WhoCanFix string `json:"whoCanFix"`
	NextStep  string `json:"nextStep"`
}

func dtoFromFields(f gen.AppRunFields) appRunDTO {
	var reason *failureReasonDTO
	if fr := f.FailureReason; fr != nil {
		reason = &failureReasonDTO{Kind: fr.Kind, Summary: fr.Summary, WhoCanFix: fr.WhoCanFix, NextStep: fr.NextStep}
	}
	return appRunDTO{
		FailureReason:        reason,
		CostMicroCents:       f.CostMicroCents,
		BudgetActionsInitial: f.BudgetActionsInitial,
		BudgetTokensInitial:  f.BudgetTokensInitial,
		ID:                   f.Id,
		OrganizationID:       f.OrganizationId,
		AppID:                f.AppId,
		AgentID:              f.AgentId,
		Status:               string(f.Status),
		TriggerKind:          string(f.TriggerKind),
		TriggerID:            f.TriggerId,
		EntryNodeURN:         f.EntryNodeUrn,
		CurNodeURN:           f.CurNodeUrn,
		UserID:               f.UserId,
		CreatedBy:            f.CreatedBy,
		ParentRunID:          f.ParentRunId,
		Attempts:             f.Attempts,
		BudgetActions:        f.BudgetActions,
		BudgetTokens:         f.BudgetTokens,
		TimeoutMs:            f.TimeoutMs,
		Policy:               f.Policy,
		EventData:            f.EventData,
		Data:                 f.Data,
		Failure:              f.Failure,
		CreatedAt:            f.CreatedAt,
		StartedAt:            f.StartedAt,
		FinishedAt:           f.FinishedAt,
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
	if _, err := fmt.Fprintf(w, "run %s  %s  (%s)\n", output.SafeCell(r.ID), output.SafeCell(r.Status), output.SafeCell(r.TriggerKind)); err != nil {
		return err
	}
	if fr := r.FailureReason; fr != nil {
		fmt.Fprintf(w, "  reason %s  %s\n", output.SafeCell(fr.Kind), output.SafeCell(fr.Summary))
		fmt.Fprintf(w, "  who can fix %s\n", output.SafeCell(fr.WhoCanFix))
		fmt.Fprintf(w, "  next step %s\n", output.SafeCell(fr.NextStep))
	}
	fmt.Fprintf(w, "  app %s  org %s\n", output.SafeCell(r.AppID), output.SafeCell(r.OrganizationID))
	fmt.Fprintf(w, "  entry %s\n", output.SafeCell(r.EntryNodeURN))
	if r.CurNodeURN != nil && *r.CurNodeURN != "" {
		fmt.Fprintf(w, "  at %s\n", output.SafeCell(*r.CurNodeURN))
	}
	fmt.Fprintf(w, "  attempts %d  cost %s\n", r.Attempts, formatCost(r.CostMicroCents))
	if r.BudgetActions != nil || r.BudgetTokens != nil {
		fmt.Fprintf(w, "  budget %s actions / %s tokens\n", optInt(r.BudgetActions), optInt(r.BudgetTokens))
	}
	if r.BudgetActionsInitial != nil || r.BudgetTokensInitial != nil {
		fmt.Fprintf(w, "  initial budget %s actions / %s tokens\n", optUnknown(r.BudgetActionsInitial), optUnknown(r.BudgetTokensInitial))
	}
	fmt.Fprintf(w, "  created %s", output.SafeCell(r.CreatedAt))
	if r.StartedAt != nil {
		fmt.Fprintf(w, "  started %s", output.SafeCell(*r.StartedAt))
	}
	if r.FinishedAt != nil {
		fmt.Fprintf(w, "  finished %s", output.SafeCell(*r.FinishedAt))
	}
	fmt.Fprintln(w)
	if r.Data != nil {
		fmt.Fprintf(w, "  data %s\n", output.SafeCell(string(*r.Data)))
	}
	if r.Failure != nil {
		fmt.Fprintf(w, "  failure %s\n", output.SafeCell(string(*r.Failure)))
	}
	return nil
}

// optUnknown is optInt for the fields whose null means UNKNOWN (initial
// budgets, like cost): the same word everywhere.
func optUnknown(v *int) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *v)
}

func optInt(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v)
}

// formatCost renders a micro-cent count (1e-8 USD, a decimal string) as exact
// dollars, e.g. "1050000" -> "$0.0105 (1050000 micro-cents)". Digit-string
// arithmetic, never a float, so any magnitude stays exact. nil is UNKNOWN and
// says so — it is never shown as zero.
func formatCost(mc *string) string {
	if mc == nil {
		return "unknown"
	}
	s := *mc
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return output.SafeCell(s)
	}
	digits := strings.TrimLeft(s, "0")
	for len(digits) < 9 {
		digits = "0" + digits
	}
	whole, frac := digits[:len(digits)-8], strings.TrimRight(digits[len(digits)-8:], "0")
	for len(frac) < 2 {
		frac += "0"
	}
	return fmt.Sprintf("$%s.%s (%s micro-cents)", whole, frac, s)
}

// costCell and reasonCell are the compact `run list` columns.
func costCell(r appRunDTO) string {
	if r.CostMicroCents == nil {
		return "unknown"
	}
	c := formatCost(r.CostMicroCents)
	if i := strings.Index(c, " ("); i > 0 {
		c = c[:i]
	}
	return c
}

func reasonCell(r appRunDTO) string {
	if r.FailureReason == nil {
		// A failed run the server gave no reason for is unknown, not blank.
		if r.Status == "FAILED" {
			return "unknown"
		}
		return "—"
	}
	return output.SafeCell(r.FailureReason.Kind)
}
