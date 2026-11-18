package task

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type renderTaskDTO struct {
	Rendered string `json:"rendered"`
}

func newCmdRender(f *cmdutil.Factory) *cobra.Command {
	var memory, argsText string
	cmd := &cobra.Command{
		Use:   "render <node-ref> | <loc> -m <memory>",
		Short: "Render a node's text through the renderRunNode Query (read-only, no run)",
		Long: `Render a node with the server's renderRunNode Query. Pass a node ID or
fully-qualified node URN, or a bare loc with -m/--memory. The server owns
admission, argument substitution, dependency access and assembly.

A qualified reference supplies its own memory; -m composes only a bare loc.

--args accepts one JSON object inline or from @file; omitted arguments remain
omitted, while an explicit {} is sent as an empty object. Invalid arguments
refuse before any request.

Human stdout is the returned string verbatim, without a label or added newline.
--json returns {"rendered":"…"}. An empty string is a successful result.

task render calls only the renderRunNode Query. It runs no model, creates no
run row and records no action-run, but the server still records the content
reads, including actually inlined dependencies. Compare task run: without --app
it calls the runTask mutation and also records an action-run; with --app it
executes. App context never enables execution in task render. There is no retry
or fallback to task run, and a render is not a promise about a
later execution's prompt.`,
		Example: `  hadron task render hrn:node:acme.com:kb:tasks:brief
  hadron task render tasks:brief -m acme.com:kb --args '{"topic":"release"}'
  hadron task render tasks:brief -m acme.com:kb --args @args.json --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			var argsObject *json.RawMessage
			if cmd.Flags().Changed("args") {
				raw := []byte(argsText)
				if path, ok := strings.CutPrefix(argsText, "@"); ok {
					var err error
					raw, err = os.ReadFile(path)
					if err != nil {
						return exitcode.Newf(exitcode.Usage, "--args: cannot read file: %v", err)
					}
				}
				var object map[string]json.RawMessage
				if err := json.Unmarshal(raw, &object); err != nil || object == nil {
					return exitcode.Newf(exitcode.Usage, "--args must be one JSON object, inline or @file")
				}
				value := json.RawMessage(raw)
				argsObject = &value
			}
			// Compose references without a lookup/read filter: renderRunNode itself
			// resolves and admits the caller's reference.
			refMemory := memory
			if cmdutil.IsQualifiedNodeRef(positional[0]) {
				refMemory = ""
			}
			ref, err := cmdutil.BatchNodeRef(refMemory, positional[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RenderRunNode(cmd.Context(), client, ref, argsObject)
			if err != nil {
				return api.MapError(err)
			}
			// The server declares renderRunNode String!, so an absent or null field
			// is a broken reply, not an empty render. Refuse it: no retry and no
			// fallback (a render records a read, cor:dmo:080:02), and nothing is
			// printed. A valid "" is a success (cli#2268).
			if resp == nil || resp.RenderRunNode == nil {
				return exitcode.Newf(exitcode.Error, "the server's reply had no renderRunNode (missing or null), so nothing was rendered")
			}
			dto := renderTaskDTO{Rendered: *resp.RenderRunNode}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := io.WriteString(w, dto.Rendered)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to compose a bare node loc against")
	cmd.Flags().StringVar(&argsText, "args", "", "one JSON object, inline or @file (omitted unless explicitly given)")
	return cmd
}
