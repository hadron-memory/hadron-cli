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
With no --node (or -m + --messages-loc) it reads the configured chat: flag,
then HADRON_CHAT_*, then "chat" in .hadron/config.json, a node URN beating the
two-field form; "chat config show" prints which one is in use and "chat doctor"
checks it. This command does not change any stored read position; for recent-only or
mentions-me reads, mark-read and waiting on your own team chat, use
"hadron team chat read --mentions-me" / "team chat mark-read". For a room named
by id or address use "hadron channel read" (--before, --mentions). There is no
blocking wait: poll with --since <nextSince> at an interval.

Output is a compact transcript ("[<seq>] <author> (<role>): <body>"); --json
returns { messages:[{seq,loc,author,identity,role,timestamp,body,sessionId,
mentions,authorWorkerId,authorUserId,authorAppId}], nextSince }. Every field
after body appears only when the message carries it: sessionId is the driving
session of a worker post, and authorWorkerId / authorUserId / authorAppId are
the platform envelope's identity — which is how you tell a WORKER post from a
human one, and a cross-App post from a local one.

` + "`author`" + ` IS THE NAME THE SERVER RECORDED (#630). It reads the envelope's
` + "`authorName`" + ` first, then the retired academy dialect's ` + "`author`" + `, and only
then falls back to parsing the message loc — which is what legacy rows with no
envelope author need. The loc suffix is a lowercase mention TOKEN rather than a
name, so reading it first rendered "jonas" where the server had written
"Jonas", and mis-attributed any handle containing a "-".

The OUTPUT KEY stays ` + "`author`" + `: the canonical team chat
(` + "`hadron team chat read`" + `) names it ` + "`authorName`" + `, and both commands emit
` + "`author`" + `, so a filter written here keeps working there (#406) — a filter
written for ` + "`authorName`" + ` still finds nothing in THIS output. Since #630 the
two agree on the VALUE as well as on that key.`,
		Example: `  hadron chat read --since 42
  hadron chat read --node hrn:node:acme.com:team-chats:team-chat:api:messages --json`,
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
	cmd.Flags().StringVar(&node, "node", "", "message-parent node URN (hrn:node:<root>:<slug>:<loc>); packs memory + message location")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "chat memory (hrn:mem:<root>:<slug>); the two-field form with --messages-loc")
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
	// Keep the unqualified drain for legacy/academy messages. Providers that
	// exclude chat-message by default need the explicit drain too. Each drain
	// proves its own paging progress before the results are merged by loc.
	platformType := "chat-message"
	filters := []*gen.NodeFilter{
		{MemoryIds: []string{c.Memory}, LocPrefix: &prefix},
		{MemoryIds: []string{c.Memory}, LocPrefix: &prefix, NodeType: &platformType},
	}
	var nodes []*messageNode
	for _, filter := range filters {
		pageNodes, err := drainMessages(ctx, client, filter)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, pageNodes...)
	}
	var msgs []Message
	seenLoc := map[string]bool{}
	for _, n := range nodes {
		if seenLoc[n.Loc] {
			continue
		}
		seenLoc[n.Loc] = true
		// The parent container is not a message. Exclude by exact loc, not
		// nodeType: legacy/academy messages predate the chat-message type.
		if n.Loc == c.MessagesLoc {
			continue
		}
		if since > 0 && (n.Seq == nil || *n.Seq <= since) {
			continue
		}
		msgs = append(msgs, parseMessage(n.Loc, n.Seq, n.Content, n.Data))
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

type messageNode = gen.ChatMessagesFindNodesFindNodesResultHitsNodeHitNode

// drainMessages has an independent seen-set for this filter. Cross-drain
// deduplication must not make a provider returning the same rows in both reads
// appear not to advance. A short page may be a server clamp; stop only on empty.
func drainMessages(ctx context.Context, client graphql.Client, filter *gen.NodeFilter) ([]*messageNode, error) {
	var nodes []*messageNode
	seenLoc := map[string]bool{}
	for offset := 0; ; {
		lim, off := chatPageSize, offset
		resp, err := gen.ChatMessages(ctx, client, filter, &lim, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp == nil || resp.FindNodes == nil {
			break
		}
		hits := resp.FindNodes.Hits
		fresh := 0
		for _, h := range hits {
			if h == nil || h.Node == nil || seenLoc[h.Node.Loc] {
				continue
			}
			seenLoc[h.Node.Loc] = true
			fresh++
			nodes = append(nodes, h.Node)
		}
		if len(hits) == 0 {
			break
		}
		if fresh == 0 {
			return nil, api.ListNotAdvancing(offset)
		}
		offset += len(hits)
	}
	return nodes, nil
}

func seqStr(seq *int) string {
	if seq == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *seq)
}
