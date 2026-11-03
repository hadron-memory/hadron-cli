package instruction

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"strings"
)

const flagUsagePrefix = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server "

const FlagUsage = flagUsagePrefix + "17ef5905 or later"

// FlagUsageForServer shares the instruction syntax while keeping each door's
// minimum independent of the text or server revision chosen by another door.
func FlagUsageForServer(minimum string) string {
	return flagUsagePrefix + minimum + " or later"
}

// Help is the help paragraph both commands share.
const Help = `--instruction <kind>:<ref> claims, for this one call, that the worker's
holder told it to make the change. kind is team-chat-message, task or
live-chat; ref is a reference to where the instruction was given (a
team-chat message URN, a task URN, or an opaque client address), never its
text. The CLI splits on the first colon only and sends the ref as written;
it reads nothing it names. A worker bound to a holder who manages the App
may make the change with it; the server records the worker, the holder and
the reference, and the holder answers for the claim. The server checks the
reference (a missing, unreadable or other-App one is refused, for a person
too), never that an instruction really happened. A host-launched session is
refused whatever it claims. The flag needs server 17ef5905 or later; an
older server's refusal is passed through, never retried without it. The MCP
tools take no instruction, so this is the way to make the call from a
worktree.`

var kinds = map[string]gen.ActInstructionKind{
	"team-chat-message": gen.ActInstructionKindTeamChatMessage,
	"task":              gen.ActInstructionKindTask,
	"live-chat":         gen.ActInstructionKindLiveChat,
}

// Parse reads --instruction <kind>:<ref>. Only the shape is the
// CLI's to check: the kind and a non-blank ref. The ref is everything after
// the first colon, byte for byte (URNs and client addresses contain colons);
// whether it names anything readable is the server's to judge.
func Parse(raw string) (*gen.ActInstructionInput, error) {
	kind, ref, ok := strings.Cut(raw, ":")
	if !ok {
		return nil, exitcode.Newf(exitcode.Usage, "--instruction takes <kind>:<ref> (kind team-chat-message, task or live-chat), got %q", raw)
	}
	k, known := kinds[kind]
	if !known {
		return nil, exitcode.Newf(exitcode.Usage, "--instruction kind %q is not one of team-chat-message, task, live-chat", kind)
	}
	if strings.TrimSpace(ref) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--instruction %s: needs a reference after the colon", kind)
	}
	return &gen.ActInstructionInput{Kind: k, Ref: ref}, nil
}
