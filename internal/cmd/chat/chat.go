// Package chat implements `hadron chat ...` — a token-lean surface for AI agents
// (and humans) participating in a Hadron team-chat memory (see the how-to guide
// "Set up an agent team chat"). Messages are message-type nodes under a
// `messagesLoc` prefix, the payload in each node's `data`; the server assigns an
// ordering `seq`. `chat read` pulls new messages in one compact call; `chat
// post` writes one (with an optional reply edge). Both resolve the agent's
// identity and chat coordinates from flags, env, or the project-local
// .hadron/config.json shared with the hadron-client push channel.
package chat

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	urnlib "github.com/hadron-memory/urn-lib-go"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdChat builds the `chat` command group.
func NewCmdChat(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat <command>",
		Short: "Participate in a Hadron team chat (agent-friendly read/post)",
		Long: `Read and post messages in a Hadron team-chat memory — a low-friction surface
over the message-node protocol, built so an AI agent spends as few tokens and as
little attention as possible on coordination.

A chat is named by the URN of the node whose direct children are its messages
(--node, or "chat":{"node"} in config) — one copyable value that packs the
memory and the message location. The two-field form (-m + --messages-loc, or
"chat":{"memory","messagesLoc"} — what the push channel uses) is equivalent.

Those coordinates and this agent's identity resolve from (highest first): a
flag, the matching HADRON_CHAT_* env var, then the project-local
.hadron/config.json (the same file the hadron-client push channel reads —
top-level "handle" and "chat": { "node" | "memory"+"messagesLoc", "identity",
"role" }). Configure once and a turn is just 'chat read --since <seq>' /
'chat post --body …'.`,
	}
	cmd.AddCommand(newCmdRead(f))
	cmd.AddCommand(newCmdPost(f))
	return cmd
}

// coords is the resolved (memory, messagesLoc) of one chat. messagesLoc is the
// loc prefix whose direct child nodes are the messages (e.g.
// "team-chat:academy:chat:messages").
type coords struct {
	memory      string
	messagesLoc string
}

// resolveCoords locates the chat. The primary form is a single message-parent
// node URN — --node, then HADRON_CHAT_NODE, then chat.node — which packs the
// memory and messagesLoc into one copyable, URN-conformant value. The
// equivalent two-field form (-m + --messages-loc, the HADRON_CHAT_MEMORY /
// HADRON_CHAT_MESSAGES_LOC env vars, or chat.memory + chat.messagesLoc — what
// the push channel uses) is the fallback. All resolve to the same (memory,
// messagesLoc); messages are direct children of messagesLoc.
func resolveCoords(pc projectChat, nodeFlag, memoryFlag, messagesLocFlag string) (coords, error) {
	if ref := firstNonEmpty(nodeFlag, os.Getenv("HADRON_CHAT_NODE"), pc.Node); ref != "" {
		memory, loc, err := splitNodeURN(ref)
		if err != nil {
			return coords{}, err
		}
		return coords{memory: memory, messagesLoc: loc}, nil
	}
	memory := firstNonEmpty(memoryFlag, os.Getenv("HADRON_CHAT_MEMORY"), pc.Memory)
	if memory == "" {
		return coords{}, exitcode.Newf(exitcode.Usage, "no chat — pass --node <message-parent-urn>, or -m/--memory + --messages-loc; or set them (chat.node, or chat.memory + chat.messagesLoc) in .hadron/config.json")
	}
	messagesLoc := firstNonEmpty(messagesLocFlag, os.Getenv("HADRON_CHAT_MESSAGES_LOC"), pc.MessagesLoc)
	if messagesLoc == "" {
		return coords{}, exitcode.Newf(exitcode.Usage, "no message location — pass --messages-loc <prefix> (or use --node <urn>), set HADRON_CHAT_MESSAGES_LOC, or add chat.messagesLoc to .hadron/config.json")
	}
	// A trailing colon would double against the ":<id>" join; normalize it off.
	messagesLoc = strings.TrimSuffix(messagesLoc, ":")
	return coords{memory: memory, messagesLoc: messagesLoc}, nil
}

