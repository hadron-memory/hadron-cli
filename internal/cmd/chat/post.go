package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// postDTO is the stable --json shape for a posted message.
//
// `loc` SURVIVES AS AN EXPLICIT NULL, and carries no omitempty.
//
// The client used to mint the message's loc and could therefore report it. The
// server mints it now, and `TeamChatMessage` has no `loc` field — only
// `nodeId` — so there is nothing to fill it with short of a second read per
// post. Checked against the SDL rather than assumed.
//
// Dropping the key is the tempting move and the wrong one:
// findings:jq-reads-an-absent-key-as-null — an absent key and a null value are
// the SAME answer to `jq .loc` (and to encoding/json), so a vanished key
// rebuilds that ambiguity for every consumer, and its corollary is that a field
// whose null is meaningful must not be omitempty. Keeping it means
// `jq 'has("loc")'` still answers truthfully; `jq .loc` cannot tell the
// difference either way, which is exactly why the key has to stay.
//
// Same call internal/cmd/channel makes for `chatRootUrn`. `nodeId` is the
// address that replaces it.
type postDTO struct {
	Loc     *string `json:"loc"`
	NodeID  string  `json:"nodeId"`
	Seq     *int    `json:"seq"`
	ReplyTo string  `json:"replyTo,omitempty"`
	// Author is what the SERVER recorded, so an agent can verify it posted as
	// whom it intended rather than trusting the request.
	Author *string `json:"author"`
}

func newCmdPost(f *cmdutil.Factory) *cobra.Command {
	var node, memory, messagesLoc, handle, identity, role, body, bodyFile, replyTo, session string
	cmd := &cobra.Command{
		Use:   "post (--body <text|-> | --body-file <path>)",
		Short: "Post a message to a team chat",
		Long: `Post one message to a team chat, through the Channel that chat IS.

The server mints the message's loc and assigns its seq, extracts @mentions, and
records the author — so this command no longer builds any of them. The Channel
is addressed by the chat root (the parent of the message location) and created
on first post if it does not exist yet.

The body comes from --body <text> (inline), --body - (stdin), or --body-file
<path> (a file — handy for a composed, multi-line message that would be painful
to quote inline). Exactly one is required.

AUTHORSHIP COMES FROM THE SESSION. --session <id> posts as the Worker bound to
that session; with no --session the server records you, the human. --handle,
--identity and --role are still accepted from flags, HADRON_CHAT_HANDLE and
.hadron/config.json, but they no longer affect the post: hand-typed attribution
is what the Worker model replaced. Reads still show the stored envelope on
messages written before this, so old transcripts keep their author.

--reply-to takes the replied-to message's seq, or its loc/URN (resolved with
one read).`,
		Example: `  hadron chat post --body "@rufus schema looks good, shipping it"
  hadron chat post --node hrn:node:acme.com:team-chats:team-chat:api:messages \
    --session 258ceeda-… --body "done" --reply-to 41`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pc := loadProjectChat()
			c, err := resolveCoords(pc, node, memory, messagesLoc)
			if err != nil {
				return err
			}
			// --handle is no longer required, and no longer used: the server
			// records the author from the session (or the caller). Kept as an
			// accepted no-op so a configured agent does not start failing.
			h := firstNonEmpty(handle, os.Getenv("HADRON_CHAT_HANDLE"), pc.Handle)
			warnDeprecatedIdentityFlags(f, cmd, h, firstNonEmpty(identity, pc.Identity), firstNonEmpty(role, pc.Role))
			text, err := ResolveBody(cmd, body, bodyFile, f.IOStreams.In)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			res, err := PostMessage(cmd.Context(), client, PostInput{
				Coords:  c,
				Body:    text,
				ReplyTo: replyTo,
				Session: session,
			})
			if err != nil {
				return err
			}
			dto := postDTO{NodeID: res.NodeID, Seq: res.Seq, ReplyTo: replyTo, Author: res.Author}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Echo the author the SERVER recorded, not the one that was
				// asked for: posting under the wrong identity is the one
				// mistake this surface can make without saying so.
				who := "you"
				if dto.Author != nil && *dto.Author != "" {
					who = *dto.Author
				}
				fmt.Fprintf(w, "✓ posted seq %s as %s\n", seqStr(dto.Seq), who)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "message-parent node URN (hrn:node:<root>:<slug>:<loc>); packs memory + message location")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "chat memory (hrn:mem:<root>:<slug>); the two-field form with --messages-loc")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "message-parent loc prefix; the two-field form with -m")
	cmd.MarkFlagsMutuallyExclusive("node", "memory")
	cmd.MarkFlagsMutuallyExclusive("node", "messages-loc")
	cmd.Flags().StringVar(&handle, "handle", "", "this agent's chat handle (overrides config/env)")
	cmd.Flags().StringVar(&identity, "identity", "", "real identity, e.g. the model name (default \"human\" convention); optional")
	cmd.Flags().StringVar(&role, "role", "", "this agent's role, e.g. \"Backend Engineer\"; optional")
	cmd.Flags().StringVar(&body, "body", "", "message body, or - to read from stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the message body from a file (multi-line safe)")
	cmd.Flags().StringVar(&session, "session", "", "post as the Worker bound to this session id (replaces --handle/--identity/--role)")
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
		// A bad --body-file path is a user-input mistake, so it is Usage (2),
		// not the generic 1 the raw os.PathError classifies as (#390) —
		// scripts branch on the documented exit-code contract. Mirrors
		// resolveHandoff, which already wraps this.
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --body-file: %v", err)
		}
		text = string(data)
	case body == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading the message from stdin: %v", err)
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

