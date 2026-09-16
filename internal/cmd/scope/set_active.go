package scope

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// defaultScopeDTO is the stable --json shape of `hadron scope use` — ONE shape
// for every successful invocation, clear included (the lesson from `org use`,
// @codex #596).
type defaultScopeDTO struct {
	Scope string `json:"scope"`
}

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
				return output.Write(f.IOStreams, f.JSON, defaultScopeDTO{}, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "✓ Cleared the default search scope")
					return err
				})
			}

			// Trim before verifying AND before storing. resolveScopeID trims
			// internally, so an untrimmed value verified successfully and was
			// then persisted with its whitespace — a setting reported as
			// verified that no later search could resolve (@codex, #597).
			ref := strings.TrimSpace(args[0])

			// `app` and `global` are keywords resolved per-invocation against
			// the App / organization context, so there is nothing to verify
			// here and pinning them now would be wrong — the whole point is
			// that they follow whatever context the later search runs in.
			if !skipVerify && ref != scopeKeywordApp && ref != scopeKeywordGlobal {
				client, err := f.GraphQLClient()
				if err != nil {
					return err
				}
				// A NAME resolves through the server's own ladder. An ID does
				// NOT — resolveScopeID short-circuits on shape without a round
				// trip, so `scope use <typo-id>` used to store an unreadable
				// default that failed every later flagless search (@codex,
				// #597). Read it explicitly instead.
				id, err := resolveScopeID(cmd, f, client, ref, false)
				if err != nil {
					return err
				}
				resp, err := gen.GetScope(cmd.Context(), client, id)
				if err != nil {
					return api.MapError(err)
				}
				if resp == nil || resp.Scope == nil {
					return exitcode.Newf(exitcode.NotFound,
						"no scope %q is readable here — it would fail every search that used it (--no-verify stores it anyway)", ref)
				}
			}

			// Stored as TYPED (trimmed). A name is resolved per-invocation
			// because the App context can differ between directories; freezing
			// it to an id here would defeat that.
			if err := cfg.Set("scope", ref); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, defaultScopeDTO{Scope: ref}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Default search scope set to %s\n", ref)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&skipVerify, "no-verify", false, "store the value without checking it resolves (offline)")
	return cmd
}
