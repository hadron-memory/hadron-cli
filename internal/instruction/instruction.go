package instruction

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"strings"
)

const FlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server 17ef5905 or later"

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

// StaffingFlagUsage is the flag line for the staffing and member doors
// (cli#1594), whose minimum server is hadron-server#3308's merge.
const StaffingFlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server 517eb1db or later"

// StaffingHelp is the help paragraph `team worker cast|retire|rm` and
// `app member add|remove` share (cli#1594, hadron-server#3308). grant is the
// worker's own grant that admits it without a claim: workers.staff or
// app.members.
func StaffingHelp(grant string) string {
	return `INSTRUCTED (--instruction <kind>:<ref>). Without the flag this call is the
signed-in person's, as before, even in a bound worktree: it carries no worker
session. With it, the call claims that the bound worker's holder told it to
make this one change. kind is team-chat-message, task or live-chat; ref is a
reference to where the instruction was given (a team-chat message URN, a task
URN, or an opaque client address), never its text. The CLI splits on the
first colon only and sends the ref as written; a bad kind or a blank ref is
refused (exit 2) before any prompt or request.

The call goes out through this worktree's worker session when the binding,
as it stood before the command's first lookup, is complete, for this server
and for the App the change targets, and is unchanged when it is sent. Otherwise
it goes out as the signed-in person, with a note on stderr saying why: the
server still checks the reference and refuses a bad one, but records no
claim. Lookups made to decide this never carry the session.

The holder answers for the claim, and still needs the right to make the
change themselves. The server checks the reference (a missing, unreadable or
other-App one is refused, for a person too), never that an instruction really
happened. An admitted claim is recorded with the worker, the holder and the
reference, also for a change that changes nothing (an already-retired worker,
an existing or absent member). A host-launched session's instruction is
ignored, neither checked nor recorded: it acts only on its own ` + grant + `
grant, refused without one and admitted with one. A worker session making
this change without an instruction needs that same grant, which nobody holds
by default. The MCP tools take no instruction, so a worker session there
needs the grant; this flag is the way to make the call from a worktree.

Needs server 517eb1db (hadron-server#3308) or later; an older server's
refusal is passed through, never retried without the flag. Refusals keep
their codes: FORBIDDEN is exit 8, with serverCode in --json errors.`
}
