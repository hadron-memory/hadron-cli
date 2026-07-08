package task

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// runTaskDTO is the stable --json output shape. mode discriminates the two
// server behaviors: "render" returns the compiled prompt in result; "execute"
// mints an app run and returns its id in both result and runId.
type runTaskDTO struct {
	Mode   string `json:"mode"`
	Result string `json:"result"`
	RunID  string `json:"runId,omitempty"`
}

func newCmdRun(f *cmdutil.Factory) *cobra.Command {
	var (
		memory, app string
		args        []string
		asSelf      bool
	)
	cmd := &cobra.Command{
		Use:   "run <task-urn> | <loc> -m <memory>",
		Short: "Run a task node",
		Long: `Run a task node (a node where isRunnable=true), passing any required
arguments. Task nodes can define the arguments they accept via Node.data.args,
enabling discovery and validation of inputs.

Pass the task as a fully-qualified URN (org::memory::loc) or as a bare loc
with -m/--memory to name a single memory instead.

By default this renders and prints the compiled prompt. With --app <ref> it
instead EXECUTES the task server-side — minting a headless run under that App
(a real LLM run, cor:agt:010:02) — and prints the run id; follow it with
'hadron run get <id>'. --as-self runs on behalf of you (reaches your personal
memories; authenticated user only).`,
		Example: `  hadron task run hadronmemory.com::experiments::email:send --arg to=user@example.com --arg subject="Hello"
  hadron task run send -m hadronmemory.com::experiments --arg to=user@example.com
  hadron task run acme.com::ops::tasks:brief --app acme.com:ops --arg topic=security --as-self`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			// runAsSelf only affects the identity of an executed run, so it's
			// meaningless without --app — reject it rather than silently ignore.
			if asSelf && app == "" {
				return exitcode.Newf(exitcode.Usage, "--as-self requires --app (it only affects an executed run's identity)")
			}
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

			// --app switches from render to execute mode: resolve the App ref and
			// pass appRef, so runTask mints a headless run and returns its id.
			var appRef, asSelfPtr = (*string)(nil), (*bool)(nil)
			if app != "" {
				resolved, err := cmdutil.ResolveAppRef(f, app)
				if err != nil {
					return err
				}
				appRef = &resolved
			}
			if asSelf {
				asSelfPtr = &asSelf
			}

			// Run the task via the runTask mutation, passing the resolved node ID.
			resp, err := gen.RunTask(cmd.Context(), client, nodeID, taskArgs, appRef, asSelfPtr)
			if err != nil {
				return api.MapError(err)
			}

			// In execute mode the returned string is the run id, not a prompt.
			dto := runTaskDTO{Mode: "render", Result: resp.RunTask}
			if appRef != nil {
				dto.Mode, dto.RunID = "execute", resp.RunTask
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Mode == "execute" {
					_, err := fmt.Fprintf(w, "✓ Run started: %s\n  follow it with: hadron run get %s\n", dto.RunID, dto.RunID)
					return err
				}
				_, err := io.WriteString(w, dto.Result+"\n")
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve bare <loc> against")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "task argument in key=value format (repeatable)")
	cmd.Flags().StringVar(&app, "app", "", "execute server-side under this App (ID or URN) and print the run id, instead of rendering the prompt")
	cmd.Flags().BoolVar(&asSelf, "as-self", false, "with --app: run on behalf of you (reaches your personal memories; authenticated user only)")
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
