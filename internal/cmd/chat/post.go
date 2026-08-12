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
		Long: `Post one message to a team chat, in the canonical chat shape: the body in the
node's content, the envelope (author/timestamp/mentions, plus identity/role
when set) in its data, nodeType chat-message. This builds the timestamped,
colon-safe loc, creates the message node, and — with --reply-to — adds the
reply edge, all in one call. It also materializes the chat's structure
best-effort: the chat entity (typed chat) and the messages container (typed
record), so the chat shows up as a real, copyable node in the portal. A chat
created by an older CLI keeps its container typed chat until retyped —
` + "`hadron team init`" + ` converges team chats; for others:
hadron node update <container-urn> --type record

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
			text, err := ResolveBody(cmd, body, bodyFile, f.IOStreams.In)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			res, err := PostMessage(cmd.Context(), client, PostInput{
				Coords:   c,
				Handle:   h,
				Identity: firstNonEmpty(identity, pc.Identity),
				Role:     firstNonEmpty(role, pc.Role),
				Body:     text,
				ReplyTo:  replyTo,
			})
			if err != nil {
				return err
			}
			dto := postDTO{Loc: res.Loc, Seq: res.Seq, ReplyTo: replyTo}
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

// ResolveBody returns the message text from exactly one source: --body-file (a
// file), --body - (stdin), or --body <text> (inline). The mutually-exclusive /
// one-required flag group is enforced by cobra; this reads whichever was set.
func ResolveBody(cmd *cobra.Command, body, bodyFile string, stdin io.Reader) (string, error) {
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

// PostInput is one message for PostMessage — the single implementation of
// the message-node dialect's write side, shared by `hadron chat post` and
// `hadron team chat post` so the shape can't drift between them (or from the
// hadron-client push channel it mirrors).
type PostInput struct {
	Coords   Coords
	Handle   string
	Identity string
	Role     string
	Body     string
	// ReplyTo is the loc (or URN) of the message this answers; adds the
	// reply edge inline with the create.
	ReplyTo string
	// Extra adds additive data fields (e.g. sessionId, #369 D16). Dialect
	// keys (author/body/timestamp/identity/role/mentions) always win — an
	// Extra entry never overrides them.
	Extra map[string]any
}

// PostResult is the created message's address.
type PostResult struct {
	Loc string
	Seq *int
}

// PostMessage builds the timestamped colon-safe loc, assembles the message in
// the CANONICAL chat shape (D-2026-08-07-001/-004: body in `content`, the
// envelope in `data`, nodeType `chat-message` — the academy data.body dialect
// is retired on the write side, still accepted on reads), best-effort
// materializes the chat entity + messages container, and creates the message
// node (with the optional reply edge) in one round-trip.
func PostMessage(ctx context.Context, client graphql.Client, in PostInput) (PostResult, error) {
	// Loc convention: <messagesLoc>:<compact-ISO>-<handle>. The stamp is
	// the RFC3339 instant with ':' and '.' stripped (they're loc
	// separators / illegal), matching the hadron-client channel so
	// CLI- and channel-posted messages interleave cleanly.
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	stamp := strings.NewReplacer(":", "", ".", "").Replace(ts)
	loc := fmt.Sprintf("%s:%s-%s", in.Coords.MessagesLoc, stamp, in.Handle)

	data := map[string]any{}
	for k, v := range in.Extra {
		// Reserved dialect keys never come from Extra — not even when the
		// typed input is empty (an empty Identity means "no identity", not
		// "take Extra's"). "body" stays reserved even though the canonical
		// shape carries the body in content: an Extra body would shadow it
		// for legacy readers.
		switch k {
		case "author", "body", "timestamp", "identity", "role", "mentions":
			continue
		}
		data[k] = v
	}
	data["author"] = in.Handle
	data["timestamp"] = ts
	if in.Identity != "" {
		data["identity"] = in.Identity
	}
	if in.Role != "" {
		data["role"] = in.Role
	}
	if ms := Mentions(in.Body); len(ms) > 0 {
		data["mentions"] = ms
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return PostResult{}, err
	}
	dataMsg := json.RawMessage(raw)

	// Best-effort: materialize the message-parent as a real "chat" node so
	// the chat is a copyable node in the portal. Locs don't require the
	// parent to exist (messages post fine without it), so this is purely
	// cosmetic — ignore every outcome, including the expected conflict once
	// it already exists, and never let it block the post.
	EnsureChatParent(ctx, client, in.Coords)

	input := gen.CreateNodeInput{
		MemoryId: in.Coords.Memory,
		Loc:      loc,
		Name:     "Message from " + in.Handle,
		// chat-message (D-2026-08-07-001): the dedicated type default search
		// will exclude; the body lives in content (D-2026-08-07-004).
		NodeType: strPtr("chat-message"),
		Content:  &in.Body,
		Data:     &dataMsg,
	}
	// The reply edge goes FROM the new message TO the one it answers; a
	// short loc resolves within this memory. Minted inline with the node
	// so a post is a single round-trip.
	if in.ReplyTo != "" {
		input.Edges = []*gen.NodeEdgeInput{{TargetId: in.ReplyTo, Name: strPtr("reply")}}
	}

	resp, err := gen.CreateNode(ctx, client, &input)
	if err != nil {
		return PostResult{}, api.MapError(err)
	}
	res := PostResult{Loc: loc}
	if resp.CreateNode != nil {
		res.Loc = resp.CreateNode.Loc
		res.Seq = resp.CreateNode.Seq
	}
	return res, nil
}

// EnsureChatParent best-effort creates the chat's structural nodes so the
// chat is real and copyable in the portal: the CHAT ENTITY (the parent of the
// messages container) typed `chat`, and the messages container itself as a
// plain `record` (D-2026-08-07-004 — the chat type belongs to the entity, not
// the container; a messagesLoc with no parent gets only the container).
// Create-only, so a re-post conflicts harmlessly; all outcomes are ignored —
// this must never affect the post.
func EnsureChatParent(ctx context.Context, client graphql.Client, c Coords) {
	name := c.MessagesLoc
	if i := strings.LastIndex(c.MessagesLoc, ":"); i >= 0 {
		chatLoc := c.MessagesLoc[:i]
		name = c.MessagesLoc[i+1:]
		chatName := chatLoc
		if j := strings.LastIndex(chatLoc, ":"); j >= 0 {
			chatName = chatLoc[j+1:]
		}
		_, _ = gen.CreateNode(ctx, client, &gen.CreateNodeInput{
			MemoryId: c.Memory,
			Loc:      chatLoc,
			Name:     chatName,
			NodeType: strPtr("chat"),
		})
	}
	_, _ = gen.CreateNode(ctx, client, &gen.CreateNodeInput{
		MemoryId: c.Memory,
		Loc:      c.MessagesLoc,
		Name:     name,
		NodeType: strPtr("record"),
	})
}

// ConvergeChatParent retypes an EXISTING chat structure onto the canonical
// D-2026-08-07-004 types — the migration path for chats materialized before
// the shape change, whose messages container was created typed `chat`.
// Deliberately separate from EnsureChatParent: per-post convergence would
// tax every message with extra write calls forever, so retyping runs from an
// explicit, idempotent setup step (`hadron team init`; other chats use
// `hadron node update <container> --type record`). Best-effort like Ensure —
// a missing node (nothing posted yet) or a permission refusal is ignored.
func ConvergeChatParent(ctx context.Context, client graphql.Client, c Coords) {
	if i := strings.LastIndex(c.MessagesLoc, ":"); i >= 0 {
		chatLoc := c.MessagesLoc[:i]
		_, _ = gen.UpdateNode(ctx, client, &gen.UpdateNodeInput{
			MemoryId: &c.Memory,
			Loc:      &chatLoc,
			NodeType: strPtr("chat"),
		})
	}
	_, _ = gen.UpdateNode(ctx, client, &gen.UpdateNodeInput{
		MemoryId: &c.Memory,
		Loc:      &c.MessagesLoc,
		NodeType: strPtr("record"),
	})
}
