package org

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// activeOrgDTO is the stable --json shape of `hadron org use`.
//
// ONE shape for every successful invocation, including the clear. An earlier
// version emitted {"org":""} on clear and {"org":…,"name":…} otherwise, so a
// consumer's parse depended on which branch ran (@codex, #596). Name is "" when
// unknown — cleared, or stored with --no-verify — never absent.
type activeOrgDTO struct {
	Org  string `json:"org"`
	Name string `json:"name"`
}

func newCmdSetActive(f *cmdutil.Factory) *cobra.Command {
	var skipVerify bool
	cmd := &cobra.Command{
		Use:     "set-active <orgRef>",
		Aliases: []string{"use"},
		Short:   "Set the active organization for future invocations",
		Long: `Set the active organization stored in ~/.config/hadron/config.toml.

The active organization is what ` + "`--scope global`" + ` resolves against: the server
reads it as "this member's organization view". Without one, a search under
` + "`--scope global`" + ` is refused when you belong to more than one organization.

An organization is named by its root (acme.com), its URN, or its id — the
server resolves all three. The ref is verified when you set it, so a typo is
caught here rather than on some later search; --no-verify skips that round trip
for offline use.

Pass an empty string ("") to clear it.`,
		Example: `  hadron org use acme.com
  hadron org set-active hrn:org:acme.com
  hadron org use ""  # clear the active organization`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if args[0] == "" {
				if err := cfg.Unset("org"); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, activeOrgDTO{}, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "✓ Cleared the active organization")
					return err
				})
			}

			// Store what the USER typed, not a rewritten form: the server
			// resolves root / URN / id alike, so canonicalizing here would be
			// a grammar this repo does not own.
			name := ""
			if !skipVerify {
				client, err := f.GraphQLClient()
				if err != nil {
					return err
				}
				resp, err := gen.GetOrganization(cmd.Context(), client, args[0])
				if err != nil {
					return api.MapError(err)
				}
				if resp == nil || resp.Organization == nil {
					// Same shape as every other read gate: a ref you may not
					// read and one that does not exist are not distinguished.
					return exitcode.Newf(exitcode.NotFound,
						"no organization %q is readable by you (--no-verify stores it anyway)", args[0])
				}
				name = resp.Organization.Name
			}

			if err := cfg.Set("org", args[0]); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, activeOrgDTO{Org: args[0], Name: name}, func(w io.Writer) error {
				if name != "" {
					_, err := fmt.Fprintf(w, "✓ Active organization set to %s (%s)\n", name, args[0])
					return err
				}
				_, err := fmt.Fprintf(w, "✓ Active organization set to %s (unverified)\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&skipVerify, "no-verify", false, "store the ref without checking it resolves (offline)")
	return cmd
}
