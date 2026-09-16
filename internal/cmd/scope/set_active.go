package scope

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdSetActive(f *cmdutil.Factory) *cobra.Command {
	var skipVerify bool
	cmd := &cobra.Command{
		Use:     "set-active <name|id|app|global>",
		Aliases: []string{"use"},
		Short:   "Set the default search scope for future invocations",
		Long: `Set the default scope stored in ~/.config/hadron/config.toml.

` + "`hadron search`" + ` applies it whenever --scope is not given, and SAYS SO on every
result — a narrowing you did not ask for and cannot see is indistinguishable
from missing data. Pass --scope on an invocation to override it.

Takes the same values --scope does: a scope name (resolved in an App's
context), a scope id, ` + "`app`" + `, or ` + "`global`" + `. The value is verified when you set
it unless --no-verify is passed.

Pass an empty string ("") to clear it.`,
		Example: `  hadron scope use research
  hadron scope set-active global
  hadron scope use ""  # clear the default`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if args[0] == "" {
				if err := cfg.Unset("scope"); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, map[string]string{"scope": ""}, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "✓ Cleared the default search scope")
					return err
				})
			}

			// `app` and `global` are keywords resolved per-invocation against
			// the App / organization context, so there is nothing to verify
			// here and pinning them now would be wrong — the whole point is
			// that they follow whatever context the later search runs in.
			if !skipVerify && args[0] != scopeKeywordApp && args[0] != scopeKeywordGlobal {
				client, err := f.GraphQLClient()
				if err != nil {
					return err
				}
				// Resolves a name through the server's own ladder, exactly as
				// a search would — so a value that will not resolve later is
				// refused now rather than silently narrowing every search.
				if _, err := resolveScopeID(cmd, f, client, args[0], false); err != nil {
					return err
				}
			}

			// Stored as TYPED. A name is resolved per-invocation because the
			// App context can differ between directories; freezing it to an id
			// here would defeat that.
			if err := cfg.Set("scope", args[0]); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{"scope": args[0]}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Default search scope set to %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&skipVerify, "no-verify", false, "store the value without checking it resolves (offline)")
	return cmd
}
