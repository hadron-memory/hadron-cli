package team

import (
	"context"
	"errors"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// WorkerWriteSession is the strict resolver behind `--as-worker` (cli#1872,
// ruling R10). Unlike AuthoredWriteContext, which is best-effort and attaches
// nothing when it cannot verify a binding, this one refuses (exit 2, nothing
// sent) in every case where the bound worker session cannot be proven to be
// the one for this request: no worktree, no binding, an incomplete binding, a
// binding for another server, or an --app naming another App. It never falls
// back to the person: a compile or approve the caller asked to make as the
// worker must not be recorded as theirs.
//
// The returned function is called immediately before each mutation. It
// re-reads the binding and refuses if it changed since the resolve (a rebind
// mid-run), so a batch never sends under a session it did not verify.
func WorkerWriteSession(f *cmdutil.Factory) func(context.Context) (func(context.Context) (context.Context, error), error) {
	return func(ctx context.Context) (func(context.Context) (context.Context, error), error) {
		b, _, err := readBindingFile(ctx)
		switch {
		case errors.Is(err, errNoWorktree):
			return nil, exitcode.Newf(exitcode.Usage, "--as-worker needs a worktree with a worker-session binding, and this directory has none; nothing was sent. Run it from the bound worktree, or drop --as-worker to act as yourself")
		case err != nil:
			return nil, exitcode.Newf(exitcode.Usage, "cannot read this worktree's session binding (%v), so --as-worker cannot attribute the write; nothing was sent", err)
		case b == nil:
			return nil, exitcode.Newf(exitcode.Usage, "--as-worker needs a worker-session binding and this worktree has none; nothing was sent. Start a session here, or drop --as-worker to act as yourself")
		}
		refuse := func(why string) error {
			return exitcode.Newf(exitcode.Usage,
				"this worktree has a session binding (%q), but %s, so --as-worker cannot send through it; nothing was sent. Run it against the binding's server and App, or drop --as-worker to act as yourself",
				b.SessionID, why)
		}
		if b.SessionID == "" || b.WorkerID == "" || b.AppID == "" || b.Server == "" {
			return nil, refuse("the binding is incomplete")
		}
		server, err := f.Server()
		if err != nil {
			return nil, err
		}
		if api.Endpoint(server) != api.Endpoint(b.Server) {
			return nil, refuse("it belongs to " + b.Server + ", not " + server)
		}
		app, err := f.App()
		if err != nil {
			return nil, err
		}
		if app != "" {
			same, err := bindingsAppIdentity(ctx, f, app, b.AppID)
			if err != nil {
				return nil, exitcode.Newf(exitcode.FromError(err), "cannot confirm the binding's App (%v); nothing was sent", err)
			}
			if !same {
				return nil, refuse("it belongs to a different App than --app")
			}
		}
		// The App lookup above is a round trip that may race a rebind. The
		// returned function re-reads the binding against this snapshot right
		// before each mutation, so a swap during the lookup is caught there.
		noteEndedBinding(ctx, b)
		snapshot := *b
		return func(mctx context.Context) (context.Context, error) {
			now, _, err := readBindingFile(mctx)
			if err != nil || !sameWriteBinding(now, &snapshot) {
				return mctx, exitcode.Newf(exitcode.Usage, "this worktree's session binding changed during the run, so --as-worker stopped before sending this write (session %q); nothing further was sent", snapshot.SessionID)
			}
			return api.WithSession(mctx, snapshot.SessionID), nil
		}, nil
	}
}
