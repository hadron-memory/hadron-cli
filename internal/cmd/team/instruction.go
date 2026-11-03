package team

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1592 (hadron-server#3307, ruling 0096, cor:dmo:050:12): a bound
// worker's claim that its holder told it to make this one change.

const instructionFlagUsage = "the holder's instruction as <kind>:<ref>; kind is team-chat-message, task or live-chat, and ref is a reference (a team-chat message URN, a task URN, or an opaque client address), never the instruction's text. Needs server 17ef5905 or later"

// instructionHelp is the help paragraph both commands share.
const instructionHelp = `--instruction <kind>:<ref> claims, for this one call, that the worker's
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

var instructionKinds = map[string]gen.ActInstructionKind{
	"team-chat-message": gen.ActInstructionKindTeamChatMessage,
	"task":              gen.ActInstructionKindTask,
	"live-chat":         gen.ActInstructionKindLiveChat,
}

// parseInstruction reads --instruction <kind>:<ref>. Only the shape is the
// CLI's to check: the kind and a non-blank ref. The ref is everything after
// the first colon, byte for byte (URNs and client addresses contain colons);
// whether it names anything readable is the server's to judge.
func parseInstruction(raw string) (*gen.ActInstructionInput, error) {
	kind, ref, ok := strings.Cut(raw, ":")
	if !ok {
		return nil, exitcode.Newf(exitcode.Usage, "--instruction takes <kind>:<ref> (kind team-chat-message, task or live-chat), got %q", raw)
	}
	k, known := instructionKinds[kind]
	if !known {
		return nil, exitcode.Newf(exitcode.Usage, "--instruction kind %q is not one of team-chat-message, task, live-chat", kind)
	}
	if strings.TrimSpace(ref) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--instruction %s: needs a reference after the colon", kind)
	}
	return &gen.ActInstructionInput{Kind: k, Ref: ref}, nil
}

// instructionSnapshot is the worktree binding as it stood before the
// command's first lookup, for an instructed write.
type instructionSnapshot struct {
	b   *binding
	err error
}

func snapshotForInstruction(ctx context.Context) instructionSnapshot {
	b, _, err := readBindingFile(ctx)
	if errors.Is(err, errNoWorktree) {
		return instructionSnapshot{}
	}
	return instructionSnapshot{b: b, err: err}
}

// instructedSession is the worker session an instructed write may go out
// through: the snapshot's, only when it is complete, belongs to this server
// and to the TARGET worker's own App (whatever --app says), and the binding
// is unchanged now. Otherwise the session is "" and why says what failed;
// why is "" only when there is no binding at all. Every check is local: no
// request is made here.
func instructedSession(ctx context.Context, f *cmdutil.Factory, snap instructionSnapshot, targetAppID string) (session, why string, err error) {
	if snap.err != nil {
		return "", fmt.Sprintf("its session binding can't be read (%v)", snap.err), nil
	}
	b := snap.b
	if b == nil {
		return "", "", nil
	}
	if b.SessionID == "" || b.WorkerID == "" || b.AppID == "" || b.Server == "" {
		return "", "its session binding is incomplete", nil
	}
	server, err := f.Server()
	if err != nil {
		return "", "", err
	}
	if api.Endpoint(server) != api.Endpoint(b.Server) {
		return "", "its session binding belongs to " + b.Server + ", not " + server, nil
	}
	if targetAppID == "" || b.AppID != targetAppID {
		return "", "its session binding is for a different App than the worker's", nil
	}
	current, _, err := readBindingFile(ctx)
	if err != nil || !sameWriteBinding(current, b) {
		return "", "its session binding changed while the worker was looked up", nil
	}
	return b.SessionID, "", nil
}

// instructionRefusal is update's fail-closed answer when the binding can't
// carry an instructed edit: like the no-flag path, never the person's write.
func instructionRefusal(snap instructionSnapshot, why string) error {
	id := ""
	if snap.b != nil {
		id = snap.b.SessionID
	}
	return exitcode.Newf(exitcode.Usage,
		"this worktree has a session binding (%q), but %s, so this instructed edit can't go out through it; nothing was written. Run it in the worker's own worktree against its server, or from an unbound shell as yourself",
		id, why)
}
