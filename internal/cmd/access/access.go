// Package access implements `hadron access` — inspecting who can do what.
// Its one subcommand today, `check <user> <resource>`, is the client side of
// hadron-server's authoritative effectiveAccess resolver (#323): it answers
// "what access does this user have to this resource?" without re-deriving the
// server's authorization rules.
package access

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdAccess builds the `access` command group.
func NewCmdAccess(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access <command>",
		Short: "Inspect access permissions",
		Long: `See what a user can do with a resource and why. The one command,
hadron access check, asks the server for the user's effective access (read,
write, manage, delete) and lists the grants that confer it.`,
		Example: `  hadron access check alice@acme.com hrn:mem:acme.com:kb`,
	}
	cmd.AddCommand(newCmdCheck(f))
	return cmd
}

// normalizeResourceRef shapes a resource reference into what
// effectiveAccess(resource:) expects: a fully-qualified hrn:<type>: URN for a
// memory/node/app/agent, or a bare id for an AiServiceConfig (the one URN-less
// kind). A scheme-prefixed ref passes through verbatim — the server dispatches
// on its type. An unprefixed ref containing ":" is an under-qualified shorthand
// (e.g. acme.com::kb): the server would misread it as a config id, so reject it
// with guidance instead.
func normalizeResourceRef(ref string) (string, error) {
	r := strings.TrimSpace(ref)
	if r == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty resource reference")
	}
	if strings.HasPrefix(r, "hrn:") || strings.HasPrefix(r, "urn:") {
		return r, nil
	}
	if strings.Contains(r, ":") {
		return "", exitcode.Newf(exitcode.Usage,
			"the resource reference is not a fully-qualified URN (the value given is not shown) — prefix it with its kind "+
				"(hrn:mem:, hrn:node:, hrn:app:, or hrn:agent:; the legacy hrn:memory: "+
				"is also accepted), e.g. hrn:mem:<root>:<slug>; "+
				"a bare, colon-free id is read as an AiServiceConfig id")
	}
	return r, nil
}
