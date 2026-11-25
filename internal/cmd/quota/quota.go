// Package quota implements `hadron quota ...` — the per-scope daily quota
// counters and their enforced limits (hadron-server #510, #5074; CLI #1343,
// #3139). The server owns every rule: who may read or set under each ref form,
// the action vocabulary and the limit range. The CLI relays the server's four
// ref forms and adds no access and no decision of its own.
package quota

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdQuota builds the `quota` command group.
func NewCmdQuota(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "quota <command>",
		Aliases: []string{"quotas"},
		Short:   "Show and set daily usage quotas for an org, an App or a person",
		Long: `Each org, each App, and each person has a daily counter per metered action:
run.execute, llm.tokens, sms.send and email.send. A counter shows how much was
used today against its limit; it resets at UTC midnight and the limit stays.
When a counter reaches its limit, further use is refused until the reset.

quota list shows the counters; quota set changes a limit. Both take exactly one
of four forms: --org, --org with --app, --app alone, or --user alone. Who may
use each form differs, and the server decides; each command's help lists them.
For a scope you have not used, the limit shown is the platform default.`,
		Example: `  hadron quota list --org acme.com
  hadron quota list --user <user-id>
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

// scopeRefs is the one ref form a quota command names: exactly the server's
// four (org; org and app; app alone; user alone). A nil ref was not given and
// is omitted from the request, never sent as null.
type scopeRefs struct{ org, app, user *string }

// addScopeFlags registers the three ref flags every quota command shares.
func addScopeFlags(cmd *cobra.Command, org, app, user *string, appUsage string) {
	cmd.Flags().StringVar(org, "org", "", "organization id, URN (hrn:org:acme.com) or root domain (acme.com)")
	cmd.Flags().StringVar(app, "app", "", appUsage)
	cmd.Flags().StringVar(user, "user", "", "a person's user id; on its own, never with --org or --app")
}

// readScope checks the ref flags before any request: a flag that was given
// must not be blank, --user stands alone, and at least one ref is required.
// Nothing is filled in from the App context or the active org, and nothing is
// looked up: every ref is sent as typed and the server answers for it.
func readScope(cmd *cobra.Command, org, app, user string) (scopeRefs, error) {
	var s scopeRefs
	for _, r := range []struct {
		flag string
		val  string
		dst  **string
	}{{"org", org, &s.org}, {"app", app, &s.app}, {"user", user, &s.user}} {
		if !cmd.Flags().Changed(r.flag) {
			continue
		}
		if strings.TrimSpace(r.val) == "" {
			return s, exitcode.Newf(exitcode.Usage, "--%s is empty; pass a value, or leave --%s out", r.flag, r.flag)
		}
		v := r.val
		*r.dst = &v
	}
	switch {
	case s.user != nil && (s.org != nil || s.app != nil):
		return s, exitcode.Newf(exitcode.Usage, "--user names a person on its own; it cannot be combined with --org or --app")
	case s.user == nil && s.org == nil && s.app == nil:
		return s, exitcode.Newf(exitcode.Usage, "name what to use: --org, --org with --app, --app alone, or --user alone (the App context is not used here)")
	}
	return s, nil
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
