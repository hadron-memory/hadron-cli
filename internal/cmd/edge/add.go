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

func newCmdAdd(f *cmdutil.Factory) *cobra.Command {
	var (
		memory      string
		from        string
		to          string
		name        string
		loc         string
		description string
		runnable    bool
		priority    int
		condition   string
		data        string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create an edge between two nodes",
		Long: `Create a directed, labeled edge from one node to another. Both
endpoints are fully-qualified node URNs (<org>:<memory>:<loc>); pass
-m/--memory to give --from/--to as bare locs in that one memory instead.
Cross-memory edges are allowed — use full URNs (omit -m) for those.`,
		Example: `  hadron edge add --from acme.com:kb:findings:flaky-ci --to acme.com:kb:start-here --label routes-to
  hadron edge add -m acme.com:kb --from findings:flaky-ci --to start-here --name routes-to`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate local input before any network round-trip.
			var conditionArg, dataArg *json.RawMessage
			var err error
			if cmd.Flags().Changed("condition") {
				if conditionArg, err = parseJSONFlag("condition", condition); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("data") {
				if dataArg, err = parseJSONFlag("data", data); err != nil {
					return err
				}
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sourceID, err := cmdutil.ResolveNodeRef(cmd, client, memory, from)
			if err != nil {
				return err
			}
			targetID, err := cmdutil.ResolveNodeRef(cmd, client, memory, to)
			if err != nil {
				return err
			}
			var priorityArg *int
			if cmd.Flags().Changed("priority") {
				priorityArg = &priority
			}
			var locArg, descArg *string
			if cmd.Flags().Changed("loc") {
				locArg = &loc
			}
			if cmd.Flags().Changed("description") {
				descArg = &description
			}
			var runnableArg *bool
			if cmd.Flags().Changed("runnable") {
				runnableArg = &runnable
			}

			resp, err := gen.CreateEdge(cmd.Context(), client, sourceID, targetID, name, locArg, descArg, runnableArg, priorityArg, conditionArg, dataArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateEdge == nil {
				return exitcode.Newf(exitcode.Error, "createEdge returned no edge")
			}

			e := resp.CreateEdge
			dto := edgeDTOFrom(e.Id, e.Name, e.Loc, e.IsRunnable, e.Priority,
				e.Source.Id, e.Source.Loc, e.Target.Id, e.Target.Loc)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ created", dto.SourceLoc+" → "+dto.TargetLoc, "("+cmdutil.EdgeDisplay(e.Name, e.Loc)+")", dto.ID)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org:memory) to resolve bare --from/--to locs against")
	cmd.Flags().StringVar(&from, "from", "", "source node URN, or bare loc with -m (required)")
	cmd.Flags().StringVar(&to, "to", "", "target node URN, or bare loc with -m (required)")
	cmd.Flags().StringVar(&name, "name", "", "relationship name (required)")
	cmd.Flags().StringVar(&loc, "loc", "", "explicit edge loc (default: derived <sourceLoc>:<name>:<targetLoc>)")
	cmd.Flags().StringVar(&description, "description", "", "edge description")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "mark the edge runnable")
	cmd.Flags().IntVar(&priority, "priority", 0, "edge priority")
	cmd.Flags().StringVar(&condition, "condition", "", "JSONLogic gate condition (JSON)")
	cmd.Flags().StringVar(&data, "data", "", "arbitrary edge data (JSON)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
