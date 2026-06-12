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
		label     string
		priority  int
		condition string
		data      string
	)
	cmd := &cobra.Command{
		Use:   "update <edge-id>",
		Short: "Update an edge's label, priority, condition, or data",
		Long: `Update an edge by its ID (shown by hadron edge ls and in node get
--json output). Only the fields you pass change.`,
		Example: `  hadron edge update edg_123 --label complements
  hadron edge update edg_123 --priority 10`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("label") && !changed("priority") && !changed("condition") && !changed("data") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var labelArg *string
			if changed("label") {
				labelArg = &label
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

			resp, err := gen.UpdateEdge(cmd.Context(), client, args[0], labelArg, priorityArg, conditionArg, dataArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateEdge == nil {
				return exitcode.Newf(exitcode.Error, "updateEdge returned no edge")
			}

			e := resp.UpdateEdge
			dto := edgeDTO{
				ID: e.Id, Label: e.Label, Priority: e.Priority,
				SourceID: e.Source.Id, SourceLoc: e.Source.Loc,
				TargetID: e.Target.Id, TargetLoc: e.Target.Loc,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ updated", dto.SourceLoc+" → "+dto.TargetLoc, "("+dto.Label+")", dto.ID)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "new edge label")
	cmd.Flags().IntVar(&priority, "priority", 0, "new edge priority")
	cmd.Flags().StringVar(&condition, "condition", "", "new JSONLogic gate condition (JSON)")
	cmd.Flags().StringVar(&data, "data", "", "new edge data (JSON)")
	return cmd
}
