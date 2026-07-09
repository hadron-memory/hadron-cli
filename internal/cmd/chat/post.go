package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// postDTO is the stable --json shape for a posted message.
type postDTO struct {
	Loc     string `json:"loc"`
	Seq     *int   `json:"seq"`
	ReplyTo string `json:"replyTo,omitempty"`
}

func newCmdPost(f *cmdutil.Factory) *cobra.Command {
	var node, memory, messagesLoc, handle, identity, role, body, bodyFile, replyTo string
	cmd := &cobra.Command{
		Use:   "post (--body <text|-> | --body-file <path>)",
		Short: "Post a message to a team chat",
		Long: `Post one message to a team chat. This builds the timestamped, colon-safe loc,
assembles the data payload (author/body/timestamp/mentions, plus identity/role
when set), creates the message node, and — with --reply-to — adds the reply edge,
all in one call. It also materializes the message-parent node (best-effort) so
the chat shows up as a real, copyable node in the portal.

The body comes from --body <text> (inline), --body - (stdin), or --body-file
<path> (a file — handy for a composed, multi-line message that would be painful
to quote inline). Exactly one is required.

Identity resolves from flags, then HADRON_CHAT_HANDLE, then .hadron/config.json
(top-level "handle"; chat.identity / chat.role), so a configured agent posts
with just --body.`,
		Example: `  hadron chat post --body "@rufus schema looks good, shipping it"
  hadron chat post --node acme.com::team-chats::team-chat:api:messages --handle iris \
    --role "Backend Engineer" --body "done" --reply-to team-chat:api:messages:...-rufus`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pc := loadProjectChat()
			c, err := resolveCoords(pc, node, memory, messagesLoc)
			if err != nil {
				return err
			}
			h := firstNonEmpty(handle, os.Getenv("HADRON_CHAT_HANDLE"), pc.Handle)
			if h == "" {
				return exitcode.Newf(exitcode.Usage, "no handle — pass --handle, set HADRON_CHAT_HANDLE, or add \"handle\" to .hadron/config.json")
			}
			text, err := resolveBody(cmd, body, bodyFile, f.IOStreams.In)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Loc convention: <messagesLoc>:<compact-ISO>-<handle>. The stamp is
			// the RFC3339 instant with ':' and '.' stripped (they're loc
			// separators / illegal), matching the hadron-client channel so
			// CLI- and channel-posted messages interleave cleanly.
			ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
			stamp := strings.NewReplacer(":", "", ".", "").Replace(ts)
			loc := fmt.Sprintf("%s:%s-%s", c.messagesLoc, stamp, h)

			data := map[string]any{"author": h, "body": text, "timestamp": ts}
			if id := firstNonEmpty(identity, pc.Identity); id != "" {
				data["identity"] = id
			}
			if r := firstNonEmpty(role, pc.Role); r != "" {
				data["role"] = r
			}
			if ms := mentions(text); len(ms) > 0 {
				data["mentions"] = ms
			}
			raw, err := json.Marshal(data)
			if err != nil {
				return err
			}
			dataMsg := json.RawMessage(raw)

			// Best-effort: materialize the message-parent as a real "chat" node so
			// the chat is a copyable node in the portal. Locs don't require the
			// parent to exist (messages post fine without it), so this is purely
			// cosmetic — ignore every outcome, including the expected conflict once
			// it already exists, and never let it block the post.
			ensureChatParent(cmd.Context(), client, c)

			input := gen.CreateNodeInput{
				MemoryId: c.memory,
				Loc:      loc,
				Name:     "Message from " + h,
				NodeType: strPtr("message"),
				Data:     &dataMsg,
			}
			// The reply edge goes FROM the new message TO the one it answers; a
			// short loc resolves within this memory. Minted inline with the node
			// so a post is a single round-trip.
			if replyTo != "" {
				input.Edges = []*gen.NodeEdgeInput{{TargetId: replyTo, Name: strPtr("reply")}}
			}

			resp, err := gen.CreateNode(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			dto := postDTO{Loc: loc, ReplyTo: replyTo}
			if resp.CreateNode != nil {
				dto.Loc = resp.CreateNode.Loc
				dto.Seq = resp.CreateNode.Seq
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "✓ posted %s (seq %s)\n", dto.Loc, seqStr(dto.Seq))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "message-parent node URN (org::memory::loc); packs memory + message location")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "chat memory (org::memory); the two-field form with --messages-loc")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "message-parent loc prefix; the two-field form with -m")
	cmd.MarkFlagsMutuallyExclusive("node", "memory")
	cmd.MarkFlagsMutuallyExclusive("node", "messages-loc")
	cmd.Flags().StringVar(&handle, "handle", "", "this agent's chat handle (overrides config/env)")
	cmd.Flags().StringVar(&identity, "identity", "", "real identity, e.g. the model name (default \"human\" convention); optional")
	cmd.Flags().StringVar(&role, "role", "", "this agent's role, e.g. \"Backend Engineer\"; optional")
	cmd.Flags().StringVar(&body, "body", "", "message body, or - to read from stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the message body from a file (multi-line safe)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "loc (or URN) of the message this replies to; adds a reply edge")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.MarkFlagsOneRequired("body", "body-file")
	return cmd
}

// resolveBody returns the message text from exactly one source: --body-file (a
// file), --body - (stdin), or --body <text> (inline). The mutually-exclusive /
// one-required flag group is enforced by cobra; this reads whichever was set.
func resolveBody(cmd *cobra.Command, body, bodyFile string, stdin io.Reader) (string, error) {
	var text string
	switch {
	case cmd.Flags().Changed("body-file"):
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", err
		}
		text = string(data)
	case body == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		text = string(data)
	default:
		text = body
	}
	if strings.TrimSpace(text) == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty message — nothing to post")
	}
	return text, nil
}

func strPtr(s string) *string { return &s }

// ensureChatParent best-effort creates the message-parent node so the chat is a
// real, copyable node. Create-only, so a re-post conflicts harmlessly; all
// outcomes are ignored — this must never affect the post.
func ensureChatParent(ctx context.Context, client graphql.Client, c coords) {
	name := c.messagesLoc
	if i := strings.LastIndex(c.messagesLoc, ":"); i >= 0 {
		name = c.messagesLoc[i+1:]
	}
	_, _ = gen.CreateNode(ctx, client, &gen.CreateNodeInput{
		MemoryId: c.memory,
		Loc:      c.messagesLoc,
		Name:     name,
		NodeType: strPtr("chat"),
	})
}
