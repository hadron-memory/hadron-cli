package edge

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name        string
		loc         string
		description string
		runnable    bool
		priority    int
		condition   string
		data        string
	)
	cmd := &cobra.Command{
		Use:   "update <edge-id>",
		Short: "Change an edge's name, description or automation settings",
		Long: `Change one edge, named by its edge id (the EDGE-ID column of
hadron edge list <node>, or node get --json). Only the fields you pass change;
everything else stays as it was. The two nodes it links cannot be changed:
to point a link elsewhere, edge rm it and edge create a new one.

To undo a setting:
  --runnable=false     the edge is no longer runnable
  --description ""     empties the description
  --condition null     removes the condition, so the edge always applies
  --data null          removes the stored data

--name changes only the displayed name; the edge keeps its loc (and so its
URN). --loc changes the loc. The automation settings (--runnable,
--priority, --condition, --data) mean the same as in edge create --help.
On success it prints the link and its id, like edge create.`,
		Example: `  hadron edge update 019f0c2a5e7b7d3e9a1c4b6d8e0f2a35 --name complements
  hadron edge update 019f0c2a5e7b7d3e9a1c4b6d8e0f2a35 --priority 10
  hadron edge update 019f0c2a5e7b7d3e9a1c4b6d8e0f2a35 --runnable=false --condition null`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("loc") && !changed("description") && !changed("runnable") &&
				!changed("priority") && !changed("condition") && !changed("data") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var nameArg, locArg, descArg *string
			if changed("name") {
				nameArg = &name
			}
			if changed("loc") {
				locArg = &loc
			}
			if changed("description") {
				descArg = &description
			}
			var runnableArg *bool
			if changed("runnable") {
				runnableArg = &runnable
			}
			var priorityArg *int
			if changed("priority") {
				priorityArg = &priority
			}
			var conditionArg, dataArg *json.RawMessage
			if changed("condition") {
				if conditionArg, err = parseJSONFlag("condition", condition); err != nil {
					return err
				}
			}
			if changed("data") {
				if dataArg, err = parseJSONFlag("data", data); err != nil {
					return err
				}
			}

			resp, err := gen.UpdateEdge(cmd.Context(), client, args[0], nameArg, locArg, descArg, runnableArg, priorityArg, conditionArg, dataArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateEdge == nil {
				return exitcode.Newf(exitcode.Error, "updateEdge returned no edge")
			}

			e := resp.UpdateEdge
			// #781: an endpoint is null when its memory is unreadable to the
			// caller — guard against nil rather than panicking.
			sid, sloc := "", ""
			if e.Source != nil {
				sid, sloc = e.Source.Id, e.Source.Loc
			}
			tid, tloc := "", ""
			if e.Target != nil {
				tid, tloc = e.Target.Id, e.Target.Loc
			}
			dto := edgeDTOFrom(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority, sid, sloc, tid, tloc)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ updated", output.SafeCell(dto.SourceLoc+" → "+dto.TargetLoc), output.SafeCell("("+cmdutil.EdgeDisplay(e.Name, e.Loc)+")"), output.SafeCell(dto.ID))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new relationship name")
	cmd.Flags().StringVar(&loc, "loc", "", "new edge loc")
	cmd.Flags().StringVar(&description, "description", "", "new description (\"\" empties it)")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "automation: runnable or not (--runnable=false turns it off)")
	cmd.Flags().IntVar(&priority, "priority", 0, "automation: new order among the edges leaving one node; lower goes first")
	cmd.Flags().StringVar(&condition, "condition", "", "automation: new JSONLogic condition (null removes it)")
	cmd.Flags().StringVar(&data, "data", "", "new JSON value to store on the edge (null removes it)")
	return cmd
}
