package task

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

// runTaskDTO is the stable --json output shape.
type runTaskDTO struct {
	Result string `json:"result"`
}

func newCmdRun(f *cmdutil.Factory) *cobra.Command {
	var (
		memory string
		args   []string
	)
	cmd := &cobra.Command{
		Use:   "run <task-urn> | <loc> -m <memory>",
		Short: "Run a task node",
		Long: `Run a task node (a node where isRunnable=true), passing any required
arguments. Task nodes can define the arguments they accept via Node.data.args,
enabling discovery and validation of inputs.

Pass the task as a fully-qualified URN (org::memory::loc) or as a bare loc
with -m/--memory to name a single memory instead.`,
		Example: `  hadron task run hadronmemory.com::experiments::email:send --arg to=user@example.com --arg subject="Hello"
  hadron task run send -m hadronmemory.com::experiments --arg to=user@example.com`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Resolve the task reference to a concrete node ID client-side, then
			// pass that as the single `nodeRef` arg (#542 — runTask unified on the
			// <object>Ref convention). ResolveNodeRef handles both a full URN and a
			// bare <loc> with -m <memory>.
			taskRef := cmdArgs[0]
			nodeID, err := cmdutil.ResolveNodeRef(cmd, client, memory, taskRef)
			if err != nil {
				return err
			}

			// Parse arguments from --arg flags into a JSON object.
			var taskArgs *json.RawMessage
			if len(args) > 0 {
				argsMap := make(map[string]interface{})
				for _, arg := range args {
					key, value, found := findArgKV(arg)
					if !found {
						return exitcode.Newf(exitcode.Usage, "--arg must be in the format key=value, got %q", arg)
					}
					argsMap[key] = value
				}
				raw, _ := json.Marshal(argsMap)
				msg := json.RawMessage(raw)
				taskArgs = &msg
			}

			// Run the task via the runTask mutation, passing the resolved node ID.
			resp, err := gen.RunTask(cmd.Context(), client, nodeID, taskArgs)
			if err != nil {
				return api.MapError(err)
			}

			dto := runTaskDTO{Result: resp.RunTask}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := io.WriteString(w, resp.RunTask+"\n")
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve bare <loc> against")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "task argument in key=value format (repeatable)")
	return cmd
}

// findArgKV parses a key=value argument string.
func findArgKV(arg string) (key, value string, found bool) {
	for i, ch := range arg {
		if ch == '=' {
			return arg[:i], arg[i+1:], true
		}
	}
	return "", "", false
}
