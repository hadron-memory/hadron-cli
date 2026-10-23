package node

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// restoreReceiptDTO is the --json shape of `node restore`: the plain restored
// node, with identity and timestamps only. There is deliberately no "changed"
// or "idempotent" flag — the wire does not carry one, and inventing it would
// be a claim nothing backs.
type restoreReceiptDTO struct {
	ID        string  `json:"id"`
	URN       string  `json:"urn"`
	MemoryID  string  `json:"memoryId"`
	Loc       string  `json:"loc"`
	Name      string  `json:"name"`
	NodeType  string  `json:"nodeType"`
	UpdatedAt string  `json:"updatedAt"`
	DeletedAt *string `json:"deletedAt"`
}

func newCmdRestore(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "restore <node-urn> | <node-id> | <loc> -m <memory>",
		Short: "Restore one deleted node",
		Long: `Restore ONE deleted node (writers only). It brings back the target node and the
comment threads its own deletion removed — nothing else: not its descendants,
not comments that were deleted independently, not its revision history.

The reference goes to the server as given; nothing is resolved first, since an
ordinary resolve omits deleted nodes. List candidates with 'node recovery ls'.

It refuses, with the server's own message, a governed node, a comment, a
compiled boot prompt, a protected address, and a node whose parent is still
deleted (restore the named ancestor first). Restoring a node that is already
live is accepted; the receipt does not say whether anything changed.

There is no prompt (a restore is reversible by deleting) and no retry: it sends
ONE request. If the answer is lost, verify with 'hadron node recovery get' or
'hadron node get' before running it again.`,
		Example: `  hadron node restore hrn:node:acme.com:kb:findings:flaky-ci
  hadron node restore findings:flaky-ci -m hrn:mem:acme.com:kb --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := cmdutil.BatchNodeRef(memory, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RestoreNode(cmd.Context(), client, ref)
			if err != nil {
				mapped := api.MapError(err)
				if exitcode.FromError(mapped) == exitcode.Unavailable {
					return exitcode.Newf(exitcode.Unavailable,
						"%s\nThe restore may have been applied: verify with `hadron node recovery get` or `hadron node get` before running it again", mapped.Error())
				}
				return mapped
			}
			n := resp.RestoreNode
			if n == nil {
				return exitcode.Newf(exitcode.Error, "restoreNode returned no node")
			}
			dto := restoreReceiptDTO{ID: n.Id, URN: n.Urn, MemoryID: n.MemoryId, Loc: n.Loc, Name: n.Name,
				NodeType: n.NodeType, UpdatedAt: n.UpdatedAt, DeletedAt: n.DeletedAt}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Node %s is live.\n", dto.URN)
				return err
			}); err != nil {
				return exitcode.Newf(exitcode.Error,
					"restore may have succeeded, but the receipt could not be written (%v); do not blindly retry — verify with `hadron node recovery get`", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "Memory, to name a bare <loc> (hrn:mem:<root>:<slug>)")
	return cmd
}
