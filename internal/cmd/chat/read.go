package chat

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// chatPageSize bounds each findNodes page while draining the message list.
const chatPageSize = 200

// readDTO is the stable --json shape: the new messages plus the high-water seq
// to pass as --since next turn.
type readDTO struct {
	Messages  []Message `json:"messages"`
	NextSince int       `json:"nextSince"`
}

func newCmdRead(f *cmdutil.Factory) *cobra.Command {
	var node, memory, messagesLoc string
	var since int
	cmd := &cobra.Command{
		Use:     "read [--since <seq>]",
		Aliases: []string{"pull"},
		Short:   "Read new chat messages since a seq",
		Long: `Read chat messages in one call, newest-tracking by the server-assigned seq.
Pass --since <seq> to get only messages after that seq; omit it (or --since 0)
for the whole history. The response's nextSince is the seq to pass next turn.

Output is a compact transcript ("[<seq>] <author> (<role>): <body>"); --json
returns { messages:[{seq,loc,author,identity,role,timestamp,body,sessionId,
mentions}], nextSince } — sessionId (an agent post's driving session, #369)
and mentions appear only when the message carries them.

This is the retired academy dialect and names the author ` + "`author`" + `. The
canonical team chat (` + "`hadron team chat read`" + `) names it ` + "`authorName`" + `, and
also splits authorUserId / authorAgentId. Both commands emit ` + "`author`" + `, so a
filter written here keeps working there (#406) — but a filter written for
` + "`authorName`" + ` finds nothing in THIS output.`,
		Example: `  hadron chat read --since 42
  hadron chat read --node acme.com::team-chats::team-chat:api:messages --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCoords(loadProjectChat(), node, memory, messagesLoc)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			msgs, err := CollectMessages(cmd.Context(), client, c, since)
			if err != nil {
				return err
			}

			next := since
			for _, m := range msgs {
				if m.Seq != nil && *m.Seq > next {
					next = *m.Seq
				}
			}
			dto := readDTO{Messages: msgs, NextSince: next}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				for _, m := range dto.Messages {
					who := m.Author
					if m.Role != "" {
						who = fmt.Sprintf("%s (%s)", m.Author, m.Role)
					}
					fmt.Fprintf(w, "[%s] %s: %s\n", seqStr(m.Seq), who, m.Body)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "message-parent node URN (org::memory::loc); packs memory + message location")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "chat memory (org::memory); the two-field form with --messages-loc")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "message-parent loc prefix; the two-field form with -m")
	cmd.Flags().IntVar(&since, "since", 0, "only messages with seq greater than this (0 = whole history)")
	cmd.MarkFlagsMutuallyExclusive("node", "memory")
	cmd.MarkFlagsMutuallyExclusive("node", "messages-loc")
	return cmd
}

// CollectMessages drains every message node under the chat prefix (findNodes
// caps a page, so it pages to exhaustion — #23), keeps those with seq > since,
// and returns them in seq order. The read side of the shared message-node
// dialect (see PostMessage).
func CollectMessages(ctx context.Context, client graphql.Client, c Coords, since int) ([]Message, error) {
	prefix := c.MessagesLoc + ":"
	filter := &gen.NodeFilter{MemoryIds: []string{c.Memory}, LocPrefix: &prefix}

	var msgs []Message
	for offset := 0; ; offset += chatPageSize {
		lim, off := chatPageSize, offset
		resp, err := gen.ChatMessages(ctx, client, filter, &lim, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp == nil || resp.FindNodes == nil {
			break
		}
		hits := resp.FindNodes.Hits
		for _, h := range hits {
			if h == nil || h.Node == nil {
				continue
			}
			n := h.Node
			// #412: the message-PARENT container comes back from the prefix
			// filter alongside its children, and parsed as a message it reads
			// as `seq: null, author: "unknown", body: ""` — indistinguishable
			// in the JSON from a genuinely malformed post, and off-by-one on
			// any count. Its nil seq excludes it from `--since <n>` reads,
			// which is why it only surfaces on a full-history read: the one a
			// new session does on its first turn.
			//
			// Excluded by exact loc, NOT by node type. Type-filtering looks
			// like the more honest predicate, but this reader exists to serve
			// the RETIRED academy dialect (kept unmigrated because its seq
			// numbers are the citation mechanism), whose messages predate the
			// chat-message nodeType — so filtering on type would drop the very
			// messages the command is for. The container, by contrast, is
			// exactly the loc we asked about.
			if n.Loc == c.MessagesLoc {
				continue
			}
			if since > 0 && (n.Seq == nil || *n.Seq <= since) {
				continue
			}
			msgs = append(msgs, parseMessage(n.Loc, n.Seq, n.Content, n.Data))
		}
		if len(hits) < chatPageSize {
			break
		}
	}

	// The server sorts by seq, but a nil-seq legacy row or paging edge could
	// disturb order — sort defensively so nextSince and the transcript agree.
	sort.SliceStable(msgs, func(i, j int) bool {
		if msgs[i].Seq == nil {
			return false
		}
		if msgs[j].Seq == nil {
			return true
		}
		return *msgs[i].Seq < *msgs[j].Seq
	})
	return msgs, nil
}

func seqStr(seq *int) string {
	if seq == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *seq)
}
