package memory

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
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
		Long: `Re-drive a memory's failed embeddings: the server re-stamps each live node whose
embedding failed (a failure awaiting its backoff, or a permanent-class one) so
the embedding worker attempts it again, and returns how many it re-stamped.

The number is RE-STAMPED, not "now indexed". A node that fails deterministically
(for example one that always exceeds the provider's invocation timeout,
hadron-server#887) fails again, and 0 is a legitimate answer — it also means the
memory is not vector-indexed. Confirm the outcome afterwards with
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
			dto := reembedFailedDTO{
				Memory:    cmdutil.CanonicalMemoryRef(args[0]),
				MemoryID:  memID,
				Restamped: resp.RetryFailedEmbeddings,
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
