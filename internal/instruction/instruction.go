package instruction

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

const flagUsagePrefix = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server "

// The server release dates the --instruction doors are floored at, named once so a
// floor changes in one place (cli#2525 F4). A floor is a date, never a revision hash:
// help says "server 2026-11-03 or later" in a form the reader can compare with their
// own server.
const (
	// ServerFloorStaffing is hadron-server#3307/#3308's merge: the base --instruction
	// flag, the staffing and member doors and the credential acts.
	ServerFloorStaffing = "2026-11-03"
	// ServerFloorManager is hadron-server#3323/#3398's merge: the App-manager writes
	// and the task writes.
	ServerFloorManager = "2026-11-04"
)

const FlagUsage = flagUsagePrefix + ServerFloorStaffing + " or later"

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
refused whatever it claims. The flag needs server ` + ServerFloorStaffing + ` or later; an
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
const StaffingFlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server " + ServerFloorStaffing + " or later"

// StaffingHelp is the help paragraph `team worker cast|retire|rm` and
// `app member add|remove` share (cli#1594, hadron-server#3308). grant is the
// worker's own grant that admits it without a claim: workers.staff or
// app.members.
func StaffingHelp(grant string) string {
	return `--instruction <kind>:<ref> is for an AI worker acting on its holder's say-so
(the holder is the person responsible for the worker). It claims that the
holder told the worker, bound to this working directory, to make this one
change. kind is team-chat-message, task or live-chat; ref points to where they
said so (a team-chat message URN, a task URN, or an opaque client address),
never its text. A bad kind or a blank ref is refused (exit 2) before any prompt
or request.

Without the flag the call is yours, as the signed-in person. With it, the call
goes out through the worker's session when its binding is complete and for this
server and App; otherwise it goes out as you, with a note on stderr, and the
server records no claim. The holder answers for the claim and still needs the
right to make the change themselves; the server checks the reference, never
that an instruction really happened. A host-launched session's instruction is
ignored: it acts only on its own ` + grant + ` grant, which nobody holds by
default; a worker session with no instruction needs the same grant. The MCP
tools take no instruction. Needs server ` + ServerFloorStaffing + ` or later; an older server's
refusal is passed through and never retried without the flag; a FORBIDDEN
refusal is exit 8. More: "hadron agentic-usage", "Instructed staffing and
member acts".`
}

// AddManagerFlag registers --instruction and appends ManagerHelp on one of the
// six App-manager writes (cli#2448). The two doors, `app ...` and `team ...`,
// share the registration but not the server minimum: each passes its own, so
// one door's version text never moves with the other's (the same independence
// FlagUsageForServer documents). The app door's minimum is the dated release
// ManagerHelp names; the team door pins the server revision its settings and
// grant mutations landed in.
func AddManagerFlag(cmd *cobra.Command, minimum string) {
	cmd.Flags().String("instruction", "", FlagUsageForServer(minimum))
	cmd.Long += "\n\n" + ManagerHelp
}

// ManagerHelp is shared by the six App-manager writes in cli#2448.
const ManagerHelp = `--instruction <kind>:<ref> is for an AI worker acting on its holder's say-so
(the holder is the person responsible for the worker). It claims that the
holder told it to make this change. kind is team-chat-message, task or
live-chat; ref is everything after the first colon, sent unchanged, never the
instruction's text. A bad kind or a blank ref is refused (exit 2) before any
prompt or request.

Without the flag this is your ordinary call as the signed-in person. With it,
the call goes out through the worker's session when its binding is complete and
for this server and App; otherwise it goes out as you with the claim still sent
(a note on stderr says why when a binding was unusable). The holder must manage
the App, and a host-launched session is refused. The server checks the
reference, never that an instruction really happened. A change made with an
instruction records one event naming only ids; a call that changes nothing
records none. A server refusal is never retried as you or without the claim.
The MCP tools take no instruction. Needs server ` + ServerFloorManager + ` or later. More:
"hadron agentic-usage", "App-manager writes with an instruction".`