// splitNodeURN splits a fully-qualified node URN into its memory (org::memory)
// and loc. Accepts an optional hrn:node: / urn:node: prefix. The loc is the
// message-parent prefix — its direct children are the messages.
func splitNodeURN(ref string) (memory, loc string, err error) {
	raw := strings.TrimSpace(ref)
	// A scheme-prefixed URN (hrn:node:/urn:node:) is unambiguous — the grammar
	// fixes the memory as the first two atoms, so both the flat grammar-v2 form
	// (hrn:node:<root>:<slug>:<loc…>) and the legacy <org>::<memory>::<loc> split
	// deterministically. A BARE ref must carry the two "::" separators: a bare
	// single-colon form is ambiguous once the loc itself has colons, so require
	// the scheme prefix for those.
	if !urnlib.HasSchemePrefix(raw) && strings.Count(raw, "::") < 2 {
		return "", "", exitcode.Newf(exitcode.Usage, "%q is not a fully-qualified node URN — expected <org>::<memory>::<loc>, or the flat hrn:node:<root>:<slug>:<loc> form", ref)
	}
	parts, splitErr := urnlib.SplitNodeUrn(raw)
	if splitErr != nil {
		return "", "", exitcode.Newf(exitcode.Usage, "%q is not a fully-qualified node URN — expected <org>::<memory>::<loc>, or the flat hrn:node:<root>:<slug>:<loc> form", ref)
	}
	// SplitNodeUrn hands back the memory as a bare <root>:<slug>; canonicalize it
	// to the grammar-v2 flat URN (hrn:mem:<root>:<slug>) the server now emits and
	// resolves.
	mem := cmdutil.CanonicalMemoryRef(parts.MemoryURN)
	return mem, strings.TrimSuffix(parts.Loc, ":"), nil
}

// message is the parsed, chat-shaped view of one message node — only the fields
// a participant cares about, lifted out of the node's generic `data` block.
type message struct {
	Seq       *int   `json:"seq"`
	Loc       string `json:"loc"`
	Author    string `json:"author"`
	Identity  string `json:"identity,omitempty"`
	Role      string `json:"role,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Body      string `json:"body"`
}

// parseMessage lifts the chat fields out of a message node's data block. Author
// falls back to the loc's "<timestamp>-<handle>" suffix when data has none, so
// hand-created messages still attribute (matching the channel's messageAuthor).
func parseMessage(loc string, seq *int, data *json.RawMessage) message {
	m := message{Seq: seq, Loc: loc}
	if data != nil {
		var d struct {
			Author    string `json:"author"`
			Identity  string `json:"identity"`
			Role      string `json:"role"`
			Timestamp string `json:"timestamp"`
			Body      string `json:"body"`
		}
		if json.Unmarshal(*data, &d) == nil {
			m.Author, m.Identity, m.Role, m.Timestamp, m.Body = d.Author, d.Identity, d.Role, d.Timestamp, d.Body
		}
	}
	if m.Author == "" {
		m.Author = authorFromLoc(loc)
	}
	return m
}

// authorFromLoc recovers the handle from a "<prefix>:<timestamp>-<handle>" loc.
// The "Z-" terminator splits stamp from handle so dashed handles survive; a
// legacy no-Z stamp falls back to the last dash.
func authorFromLoc(loc string) string {
	last := loc
	if i := strings.LastIndex(loc, ":"); i >= 0 {
		last = loc[i+1:]
	}
	if i := strings.Index(last, "Z-"); i >= 0 {
		return last[i+2:]
	}
	if i := strings.LastIndex(last, "-"); i >= 0 {
		return last[i+1:]
	}
	return "unknown"
}

// mentionRE matches an @handle not preceded by a word char or dot (so
// user@example.com isn't a mention). Go's regexp has no lookbehind, so the
// boundary is a captured group we discard. Mirrors the channel's MENTION_RE.
var mentionRE = regexp.MustCompile(`(^|[^a-zA-Z0-9._-])@([a-z0-9_-]+)`)

// mentions extracts the distinct @handles from a body, in first-seen order.
func mentions(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range mentionRE.FindAllStringSubmatch(body, -1) {
		h := strings.ToLower(m[2])
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}
