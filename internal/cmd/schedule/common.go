// Package schedule implements `hadron schedule ...` — recurring headless-run
// or one-shot triggers (spec-040, cor:agt:010, D-2026-07-04-E). A schedule fires an entry
// node under an App's identity on a cron expression; the runs it spawns are
// visible through `hadron run`.
package schedule

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// scheduleDTO is the stable --json shape for an AgentSchedule.
type scheduleDTO struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organizationId"`
	AppID          string           `json:"appId"`
	AgentID        *string          `json:"agentId"`
	Name           string           `json:"name"`
	Cron           string           `json:"cron"`
	Timezone       string           `json:"timezone"`
	Enabled        bool             `json:"enabled"`
	EntryNodeURN   string           `json:"entryNodeUrn"`
	AIConfigName   *string          `json:"aiConfigName"`
	UserID         *string          `json:"userId"`
	CreatedBy      *string          `json:"createdBy"`
	EventData      *json.RawMessage `json:"eventData"`
	Policy         *json.RawMessage `json:"policy"`
	BudgetTokens   *int             `json:"budgetTokens"`
	BudgetActions  *int             `json:"budgetActions"`
	LastRunAt      *string          `json:"lastRunAt"`
	NextRunAt      *string          `json:"nextRunAt"`
	CreatedAt      string           `json:"createdAt"`
}

func dtoFromFields(f gen.AgentScheduleFields) scheduleDTO {
	// cron went nullable when the server added one-shot (runAt) schedules (#510);
	// keep the established --json plain-string shape (empty for a one-shot).
	cron := ""
	if f.Cron != nil {
		cron = *f.Cron
	}
	return scheduleDTO{
		ID:             f.Id,
		OrganizationID: f.OrganizationId,
		AppID:          f.AppId,
		AgentID:        f.AgentId,
		Name:           f.Name,
		Cron:           cron,
		Timezone:       f.Timezone,
		Enabled:        f.Enabled,
		EntryNodeURN:   f.EntryNodeUrn,
		AIConfigName:   f.AiConfigName,
		UserID:         f.UserId,
		CreatedBy:      f.CreatedBy,
		EventData:      f.EventData,
		Policy:         f.Policy,
		BudgetTokens:   f.BudgetTokens,
		BudgetActions:  f.BudgetActions,
		LastRunAt:      f.LastRunAt,
		NextRunAt:      f.NextRunAt,
		CreatedAt:      f.CreatedAt,
	}
}

// writeScheduleLine renders the human summary for create/update.
func writeScheduleLine(w io.Writer, verb string, s scheduleDTO) error {
	state := "enabled"
	if !s.Enabled {
		state = "disabled"
	}
	if _, err := fmt.Fprintf(w, "%s %s  %s (%s)  %s\n", verb, output.SafeCell(s.Name), cronCell(s.Cron), output.SafeCell(s.Timezone), output.SafeCell(state)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "  id %s  app %s  entry %s\n", output.SafeCell(s.ID), output.SafeCell(s.AppID), output.SafeCell(s.EntryNodeURN))
	return err
}

// cronCell keeps a missing recurrence visibly empty without inventing a schedule.
func cronCell(cron string) string {
	if cron == "" {
		return "-"
	}
	return output.SafeCell(cron)
}
