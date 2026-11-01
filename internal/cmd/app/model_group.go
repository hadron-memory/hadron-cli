package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// effectiveModelGroupDTO is one model group in force in an App (#1485 S1,
// cor:agt:060:12), with its origin as a CLI label.
type effectiveModelGroupDTO struct {
	Name     string   `json:"name"`
	Patterns []string `json:"patterns"`
	Origin   string   `json:"origin"`
}

// appModelGroupDTO is the App's own stored group, as setAppModelGroup returns it.
type appModelGroupDTO struct {
	Name      string   `json:"name"`
	Patterns  []string `json:"patterns"`
	UpdatedAt string   `json:"updatedAt"`
}

// appModelGroupClearedDTO confirms one deleteAppModelGroup.
type appModelGroupClearedDTO struct {
	App     string `json:"app"`
	Name    string `json:"name"`
	Cleared bool   `json:"cleared"`
}

// modelGroupOriginLabel is display formatting only (cor:api:240:01 allows it):
// the server's enum mapped to the labels the agentic-usage contract names. A
// value this CLI does not know yet is shown as the server sent it, not
// guessed into one of the three.
func modelGroupOriginLabel(o gen.ModelGroupOrigin) string {
	switch o {
	case gen.ModelGroupOriginAgent:
		return "agent"
	case gen.ModelGroupOriginAppOverride:
		return "app-override"
	case gen.ModelGroupOriginApp:
		return "app-only"
	default:
		return string(o)
	}
}

func copyPatterns(in []string) []string {
	return append(make([]string, 0, len(in)), in...)
}

func appPatternsText(patterns []string) string {
	if len(patterns) == 0 {
		return "(none)"
	}
	cells := make([]string, len(patterns))
	for i, p := range patterns {
		cells[i] = output.SafeCell(p)
	}
	return strings.Join(cells, ", ")
}

const appModelGroupLong = `A model group is a named list of model patterns (lowercase globs: * any run,
? one character) matched against a worker session's self-reported model. It is
a matching input only, never an authorization input. The App's Team Agent
declares groups (hadron agent model-group); the App may override one by name
or add its own. Groups resolve live: a change to the Agent's group shows here
at once unless the App overrides it.

Names and patterns are sent exactly as given. The server lowercases them,
enforces the name grammar and its limits, and decides who may read or write;
the CLI decides nothing about them and reads nothing first.`

func newCmdModelGroup(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model-group <command>",
		Aliases: []string{"model-groups"},
		Short:   "Manage the model groups in force in an App",
		Long:    appModelGroupLong,
	}
	cmd.AddCommand(newCmdModelGroupList(f))
	cmd.AddCommand(newCmdModelGroupSet(f))
	cmd.AddCommand(newCmdModelGroupClear(f))
	return cmd
}

func newCmdModelGroupList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list <app-ref>",
		Aliases: []string{"ls"},
		Short:   "List the model groups in force in an App, with their origin",
		Long: `List the App's effective model groups by name: the Team Agent's groups with
the App's overrides applied, plus the App's own. Each row has an origin:

  agent         declared by the Team Agent, not overridden here
  app-override  the App replaced the Agent's group of this name
  app-only      the App's own group; the Agent declares none of this name

Any participant of the App may read this. JSON is an array of
{name, patterns, origin} ([] when there are none); patterns is [] for a group
that matches no one.

` + appModelGroupLong,
		Example: `  hadron app model-group list hrn:app:acme.com:eng-team
  hadron app model-group list acme.com:eng-team --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.EffectiveModelGroups(cmd.Context(), client, ref)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			rows := []effectiveModelGroupDTO{}
			for _, g := range resp.EffectiveModelGroups {
				if g == nil {
					return exitcode.Newf(exitcode.Error, "the server returned a null model group")
				}
				rows = append(rows, effectiveModelGroupDTO{Name: g.Name, Patterns: copyPatterns(g.Patterns), Origin: modelGroupOriginLabel(g.Origin)})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "No model groups are in force in this App.")
					return err
				}
				t := output.NewTable(w, "NAME", "ORIGIN", "PATTERNS")
				for _, g := range rows {
					t.Row(output.SafeCell(g.Name), output.SafeCell(g.Origin), appPatternsText(g.Patterns))
				}
				return t.Flush()
			})
		},
	}
}

func newCmdModelGroupSet(f *cmdutil.Factory) *cobra.Command {
	var patterns []string
	cmd := &cobra.Command{
		Use:   "set <app-ref> <name> [--pattern <glob>...]",
		Short: "Override the Team Agent's group by name, or set an App-only group",
		Long: `Set the App's group of this name, replacing its whole pattern list (no
merge). When the Team Agent declares a group of this name, this overrides it in
this App; otherwise it is the App's own group. Repeat --pattern once per glob;
each value is sent as one list item, never split on commas. With no --pattern
the group is set to an empty list, which matches no one.

Whoever manages the App may set it. One mutation is sent and never retried.
JSON is the stored group {name, patterns, updatedAt}, as the server returns it
(lowercased).

` + appModelGroupLong,
		Example: `  hadron app model-group set acme.com:eng-team frontier --pattern 'claude-opus-*'
  hadron app model-group set acme.com:eng-team frontier`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			// Always a non-nil slice: genqlient marshals nil as null, and the
			// empty list is a real value here.
			list := copyPatterns(patterns)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAppModelGroup(cmd.Context(), client, ref, args[1], list)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			g := resp.SetAppModelGroup
			if g == nil {
				return exitcode.Newf(exitcode.Error, "the server did not return the model group; list the App's groups before retrying")
			}
			dto := appModelGroupDTO{Name: g.Name, Patterns: copyPatterns(g.Patterns), UpdatedAt: g.UpdatedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ set model group %s in app %s\n  patterns: %s\n", output.SafeCell(dto.Name), output.SafeCell(ref), appPatternsText(dto.Patterns))
				return err
			})
		},
	}
	cmd.Flags().StringArrayVar(&patterns, "pattern", nil, "a model glob; repeat for each (omit all for an empty list)")
	return cmd
}

func newCmdModelGroupClear(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear <app-ref> <name> [--yes]",
		Short: "Remove the App's override or App-only group of this name",
		Long: `Remove the App's own group of this name. Clearing an override makes the Team
Agent's group of that name apply again; clearing an App-only group removes it.
The Agent's group itself is never deleted from here: for a name only the Agent
declares, the server answers MODEL_GROUP_NOT_FOUND, shown as it comes (exit 4).
To change the Agent's group, use hadron agent model-group.

Whoever manages the App may clear. Confirmation is required; scripts pass
--yes. One mutation is sent and never retried. JSON is {app, name, cleared}.

` + appModelGroupLong,
		Example: `  hadron app model-group clear acme.com:eng-team frontier --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			name := args[1]
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("App model group %q of %s", name, ref)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteAppModelGroup(cmd.Context(), client, ref, name)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if !resp.DeleteAppModelGroup {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the clear; list the App's groups before retrying")
			}
			dto := appModelGroupClearedDTO{App: ref, Name: name, Cleared: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ cleared model group %s in app %s\n", output.SafeCell(name), output.SafeCell(ref))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	return cmd
}
