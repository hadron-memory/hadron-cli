package chat

import (
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
	Messages  []message `json:"messages"`
	NextSince int       `json:"nextSince"`
}

func newCmdRead(f *cmdutil.Factory) *cobra.Command {
	var memory, messagesLoc, chatSlug string
	var since int
	cmd := &cobra.Command{
		Use:     "read [--since <seq>]",
		Aliases: []string{"pull"},
		Short:   "Read new chat messages since a seq",
		Long: `Read chat messages in one call, newest-tracking by the server-assigned seq.
Pass --since <seq> to get only messages after that seq; omit it (or --since 0)
for the whole history. The response's nextSince is the seq to pass next turn.

Output is a compact transcript ("[<seq>] <author> (<role>): <body>"); --json
returns { messages:[{seq,loc,author,identity,role,timestamp,body}], nextSince }.`,
		Example: `  hadron chat read --since 42
  hadron chat read --chat api-redesign -m acme.com::team-chats --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCoords(loadProjectChat(), memory, messagesLoc, chatSlug)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			msgs, err := collectMessages(cmd, client, c, since)
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "chat memory (org::memory); overrides config/env")
	cmd.Flags().StringVar(&chatSlug, "chat", "", "chat slug (expands to loc prefix chats:<slug>:messages)")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "explicit messages loc prefix (overrides --chat)")
	cmd.Flags().IntVar(&since, "since", 0, "only messages with seq greater than this (0 = whole history)")
	return cmd
}

// collectMessages drains every message node under the chat prefix (findNodes
// caps a page, so it pages to exhaustion — #23), keeps those with seq > since,
// and returns them in seq order.
func collectMessages(cmd *cobra.Command, client graphql.Client, c coords, since int) ([]message, error) {
	prefix := c.messagesLoc + ":"
	filter := &gen.NodeFilter{MemoryIds: []string{c.memory}, LocPrefix: &prefix}

	var msgs []message
	for offset := 0; ; offset += chatPageSize {
		lim, off := chatPageSize, offset
		resp, err := gen.ChatMessages(cmd.Context(), client, filter, &lim, &off)
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
			if since > 0 && (n.Seq == nil || *n.Seq <= since) {
				continue
			}
			msgs = append(msgs, parseMessage(n.Loc, n.Seq, n.Data))
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
