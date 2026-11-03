// Package quota implements `hadron quota ...` — the per-scope daily quota
// counters and their enforced limits (hadron-server #510; CLI #1343). Org
// ADMIN only; the server owns every rule, including the action vocabulary and
// the limit range.
package quota

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdQuota builds the `quota` command group.
func NewCmdQuota(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "quota <command>",
		Aliases: []string{"quotas"},
		Short:   "Show and set an org's daily usage quotas",
		Long: `Each org, and each App in it, has a daily counter per metered action:
run.execute, llm.tokens, sms.send and email.send. A counter shows how much was
used today against its limit; it resets at UTC midnight and the limit stays.
When a counter reaches its limit, further use is refused until the reset.

quota list shows the counters; quota set changes a limit. Both need ADMIN or
OWNER in the org (else exit 8).`,
		Example: `  hadron quota list --org acme.com
  hadron quota set --org acme.com --action run.execute --limit 500`,
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdSet(f))
	return cmd
}

// counterDTO is the stable --json shape for one QuotaCounter.
type counterDTO struct {
	ID        string `json:"id"`
	ScopeKind string `json:"scopeKind"`
	ScopeID   string `json:"scopeId"`
	Action    string `json:"action"`
	Window    string `json:"window"`
	Used      int    `json:"used"`
	Limit     int    `json:"limit"`
	ResetAt   string `json:"resetAt"`
}

func dtoFromFields(q gen.QuotaCounterFields) counterDTO {
	return counterDTO{
		ID:        q.Id,
		ScopeKind: q.ScopeKind,
		ScopeID:   q.ScopeId,
		Action:    q.Action,
		Window:    q.Window,
		Used:      q.Used,
		Limit:     q.Limit,
		ResetAt:   q.ResetAt,
	}
}

func writeTable(w io.Writer, rows []counterDTO) error {
	t := output.NewTable(w, "SCOPE", "ACTION", "USED", "LIMIT", "WINDOW", "RESETS")
	for _, r := range rows {
		t.Row(output.SafeCell(r.ScopeKind+":"+r.ScopeID), output.SafeCell(r.Action),
			fmt.Sprint(r.Used), fmt.Sprint(r.Limit), output.SafeCell(r.Window), output.SafeCell(r.ResetAt))
	}
	return t.Flush()
}
