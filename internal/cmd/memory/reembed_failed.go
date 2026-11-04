package memory

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

// reembedFailedDTO is the stable --json shape of `memory reembed-failed`.
//
// Restamped is the count the server re-stamped for another embedding attempt.
// It is NOT a count of nodes that ended up indexed: a node whose embedding
// fails deterministically (hadron-server#887) fails again, so the name says
// what the number means rather than what a reader hopes.
type reembedFailedDTO struct {
	Memory    string `json:"memory"`
	MemoryID  string `json:"memoryId"`
	Restamped int    `json:"restamped"`
}

func newCmdReembedFailed(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reembed-failed <memoryRef>",
		Short: "Ask the server to retry every node whose embedding failed",
		Long: `Retry every node in a memory whose embedding failed. An embedding is the
vector the server computes from a node's text so that search can find it by
meaning (search --mode vector or hybrid); a node whose embedding failed is
missing from those results. The server marks each such node (one waiting to
retry, or one that failed for good) to be tried again, and returns how many it
marked.

The number is marked, not "now indexed". A node that fails every time (for
example one too long for the embedding provider's time limit) fails again. 0 is
a normal answer; it also means the memory has no vector index. Confirm the outcome afterwards with
` + "`hadron memory validate <memoryRef> --check embed-failed`" + `.

This is a mutation, kept apart from the read-only ` + "`memory validate`" + ` so an audit
never changes anything. Running it IS the request, so it does not prompt. It is
sent exactly once: a transport error leaves the outcome unknown and is not
retried for you — re-run it yourself if you want another pass. The server's own
gate applies (memory owner or org ADMIN), as do its limits on encrypted memories.`,
		Example: `  hadron memory validate hrn:mem:hadronmemory.com:specs --check embed-failed
  hadron memory reembed-failed hrn:mem:hadronmemory.com:specs
  hadron memory reembed-failed hrn:mem:hadronmemory.com:specs --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.RetryFailedEmbeddings(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			// 0 is a real answer; a missing/null result is not 0. The mutation
			// was sent and its outcome did not reach us, so say so (Unavailable)
			// and do not retry it for the caller.
			if resp == nil || resp.RetryFailedEmbeddings == nil {
				return exitcode.Newf(exitcode.Unavailable, "reembed-failed outcome unknown: the server's answer carried no re-stamped count (the request was sent once and is not retried; check with `hadron memory validate %s --check embed-failed`)", args[0])
			}
			dto := reembedFailedDTO{
				Memory:    cmdutil.CanonicalMemoryRef(args[0]),
				MemoryID:  memID,
				Restamped: *resp.RetryFailedEmbeddings,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ re-stamped %d node(s) in %s for another embedding attempt (not a count of indexed nodes — confirm with `hadron memory validate %s --check embed-failed`)\n",
					dto.Restamped, dto.Memory, dto.Memory)
				return err
			})
		},
	}
	return cmd
}
