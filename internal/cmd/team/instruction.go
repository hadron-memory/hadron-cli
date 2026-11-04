package team

import (
	"context"
	"errors"
	"fmt"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
		return "", "a session binding appeared while the target was looked up", nil
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
		return "", "its session binding is for a different App than the one this change targets", nil
	}
	current, _, err := readBindingFile(ctx)
	if err != nil || !sameWriteBinding(current, b) {
		return "", "its session binding changed while the target was looked up", nil
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

type credentialInstructionSnapshotKey struct{}

// CredentialInstructionContext verifies a complete same-server worktree binding
// for an explicit credential instruction (Xanthos ruling #71555). It fails closed,
// never borrowing the person's authority. Target-App authorization is server-owned
// in the one credential request; no ambient App lookup is added.
func CredentialInstructionContext(f *cmdutil.Factory) func(context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		snap, captured := ctx.Value(credentialInstructionSnapshotKey{}).(instructionSnapshot)
		if !captured {
			snap = snapshotForInstruction(ctx)
			ctx = context.WithValue(ctx, credentialInstructionSnapshotKey{}, snap)
		}
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

// cli#1594 (hadron-server#3308, ruling 0096/0097): cast (and its preview),
// retire, rm, and the App member add/remove are not authored writes, so
// without --instruction they go out as the signed-in person, headerless,
// even from a bound worktree. With it they follow change-agent's row of the
// #1592 matrix: the claim goes out through the binding's worker session only
// when that binding, as it stood before the command's first lookup, is
// complete, for this server and for the TARGET App, and unchanged at send
// time; otherwise as the person, headerless, with a note on stderr (the
// server then checks the person's reference but records no claim). Every
// lookup made to decide this is headerless.

// InstructedWrite yields the context for the one instructed write. Pass the
// target App's id when the command already holds it (a resolved worker's
// AppId), else "" and the App ref the write names (id, URN or alias).
// worker reports whether the worker session was attached (the claim, once
// admitted, is recorded); false means the call goes out as the person.
type InstructedWrite func(ctx context.Context, targetAppID, targetAppRef string) (out context.Context, worker bool, err error)

// BeginInstructedWrite snapshots the binding NOW; call it before the
// command's first lookup.
func BeginInstructedWrite(f *cmdutil.Factory) func(context.Context) InstructedWrite {
	return func(ctx context.Context) InstructedWrite {
		snap := snapshotForInstruction(ctx)
		return func(ctx context.Context, targetAppID, targetAppRef string) (context.Context, bool, error) {
			if targetAppID == "" && snap.b != nil && snap.err == nil {
				// Everything but the App first, so an unusable binding costs
				// no lookup.
				if _, why, err := instructedSession(ctx, f, snap, snap.b.AppID); err != nil || why != "" {
					return instructedAsPerson(ctx, f, why, err)
				}
				id, err := instructedTargetAppID(ctx, f, targetAppRef, snap.b.AppID)
				if err != nil {
					return ctx, false, err
				}
				if id == "" {
					// app(ref:) is readable to the App's org members only, so
					// an App member outside the org lands here: say so, not
					// "a different App".
					return instructedAsPerson(ctx, f, "the target App "+targetAppRef+" can't be read as you, so it can't be matched to the binding", nil)
				}
				targetAppID = id
			}
			session, why, err := instructedSession(ctx, f, snap, targetAppID)
			if err != nil || why != "" || session == "" {
				// session "" with no why: no binding before or after, so the
				// person's call, without a note.
				return instructedAsPerson(ctx, f, why, err)
			}
			return api.WithSession(ctx, session), true, nil
		}
	}
}

// instructedTargetAppID resolves the App ref a write names, headerless.
// An App this caller can't see is "" (not the binding's): the write then
// goes out as the person and the server answers it.
func instructedTargetAppID(ctx context.Context, f *cmdutil.Factory, ref, bindingAppID string) (string, error) {
	if ref == bindingAppID {
		return ref, nil
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return "", err
	}
	resp, err := gen.TeamAppIdentity(ctx, client, ref)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp == nil || resp.App == nil {
		return "", nil
	}
	return resp.App.Id, nil
}

func instructedAsPerson(ctx context.Context, f *cmdutil.Factory, why string, err error) (context.Context, bool, error) {
	if err != nil {
		return ctx, false, err
	}
	if why != "" {
		fmt.Fprintf(f.IOStreams.ErrOut, "note: this worktree's worker session is not used (%s); the change goes out as the signed-in person, with the instruction checked but not recorded\n", why)
	}
	return ctx, false, nil
}

// BeginServerTargetInstructedWrite is for person writes whose server resolves
// the target App (cli#1610, Xanthos #72650). Verify the binding locally but do
// not infer an App from an asset id. A server refusal never triggers fallback.
func BeginServerTargetInstructedWrite(f *cmdutil.Factory) func(context.Context) func(context.Context) (context.Context, bool, error) {
	return func(ctx context.Context) func(context.Context) (context.Context, bool, error) {
		snap := snapshotForInstruction(ctx)
		return func(ctx context.Context) (context.Context, bool, error) {
			appID := ""
			if snap.b != nil {
				appID = snap.b.AppID
			}
			session, why, err := instructedSession(ctx, f, snap, appID)
			if err != nil || why != "" || session == "" {
				return instructedAsPerson(ctx, f, why, err)
			}
			return api.WithSession(ctx, session), true, nil
		}
	}
}
