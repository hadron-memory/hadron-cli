package agent

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

// modelGroupDTO is one Agent model group as the server stored it (#1485 S1,
// cor:agt:060:12). It carries no origin: an Agent's own definition is the
// management read; the App-side effective list is where origins live.
type modelGroupDTO struct {
	Name      string   `json:"name"`
	Patterns  []string `json:"patterns"`
	UpdatedAt string   `json:"updatedAt"`
}

// modelGroupDeletedDTO confirms one deleteAgentModelGroup.
type modelGroupDeletedDTO struct {
	Agent   string `json:"agent"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func modelGroupFrom(name string, patterns []string, updatedAt string) modelGroupDTO {
	p := make([]string, 0, len(patterns))
	p = append(p, patterns...)
	return modelGroupDTO{Name: name, Patterns: p, UpdatedAt: updatedAt}
}

// patternsText renders a pattern list for humans; an empty list is "(none)",
// which is a real group that matches no one, not an absent one.
func patternsText(patterns []string) string {
	if len(patterns) == 0 {
		return "(none)"
	}
	cells := make([]string, len(patterns))
	for i, p := range patterns {
		cells[i] = output.SafeCell(p)
	}
	return strings.Join(cells, ", ")
}

const modelGroupLong = `A model group is a named list of model patterns (lowercase globs: * any run,
? one character) matched against a worker session's self-reported model. It is
a matching input only, never an authorization input. The Agent declares its
groups; an App that has this Agent as its Team Agent may override one by name
or add its own (see hadron app model-group).

Names and patterns are sent exactly as given. The server lowercases them and
enforces the name grammar and its limits; the CLI decides nothing about them.
Only whoever manages the Agent may read or change its groups.`

func newCmdAgentModelGroup(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model-group <command>",
		Aliases: []string{"model-groups"},
		Short:   "Manage the model groups an Agent declares",
		Long:    modelGroupLong,
	}
	cmd.AddCommand(newCmdAgentModelGroupList(f))
	cmd.AddCommand(newCmdAgentModelGroupSet(f))
	cmd.AddCommand(newCmdAgentModelGroupDelete(f))
	return cmd
}

func agentRefArg(arg string) (string, error) {
	ref := strings.TrimSpace(arg)
	if ref == "" {
		return "", exitcode.Newf(exitcode.Usage, "the agent reference is empty")
	}
	return ref, nil
}

func newCmdAgentModelGroupList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list <agent>",
		Aliases: []string{"ls"},
		Short:   "List the model groups an Agent declares",
		Long: `List the Agent's own model groups by name, as stored: name, patterns and
updatedAt. There is no origin here; App overrides are not applied. To see the
groups in force in an App, use hadron app model-group list.

This is a management read. An Agent you cannot read is not found (exit 4); one
you can read but do not manage is refused (exit 8).

JSON is an array of {name, patterns, updatedAt} ([] when there are none);
patterns is [] for a group with no patterns.

` + modelGroupLong,
		Example: `  hadron agent model-group list hrn:agent:acme.com:iris
  hadron agent model-group list hrn:agent:acme.com:iris --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := agentRefArg(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentModelGroups(cmd.Context(), client, ref)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			rows := []modelGroupDTO{}
			for _, g := range resp.AgentModelGroups {
				if g == nil {
					return exitcode.Newf(exitcode.Error, "the server returned a null model group")
				}
				rows = append(rows, modelGroupFrom(g.Name, g.Patterns, g.UpdatedAt))
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "This Agent declares no model groups.")
					return err
				}
				t := output.NewTable(w, "NAME", "PATTERNS", "UPDATED")
				for _, g := range rows {
					t.Row(output.SafeCell(g.Name), patternsText(g.Patterns), output.SafeCell(g.UpdatedAt))
				}
				return t.Flush()
			})
		},
	}
}

func newCmdAgentModelGroupSet(f *cmdutil.Factory) *cobra.Command {
	var patterns []string
	cmd := &cobra.Command{
		Use:   "set <agent> <name> [--pattern <glob>...]",
		Short: "Create an Agent model group or replace its whole pattern list",
		Long: `Create the named group, or replace its whole pattern list (no merge). Repeat
--pattern once per glob; each value is sent as one list item, never split on
commas. With no --pattern the group is set to an empty list, which the server
accepts: a group that matches no one.

Installs that override this group keep their own list. One mutation is sent
and never retried. JSON is the stored group {name, patterns, updatedAt}, as
the server returns it (lowercased).

` + modelGroupLong,
		Example: `  hadron agent model-group set hrn:agent:acme.com:iris frontier --pattern 'claude-opus-*' --pattern 'gpt-6*'
  hadron agent model-group set hrn:agent:acme.com:iris parked`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := agentRefArg(args[0])
			if err != nil {
				return err
			}
			// Always a non-nil slice: genqlient marshals nil as null, and the
			// empty list is a real value here.
			list := append([]string{}, patterns...)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAgentModelGroup(cmd.Context(), client, ref, args[1], list)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			g := resp.SetAgentModelGroup
			if g == nil {
				return exitcode.Newf(exitcode.Error, "the server did not return the model group; list the Agent's groups before retrying")
			}
			dto := modelGroupFrom(g.Name, g.Patterns, g.UpdatedAt)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ set model group %s on agent %s\n  patterns: %s\n", output.SafeCell(dto.Name), output.SafeCell(ref), patternsText(dto.Patterns))
				return err
			})
		},
	}
	cmd.Flags().StringArrayVar(&patterns, "pattern", nil, "a model glob; repeat for each (omit all for an empty list)")
	return cmd
}

func newCmdAgentModelGroupDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <agent> <name> [--yes]",
		Aliases: []string{"rm"},
		Short:   "Delete one of an Agent's model groups",
		Long: `Delete the Agent's group of this name. Installs that override it keep their
own list. A name the Agent does not declare is refused by the server
(MODEL_GROUP_NOT_FOUND). Confirmation is required; scripts pass --yes.

One mutation is sent and never retried. JSON is {agent, name, deleted}.

` + modelGroupLong,
		Example: `  hadron agent model-group delete hrn:agent:acme.com:iris parked --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := agentRefArg(args[0])
			if err != nil {
				return err
			}
			name := args[1]
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("model group %q of agent %s", name, ref)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteAgentModelGroup(cmd.Context(), client, ref, name)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if !resp.DeleteAgentModelGroup {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the deletion; list the Agent's groups before retrying")
			}
			dto := modelGroupDeletedDTO{Agent: ref, Name: name, Deleted: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted model group %s from agent %s\n", output.SafeCell(name), output.SafeCell(ref))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	return cmd
}
