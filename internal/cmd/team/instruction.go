package team

import (
	"context"
	"errors"
	"fmt"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
)

// cli#1592 (hadron-server#3307, ruling 0096, cor:dmo:050:12): a bound
// worker's claim that its holder told it to make this one change.

const instructionFlagUsage = instruction.FlagUsage
const instructionHelp = instruction.Help

var parseInstruction = instruction.Parse

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
// why is "" only when there was no binding before the lookup and there is
// still none (a binding that appears meanwhile is a change too: Gizelle,
// BLIND P2 @45626ad1). Every check is local: no request is made here.
func instructedSession(ctx context.Context, f *cmdutil.Factory, snap instructionSnapshot, targetAppID string) (session, why string, err error) {
	if snap.err != nil {
		return "", fmt.Sprintf("its session binding can't be read (%v)", snap.err), nil
	}
	b := snap.b
	if b == nil {
		now, _, err := readBindingFile(ctx)
		if errors.Is(err, errNoWorktree) || (err == nil && now == nil) {
			return "", "", nil
		}
		return "", "a session binding appeared while the worker was looked up", nil
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
	has := "this worktree has a session binding"
	if snap.b != nil {
		has = fmt.Sprintf("%s (%q)", has, snap.b.SessionID)
	}
	return exitcode.Newf(exitcode.Usage,
		"%s, but %s, so this instructed edit can't go out through it; nothing was written. Run it in the worker's own worktree against its server, or from an unbound shell as yourself",
		has, why)
}

// CredentialInstructionContext verifies a complete same-server worktree binding
// for an explicit credential instruction (Xanthos ruling #71555). It fails closed,
// never borrowing the person's authority. Target-App authorization is server-owned
// in the one credential request; no ambient App lookup is added.
func CredentialInstructionContext(f *cmdutil.Factory) func(context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		snap := snapshotForInstruction(ctx)
		if snap.b == nil && snap.err == nil {
			return ctx, exitcode.Newf(exitcode.Usage, "--instruction needs a complete worker-session binding; nothing was sent. Run from the worker's own worktree, or omit the flag to act as yourself")
		}
		target := ""
		if snap.b != nil {
			target = snap.b.AppID
		}
		session, why, err := instructedSession(ctx, f, snap, target)
		if err != nil {
			return ctx, err
		}
		if why != "" || session == "" {
			return ctx, exitcode.Newf(exitcode.Usage, "--instruction cannot verify this worker-session binding (%s); nothing was sent. Run from the worker's own worktree against its server, or omit --instruction to act as yourself", why)
		}
		return api.WithSession(ctx, session), nil
	}
}
