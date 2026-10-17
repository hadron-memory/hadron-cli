package team

import (
	"context"
	"fmt"
	"sync"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// hadron-cli#914 (Holger R1/R2, decisions:2026-10-17-holger-rulings-to-vera;
// cor:dmo:070:05 for the cause). A call through a worktree binding whose
// session has ENDED warns on stderr: which session, when it ended, its closed
// cause as the server recorded it, and the supported own-session rebind. The
// call itself runs exactly as before: stdout, --json and the exit status are
// untouched, nothing ends or rebinds, and the binding file is not rewritten.
//
// Once per command, at the first point the command actually consumes the
// binding (readBinding, or an authored write attaching the session), so the
// notice lands before any refusal the call itself produces. A check that
// cannot be completed says so; it never infers an end.

// endedBindingCheck is installed once per root command (InstallEndedBindingCheck).
type endedBindingCheck struct {
	f    *cmdutil.Factory
	once sync.Once
}

var endedCheck *endedBindingCheck

// InstallEndedBindingCheck arms the notice for one root command. Each
// NewRootCmd installs a fresh one, so one command checks at most once.
func InstallEndedBindingCheck(f *cmdutil.Factory) {
	endedCheck = &endedBindingCheck{f: f}
}

// suppressEndedBindingCheck is for the commands that judge the bound session
// themselves (`session start`, `session end`, `whoami --check`), so the notice
// never doubles their own answer.
func suppressEndedBindingCheck() {
	if c := endedCheck; c != nil {
		c.once.Do(func() {})
	}
}

// noteEndedBinding runs the check for b, at most once per command.
func noteEndedBinding(ctx context.Context, b *binding) {
	c := endedCheck
	if c == nil || b == nil || b.SessionID == "" {
		return
	}
	c.once.Do(func() { c.run(ctx, b) })
}

func (c *endedBindingCheck) run(ctx context.Context, b *binding) {
	errOut := c.f.IOStreams.ErrOut
	who := b.SessionID
	if b.WorkerName != "" {
		who = fmt.Sprintf("%s (worker %s)", b.SessionID, b.WorkerName)
	}
	cannot := func(why string) {
		fmt.Fprintf(errOut, "note: could not check whether this worktree's worker session %s has ended (%s); the command runs as usual\n", who, why)
	}
	// A binding written for another server names a session id that means
	// nothing here: asking would answer for an unrelated row.
	// So does a legacy binding that recorded no server: it cannot be matched
	// to this one, so nothing is asked.
	if b.Server == "" {
		cannot("the binding records no server")
		return
	}
	server, err := c.f.Server()
	if err != nil {
		cannot("this command's server could not be resolved: " + err.Error())
		return
	}
	if server != b.Server {
		cannot("the binding is for " + b.Server + ", not this server")
		return
	}
	client, err := c.f.GraphQLClient()
	if err != nil {
		cannot(err.Error())
		return
	}
	endedAt, reason, err := boundSessionEnd(ctx, client, b.SessionID)
	if err != nil {
		cannot(api.MapError(err).Error())
		return
	}
	if endedAt == nil {
		return // open (live or lapsed): nothing to say here
	}
	rebind := b.WorkerID
	if rebind == "" {
		rebind = "<worker>"
	}
	// superseded: another session of this worker replaced it, and if that one
	// is live a plain start refuses WORKER_TAKEN (Xavier's gate, UX seat), so
	// the advice names where to look instead of promising the rebind works.
	advice := fmt.Sprintf("To work as this worker again, rebind this worktree: hadron team session start --as %s "+
		"(a plain start replaces an ended binding; no --force needed)", rebind)
	if reason == "superseded" {
		advice = fmt.Sprintf("Another session of this worker replaced it; hadron team session list --as %s --active shows it. "+
			"If that session is yours and no longer in use, hadron team session start --as %s rebinds this worktree; "+
			"if it is live, start refuses and says who is driving it", rebind, rebind)
	}
	fmt.Fprintf(errOut,
		"warning: this worktree's worker session %s ended at %s (cause: %s). This command still runs. %s\n",
		who, *endedAt, reason, advice)
}

// boundSessionEnd reads the session's end and its closed cause. An older
// server without endReason still answers whether the session ended, with the
// cause unknown: never guessed from autoExpiredAt, the summary or the binder.
func boundSessionEnd(ctx context.Context, client graphql.Client, id string) (*string, string, error) {
	resp, err := gen.GetBoundSessionEnd(ctx, client, id)
	if api.IsGraphQLValidationFor(err, "endReason") {
		legacy, lerr := gen.GetTeamSession(ctx, client, id)
		if lerr != nil {
			return nil, "", lerr
		}
		if legacy == nil || legacy.Session == nil {
			return nil, "", fmt.Errorf("the server returned no such session visible to you")
		}
		return legacy.Session.EndedAt, string(gen.SessionEndReasonUnknown), nil
	}
	if err != nil {
		return nil, "", err
	}
	if resp == nil || resp.Session == nil {
		return nil, "", fmt.Errorf("the server returned no such session visible to you")
	}
	reason := string(gen.SessionEndReasonUnknown)
	if resp.Session.EndReason != nil && *resp.Session.EndReason != "" {
		reason = string(*resp.Session.EndReason)
	}
	return resp.Session.EndedAt, reason, nil
}
