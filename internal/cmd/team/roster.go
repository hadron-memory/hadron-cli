package team

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/approster"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newCmdRoster builds `hadron team roster` — the roster read the team command
// group should have had from the start (#383).
//
// Under cor:agt:020:01 the roster IS the AppAgent join, and until this command
// existed only raw GraphQL exposed it. `persona list` and `agent list` are
// narrowings of the caller's whole READABLE agent set, so they answer "every
// persona I can read" — and because `--app` is the persistent App-CONTEXT flag
// rather than a filter, passing it changed nothing, which let them name a
// persona installed in another org's App as if it were on this team.
func newCmdRoster(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "roster",
		Aliases: []string{"members"},
		Short:   "Who is on this team — the App's installed agents",
		Long: `List the agents installed in the team App: the roster, read from the
AppAgent join (cor:agt:020:01) rather than narrowed client-side.

This is the command that answers "who is on this team?". ` + "`persona list`" + `
answers a different question — every persona YOU can read, across every org
and App — and ` + "`--app`" + ` does not scope it (it is the App-context flag, not a
filter), so it must not be read as a roster.

Rows with no persona name are installed agents that are not personas — the
Team Agent itself, typically. The team App resolves from --app (or the
configured App context), falling back to the worktree binding's team memory.

The same read is available as ` + "`hadron app agent list <app>`" + ` for a
non-team App (#408); this is the team-flavoured name for it.`,
		Example: `  hadron team roster --app acme.com::eng-team
  hadron team roster --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			appRef, err := resolveTeamApp(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Shared with `hadron app agent list` (#408) — the same read under
			// two nouns must not become two --json shapes.
			r, err := approster.Fetch(ctx, client, appRef)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, r.Members, func(w io.Writer) error {
				return approster.Render(w, r)
			})
		},
	}
	return cmd
}
