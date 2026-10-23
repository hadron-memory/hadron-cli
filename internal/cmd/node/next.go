package node

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

func newCmdNext(f *cmdutil.Factory) *cobra.Command {
	var memory, after string
	var raw bool
	cmd := &cobra.Command{
		Use:   "next <parent> [--after <current>] [-m <memory>] [--raw]",
		Short: "Read the next ordered child of a parent",
		Long: `Read the first child of a parent, or the next sibling after --after.
The server orders by seq ascending (unset seq last), then loc. This is a
stateless read, not a session or traversal cursor.

Refs may be node ids, fully-qualified node URNs (including legacy forms), or
bare locs with -m/--memory. They are normalized locally and sent directly to
the server; no parent-existence lookup is made.

End of chain, a missing or unreadable parent, or --after outside its direct
children all return the same answer: "end of sibling chain" (JSON null), exit 0.
Malformed refs and provider failures remain errors. This says nothing about
whether a concealed parent exists.

Content is rendered by default, as for node get. --raw reads the stored body;
JSON rendered records the distinction. Advisory revision/stamp/comment/signature
fields are null because this command does not fetch those separate snapshots.`,
		Example: "  hadron node next guides -m hrn:mem:acme.com:kb\n  hadron node next hrn:node:acme.com:kb:guides --after hrn:node:acme.com:kb:guides:intro --raw --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("memory") && strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory must not be empty")
			}
			parent, err := nextNodeRef(memory, args[0])
			if err != nil {
				return err
			}
			var current *string
			if cmd.Flags().Changed("after") {
				if strings.TrimSpace(after) == "" {
					return exitcode.Newf(exitcode.Usage, "--after must not be empty; omit it to read the first child")
				}
				ref, err := nextNodeRef(memory, after)
				if err != nil {
					return err
				}
				current = &ref
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.NextNode(cmd.Context(), client, parent, current, raw)
			if err != nil {
				return api.MapError(err)
			}
			if resp.NextNode == nil {
				return output.Write(f.IOStreams, f.JSON, nil, func(w io.Writer) error { _, err := fmt.Fprintln(w, "end of sibling chain"); return err })
			}
			dto := detailDTO(resp.NextNode)
			if dto.Tags == nil {
				dto.Tags = []string{}
			}
			dto.Rendered = !raw
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderNodeDetail(w, dto) })
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory for bare parent/current locs")
	cmd.Flags().StringVar(&after, "after", "", "current direct child; omit to read the first child")
	cmd.Flags().BoolVar(&raw, "raw", false, "read stored content instead of rendered content")
	return cmd
}

// Memory scopes a bare loc only; qualified refs already name their own memory.
// Keep this local to nextNode so the fix does not change other callers.
func nextNodeRef(memory, ref string) (string, error) {
	if cmdutil.IsQualifiedNodeRef(ref) {
		memory = ""
	}
	return cmdutil.BatchNodeRef(memory, ref)
}
