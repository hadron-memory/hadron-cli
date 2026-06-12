package edge

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdAdd(f *cmdutil.Factory) *cobra.Command {
	var (
		from      string
		to        string
		label     string
		priority  int
		condition string
		data      string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create an edge between two nodes",
		Long: `Create a directed, labeled edge from one node to another. Both
endpoints are fully-qualified node URNs (<org>:<memory>:<loc>);
cross-memory edges are allowed.`,
		Example: `  hadron edge add --from acme.com:kb:findings:flaky-ci --to acme.com:kb:start-here --label routes-to
  hadron edge add --from a.com:m:x --to a.com:m:y --label triggers --condition '{"==":[{"var":"lang"},"go"]}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate local input before any network round-trip.
			conditionArg, err := parseJSONFlag("condition", condition)
			if err != nil {
				return err
			}
			dataArg, err := parseJSONFlag("data", data)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sourceID, err := resolveNodeURN(cmd, client, from)
			if err != nil {
				return err
			}
			targetID, err := resolveNodeURN(cmd, client, to)
			if err != nil {
				return err
			}
			var priorityArg *int
			if cmd.Flags().Changed("priority") {
				priorityArg = &priority
			}

			resp, err := gen.CreateEdge(cmd.Context(), client, sourceID, targetID, label, priorityArg, conditionArg, dataArg)
			if err != nil {
				return api.MapError(err)
			}

			e := resp.CreateEdge
			dto := edgeDTO{
				ID: e.Id, Label: e.Label, Priority: e.Priority,
				SourceID: e.Source.Id, SourceLoc: e.Source.Loc,
				TargetID: e.Target.Id, TargetLoc: e.Target.Loc,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ created", dto.SourceLoc+" → "+dto.TargetLoc, "("+dto.Label+")", dto.ID)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source node URN (required)")
	cmd.Flags().StringVar(&to, "to", "", "target node URN (required)")
	cmd.Flags().StringVar(&label, "label", "", "edge label (required)")
	cmd.Flags().IntVar(&priority, "priority", 0, "edge priority")
	cmd.Flags().StringVar(&condition, "condition", "", "JSONLogic gate condition (JSON)")
	cmd.Flags().StringVar(&data, "data", "", "arbitrary edge data (JSON)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("label")
	return cmd
}
