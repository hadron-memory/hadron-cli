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
			"%q is not a fully-qualified resource URN — prefix it with its kind "+
				"(hrn:memory:, hrn:node:, hrn:app:, or hrn:agent:), e.g. hrn:memory:%[1]s; "+
				"a bare, colon-free id is read as an AiServiceConfig id", r)
	}
	return r, nil
}