// PostInput is one message for PostMessage.
//
// The pre-Channel identity envelope (Handle/Identity/Role) and the additive
// Extra bag are GONE from this type rather than kept as ignored fields: a
// struct field that is assigned and never read is indistinguishable from one
// that works. The flags themselves survive at the command layer, where
// warnDeprecatedIdentityFlags answers for them.
type PostInput struct {
	Coords Coords
	Body   string
	// ReplyTo is the seq, or the loc/URN, of the message this answers.
	ReplyTo string
	// Session binds the post to a worker session, which is what makes it the
	// WORKER speaking rather than the human. It is what replaced the identity
	// envelope: the server derives authorship from it.
	Session string
}

// PostResult is the created message's address, as the SERVER minted it.
type PostResult struct {
	// NodeID is the message node. The client no longer knows the loc — the
	// server mints it — so this is the address a caller gets back.
	NodeID string
	Seq    *int
	// Author is the name the SERVER recorded. Echoed back rather than assumed,
	// because posting under the wrong identity is the one mistake this surface
	// can make silently.
	Author *string
}

// PostMessage posts one message through the Channel this chat IS
// (createChannelMessage), creating that Channel on first post.
//
// It writes NO node structure of its own. That is the point of #367: the client
// used to mint the loc, assemble the `data` envelope, parse @mentions and
// best-effort materialize two container nodes, which made it a second,
// structurally incompatible chat model beside the server's. The server now owns
// all of it — including the loc's random component and the atomic seq, which is
// why this change fixes #367's two live bugs without either being patched here.
//
// The read path is deliberately NOT moved (docs/plans/chat-as-channel-wrapper.md
// §6): it already reads these same nodes, and a read regression must not be able
// to hide inside a write change.
func PostMessage(ctx context.Context, client graphql.Client, in PostInput) (PostResult, error) {
	// The Channel this chat IS. A Channel is addressed by its id or by its
	// CHAT ROOT's node ref (hadron-server#1172), and the chat root is the
	// parent of the messages container — the same derivation EnsureChatParent
	// used to make by hand. cmdutil.NodeURN composes the flat-v2 node ref the
	// server accepts, so nothing here looks a Channel up by matching on loc:
	// that rule is why internal/cmd/channel classifies no refs either.
	channelRef := chatRootRef(in.Coords)
	if channelRef == "" {
		return PostResult{}, exitcode.Newf(exitcode.Usage,
			"cannot derive the chat root from %q — a chat's messages live under a parent node (e.g. <chat>:messages)", in.Coords.MessagesLoc)
	}

	var replyToSeq *int
	if in.ReplyTo != "" {
		seq, err := resolveReplyToSeq(ctx, client, in.Coords, in.ReplyTo)
		if err != nil {
			return PostResult{}, err
		}
		replyToSeq = seq
	}

	var sessionRef *string
	if in.Session != "" {
		sessionRef = &in.Session
	}

	post := func() (*gen.CreateChannelMessageResponse, error) {
		return gen.CreateChannelMessage(ctx, client, channelRef, in.Body, replyToSeq, sessionRef)
	}
	resp, err := post()
	if err != nil && api.HasErrorCode(err, "CHANNEL_NOT_FOUND") {
		// Create-if-missing. Unlike the best-effort node materialization this
		// replaces, this one is NOT swallowed: without a Channel there is
		// nothing to post to, so a failure here is the post's failure.
		//
		// CHANNEL_NOT_FOUND also covers "you may not read its host memory",
		// deliberately and identically — so the create may refuse on access,
		// which is the correct outcome and the server's words for it.
		if cerr := ensureChannel(ctx, client, in.Coords); cerr != nil {
			return PostResult{}, cerr
		}
		resp, err = post()
	}
	if err != nil {
		return PostResult{}, api.MapError(err)
	}
	if resp == nil || resp.CreateChannelMessage == nil {
		// The mutation is non-nullable in the SDL, so an absent message is a
		// broken response rather than an empty one — say so instead of
		// reporting a post with no seq and no author as success.
		return PostResult{}, exitcode.Newf(exitcode.Unavailable, "the server returned no message")
	}
	m := resp.CreateChannelMessage
	return PostResult{NodeID: m.NodeId, Seq: &m.Seq, Author: m.AuthorName}, nil
}

