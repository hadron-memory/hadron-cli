package team

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// Explicit values survive unchanged; omission never becomes a null/clear.
// The server owns reference resolution, family classification and permission.
type sessionStartInputs struct {
	agent, customer, family, language, parent, plan, previous, typ, description string
	pr                                                                          int
}

func (v *sessionStartInputs) flags(cmd *cobra.Command) {
	for _, f := range []struct {
		p          *string
		name, help string
	}{
		{&v.agent, "agent", "role-Agent reference (id or URN; usually derived from --as)"},
		{&v.customer, "customer", "customer id recorded on the new session"},
		{&v.family, "family", "model family token (e.g. codex); also refreshes a resumed session"},
		{&v.language, "language", "language recorded on the new session"},
		{&v.parent, "parent-session", "parent session id recorded on the new session"},
		{&v.plan, "plan", "stated intent recorded on the new session"},
		{&v.previous, "previous-session", "previous session id recorded on the new session"},
		{&v.typ, "type", "type recorded on the new session"},
		{&v.description, "description", "resume mode only: plan if redemption starts a new session; ignored when resuming an open one"},
	} {
		cmd.Flags().StringVar(f.p, f.name, "", f.help)
	}
	cmd.Flags().IntVar(&v.pr, "pr-number", 0, "PR number recorded on the new session (unset omits it)")
}
func (v *sessionStartInputs) validate(cmd *cobra.Command, resume bool) error {
	for _, name := range []string{"agent", "customer", "family", "language", "parent-session", "plan", "previous-session", "type", "description"} {
		if cmd.Flags().Changed(name) {
			s, _ := cmd.Flags().GetString(name)
			if strings.TrimSpace(s) == "" {
				return exitcode.Newf(exitcode.Usage, "--%s must not be empty", name)
			}
		}
	}
	if !resume && cmd.Flags().Changed("description") {
		return exitcode.Newf(exitcode.Usage, "--description applies to resume mode; use --plan with --as or --launch-grant")
	}
	return nil
}
func (v *sessionStartInputs) apply(cmd *cobra.Command, in *gen.SessionInput) {
	in.AgentRef = changedString1337(cmd, "agent", v.agent)
	in.CustomerId = changedString1337(cmd, "customer", v.customer)
	in.Family = changedString1337(cmd, "family", v.family)
	in.Language = changedString1337(cmd, "language", v.language)
	in.ParentSessionId = changedString1337(cmd, "parent-session", v.parent)
	in.Plan = changedString1337(cmd, "plan", v.plan)
	in.PrevSessionId = changedString1337(cmd, "previous-session", v.previous)
	in.Type = changedString1337(cmd, "type", v.typ)
	in.PrNumber = changedInt1337(cmd, "pr-number", v.pr)
}
func changedString1337(cmd *cobra.Command, flag, value string) *string {
	if cmd.Flags().Changed(flag) {
		return &value
	}
	return nil
}
func changedInt1337(cmd *cobra.Command, flag string, value int) *int {
	if cmd.Flags().Changed(flag) {
		return &value
	}
	return nil
}
