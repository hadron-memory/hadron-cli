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
		Short: "Update an edge's name, loc, description, runnable, priority, condition, or data",
		Long: `Update an edge by its ID (shown by hadron edge ls and in node get
--json output). Only the fields you pass change.`,
		Example: `  hadron edge update edg_123 --name complements
  hadron edge update edg_123 --priority 10`,
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
			dto := edgeDTOFrom(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority,
				e.Source.Id, e.Source.Loc, e.Target.Id, e.Target.Loc)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ updated", dto.SourceLoc+" → "+dto.TargetLoc, "("+cmdutil.EdgeDisplay(e.Name, e.Loc)+")", dto.ID)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new relationship name")
	cmd.Flags().StringVar(&loc, "loc", "", "new edge loc")
	cmd.Flags().StringVar(&description, "description", "", "new edge description")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "set the edge runnable flag")
	cmd.Flags().IntVar(&priority, "priority", 0, "new edge priority")
	cmd.Flags().StringVar(&condition, "condition", "", "new JSONLogic gate condition (JSON)")
	cmd.Flags().StringVar(&data, "data", "", "new edge data (JSON)")
	return cmd
}
