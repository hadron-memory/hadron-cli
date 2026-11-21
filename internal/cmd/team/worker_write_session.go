package team

import (
	"context"
	"errors"
	"sync"

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
func WorkerWriteSession(f *cmdutil.Factory) func(context.Context, ...string) (cmdutil.WorkerWriteContext, error) {
	return func(ctx context.Context, targetApps ...string) (cmdutil.WorkerWriteContext, error) {
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
		// A positional App the command names is a target App too (cli#1887):
		// compared by RESOLVED id, so any spelling of the binding's own App
		// passes. The text speaks of the binding, never the other App.
		for _, target := range targetApps {
			same, err := bindingsAppIdentity(ctx, f, target, b.AppID)
			if err != nil {
				return nil, exitcode.Newf(exitcode.FromError(err), "cannot confirm the binding's App (%v); nothing was sent", err)
			}
			if !same {
				return nil, refuse("it belongs to a different App than the one this command names")
			}
		}
		// The App lookup above is a round trip that may race a rebind. The
		// returned function re-reads the binding against this snapshot right
		// before each mutation, so a swap during the lookup is caught there.
		noteEndedBinding(ctx, b)
		snapshot := *b
		// The first failed verification is latched for the rest of the run: a
		// binding that changes and is then restored must not let a later write
		// of the same batch resume (blind P2 on PR #1883).
		//
		// The mutex is held across the late App lookup on purpose: verify,
		// look up, verify again is one step per write, so two writes cannot
		// interleave a rebind check. It is command-local and used serially
		// today (a batch writes one worker at a time). If a parallel batch
		// consumer is ever added, this lock would serialize its App lookups
		// and is the thing to revisit (cli#2239 P3-5).
		var mu sync.Mutex
		var latched error
		return func(mctx context.Context, resolvedTargetApps ...string) (context.Context, error) {
			mu.Lock()
			defer mu.Unlock()
			// On every refusal the context is nil, never the person's: a caller
			// that drops the error fails closed instead of sending as them.
			if latched != nil {
				return nil, latched
			}
			verify := func() error {
				now, _, err := readBindingFile(mctx)
				if err != nil || !sameWriteBinding(now, &snapshot) {
					return exitcode.Newf(exitcode.Usage, "this worktree's session binding changed during the run, so --as-worker stopped before sending this write (session %q); nothing further was sent", snapshot.SessionID)
				}
				return nil
			}
			if latched = verify(); latched != nil {
				return nil, latched
			}
			// A worker lookup can reveal its App only after the initial
			// resolve. Compare it to this original snapshot, never a freshly
			// read session that could belong to a replacement binding.
			for _, target := range resolvedTargetApps {
				same, err := bindingsAppIdentity(mctx, f, target, snapshot.AppID)
				if err != nil {
					latched = exitcode.Newf(exitcode.FromError(err), "cannot confirm the binding's App (%v); nothing was sent", err)
					return nil, latched
				}
				if !same {
					latched = refuse("it belongs to a different App than the worker this command names")
					return nil, latched
				}
			}
			// A target identity read is itself a round trip. Re-check after
			// it too, before returning a session-bearing mutation context.
			if len(resolvedTargetApps) != 0 {
				if latched = verify(); latched != nil {
					return nil, latched
				}
			}
			return api.WithSession(mctx, snapshot.SessionID), nil
		}, nil
	}
}

// WorkerSessionIfBound is the resolver behind `secret exec` and `secret reveal`
// (cli#2664). A read client is revealed by the worker it names, in that worker's
// session, but a person on an API token must still be ASKED so the server gives
// its one uniform answer (cor:sec:060:02): so, unlike WorkerWriteSession, an
// unbound worktree (or no worktree at all) is not an error. It returns a nil
// WorkerWriteContext and nil error, and the caller sends without a session and
// says so. Once there IS a binding the strict resolver decides: an incomplete
// binding, one for another server, or another --app is refused (exit 2, nothing
// sent), and the returned function re-verifies the binding right before the call.
func WorkerSessionIfBound(f *cmdutil.Factory) func(context.Context, ...string) (cmdutil.WorkerWriteContext, error) {
	strict := WorkerWriteSession(f)
	return func(ctx context.Context, targetApps ...string) (cmdutil.WorkerWriteContext, error) {
		b, _, err := readBindingFile(ctx)
		switch {
		case errors.Is(err, errNoWorktree):
			return nil, nil
		case err != nil:
			return nil, exitcode.Newf(exitcode.Usage, "cannot read this worktree's session binding (%v), so the reveal cannot tell whether it is a worker's; nothing was sent", err)
		case b == nil:
			return nil, nil
		}
		return strict(ctx, targetApps...)
	}
}
