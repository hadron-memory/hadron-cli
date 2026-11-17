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
		Use:     "create",
		Aliases: []string{"add"},
		Short:   "Create an edge between two nodes",
		Long: `Link one node to another: an edge from --from to --to, named --name
(the relationship, for example routes-to). Give both nodes as full node URNs
(hrn:node:<root>:<slug>:<loc>), or pass -m/--memory and give them as bare locs
in that memory. To link nodes in two different memories, use full URNs and
omit -m. You need write access to the --from node's memory.

On success it prints the link and the new edge's id, which edge update and
edge rm take:

  ✓ created  findings:flaky-ci → start-here  (routes-to)  <edge-id>

The edge's loc (its address inside the memory) is <from-loc>:<name>:<to-loc>
unless you set --loc, and it must be unique in the memory: creating the same
link twice fails and creates nothing. A node that does not exist, or that
you cannot read, is refused before anything is written.

Settings for automation (optional; an ordinary link needs none of them):
  --runnable    marks the edge as one that flows and task runs may follow
  --priority    orders the edges that leave one node; lower goes first (default 0)
  --condition   a JSONLogic expression; the edge applies only when it is true
                (no condition: always). It may read memory.*, chat.*, agent.*,
                message.data.*, now() and today().
  --data        any JSON value stored on the edge for your own use`,
		Example: `  hadron edge create -m hrn:mem:acme.com:kb --from findings:flaky-ci --to start-here --name routes-to
  hadron edge create --from hrn:node:acme.com:kb:findings:flaky-ci --to hrn:node:acme.com:ops:runbook --name documents
  hadron edge create -m hrn:mem:acme.com:kb --from intake --to triage --name next --runnable --priority 1 --condition '{"==":[{"var":"message.data.kind"},"bug"]}'`,
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
			// #781: an endpoint is null when its memory is unreadable to the
			// caller; a just-created edge is normally fully readable, but guard
			// against nil rather than panicking.
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
				t.Row("✓ created", output.SafeCell(dto.SourceLoc+" → "+dto.TargetLoc), output.SafeCell("("+cmdutil.EdgeDisplay(e.Name, e.Loc)+")"), output.SafeCell(dto.ID))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve bare --from/--to locs against")
	cmd.Flags().StringVar(&from, "from", "", "the node the link starts from: a node URN, or a bare loc with -m (required)")
	cmd.Flags().StringVar(&to, "to", "", "the node the link points to: a node URN, or a bare loc with -m (required)")
	cmd.Flags().StringVar(&name, "name", "", "relationship name, for example routes-to (required)")
	cmd.Flags().StringVar(&loc, "loc", "", "the edge's loc in the memory (default <from-loc>:<name>:<to-loc>)")
	cmd.Flags().StringVar(&description, "description", "", "a description of the link")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "automation: flows and task runs may follow this edge")
	cmd.Flags().IntVar(&priority, "priority", 0, "automation: order among the edges leaving one node; lower goes first")
	cmd.Flags().StringVar(&condition, "condition", "", "automation: JSONLogic expression; the edge applies only when it is true")
	cmd.Flags().StringVar(&data, "data", "", "any JSON value to store on the edge")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
