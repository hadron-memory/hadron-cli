// Package webpoll implements `hadron web-poll ...`: the org admin view of the
// page-monitor watches (hadron-server #1562, cli#1920): list them and stop one.
//
// The platform makes every authorization decision (cor:web:030:00): the org
// ADMIN/OWNER role and the tool.web_poll policy are checked by the server, and
// this package adds no preflight of either. A refusal is rendered as returned,
// so a job id from another organization and an unknown one stay
// indistinguishable (both NOT_FOUND). These are the person-facing admin
// operations, not the capability-tool plane the watches themselves run on.
package webpoll

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdWebPoll builds the `web-poll` command group.
func NewCmdWebPoll(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "web-poll <command>",
		Aliases: []string{"web-polls"},
		Short:   "List and stop an organization's page-monitor watches",
		Long: `List and stop the page-monitor watches (web polls) of an organization.

A watch is started by a run and checks a page on a schedule. These commands are
for an organization ADMIN or OWNER; the server decides who may list or stop a
watch, and the CLI shows its answer as returned.`,
	}
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdCancel(f))
	return cmd
}

// webPollDTO is the stable --json shape for a watch. Timestamps the server did
// not set are null.
type webPollDTO struct {
	JobID               string  `json:"jobId"`
	AppID               string  `json:"appId"`
	SourceRunID         string  `json:"sourceRunId"`
	URL                 string  `json:"url"`
	Status              string  `json:"status"`
	ConsecutiveFailures int     `json:"consecutiveFailures"`
	TriggerCount        int     `json:"triggerCount"`
	CreatedAt           string  `json:"createdAt"`
	ExpiresAt           string  `json:"expiresAt"`
	LastCheckedAt       *string `json:"lastCheckedAt"`
	LastTriggeredAt     *string `json:"lastTriggeredAt"`
	NextRunAt           *string `json:"nextRunAt"`
}

func dtoFromWebPoll(p *gen.WebPollsWebPollsWebPoll) webPollDTO {
	return webPollDTO{
		JobID:               p.JobId,
		AppID:               p.AppId,
		SourceRunID:         p.SourceRunId,
		URL:                 p.Url,
		Status:              p.Status,
		ConsecutiveFailures: p.ConsecutiveFailures,
		TriggerCount:        p.TriggerCount,
		CreatedAt:           p.CreatedAt,
		ExpiresAt:           p.ExpiresAt,
		LastCheckedAt:       p.LastCheckedAt,
		LastTriggeredAt:     p.LastTriggeredAt,
		NextRunAt:           p.NextRunAt,
	}
}

// cancelDTO is the stable --json shape of a stop.
type cancelDTO struct {
	JobID            string `json:"jobId"`
	Cancelled        bool   `json:"cancelled"`
	AlreadyCancelled bool   `json:"alreadyCancelled"`
}