// EnsureChatParent and ConvergeChatParent lived here: two best-effort
// createNode/updateNode pairs that hand-built the chat entity and the messages
// container so a chat would look like a real node in the portal. createChannel
// does that now, as the server's own operation, so both are gone rather than
// kept as a fallback — a fallback would be the two-writer split #367 exists to
// end. (ConvergeChatParent had already lost its last caller before this
// change.) The retyping advice they implemented is likewise gone from the help
// text: nothing needs converging when the server owns the structure.

// chatRootRef composes the node ref of the chat ROOT — the parent of the
// messages container — which is what a channelRef accepts (hadron-server#1172).
//
// Returns "" when the messages loc has no parent: a chat root is required, and
// guessing one would create a Channel at the wrong address.
func chatRootRef(c Coords) string {
	i := strings.LastIndex(c.MessagesLoc, ":")
	if i < 0 {
		return ""
	}
	return cmdutil.NodeURN(c.Memory, c.MessagesLoc[:i])
}

// ensureChannel creates the Channel for this chat's root. Called only after a
// CHANNEL_NOT_FOUND, so it is the create half of create-if-missing.
func ensureChannel(ctx context.Context, client graphql.Client, c Coords) error {
	i := strings.LastIndex(c.MessagesLoc, ":")
	if i < 0 {
		return exitcode.Newf(exitcode.Usage, "cannot derive the chat root from %q", c.MessagesLoc)
	}
	chatLoc := c.MessagesLoc[:i]
	name := chatLoc
	if j := strings.LastIndex(chatLoc, ":"); j >= 0 {
		name = chatLoc[j+1:]
	}
	input := gen.CreateChannelInput{MemoryRef: c.Memory, Loc: chatLoc, Name: name}
	if _, err := gen.CreateChannel(ctx, client, &input); err != nil {
		return api.MapError(err)
	}
	return nil
}

// resolveReplyToSeq turns --reply-to into the seq createChannelMessage wants.
//
// Accepts BOTH forms, Postel-liberal like every other ref here: a bare integer
// is already a seq; anything else is a node loc or URN and costs one read. The
// CLI's reply has always been a loc and a config or a script may hold one, so
// refusing them would break callers for a server-side spelling change.
//
// A target with no seq is refused loudly rather than silently posted without a
// reply: a reply pointing at nothing is worse than a refusal, and nil-seq rows
// are exactly what this migration exists to stop creating.
func resolveReplyToSeq(ctx context.Context, client graphql.Client, c Coords, replyTo string) (*int, error) {
	if n, err := strconv.Atoi(strings.TrimSpace(replyTo)); err == nil {
		return &n, nil
	}
	ref := replyTo
	if !cmdutil.IsQualifiedNodeRef(ref) && !cmdutil.IsBareID(ref) {
		ref = cmdutil.NodeURN(c.Memory, ref)
	}
	resp, err := gen.GetNode(ctx, client, ref)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp == nil || resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "no message %q is readable here to reply to", replyTo)
	}
	if resp.Node.Seq == nil {
		return nil, exitcode.Newf(exitcode.Usage,
			"message %q has no seq, so it cannot be replied to — pass the seq directly if you know it", replyTo)
	}
	return resp.Node.Seq, nil
}

// warnDeprecatedIdentityFlags warns once when a caller still supplies the
// pre-Channel identity envelope.
//
// --handle / --identity / --role were how a post said who it was before the
// server recorded authorship. A Channel derives it from the bound worker
// session, so these are accepted and IGNORED rather than removed: a
// .hadron/config.json carrying them (and there are live ones) must not start
// failing. The warning names --session, because "your flag did nothing" is
// only half an answer without the thing that replaces it.
//
// Silent on the --json path: a warning on stderr is for a human, and an agent
// gets the truth from the `author` field in the payload instead.
func warnDeprecatedIdentityFlags(f *cmdutil.Factory, cmd *cobra.Command, handle, identity, role string) {
	if f.JSON {
		return
	}
	var set []string
	if handle != "" {
		set = append(set, "handle")
	}
	if identity != "" {
		set = append(set, "identity")
	}
	if role != "" {
		set = append(set, "role")
	}
	if len(set) == 0 {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"note: %s no longer affects the post — the server records the author from the session. Use --session <id> to post as a worker; existing messages keep their stored envelope.\n",
		strings.Join(set, "/"))
}
