package hostpoller

import (
	"errors"
	"strings"
	"testing"
)

// hadron-cli#2057 C2b: the reset is part of worker.start (G6), behind
// reset_on_start (default off), and decides before anything is launched.

func withReset(p *Poller) {
	w := p.cfg.Workers["iris"]
	w.ResetOnStart = true
	p.cfg.Workers["iris"] = w
}

func TestWorkerStartDoesNotResetByDefault(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if len(host.resets) != 0 {
		t.Fatalf("reset_on_start unset must keep today's start untouched: %v", host.resets)
	}
	if len(host.launches) != 1 {
		t.Fatalf("launches = %d", len(host.launches))
	}
}

func TestWorkerStartResetsTheTreeBeforeLaunching(t *testing.T) {
	p, srv, host := testPoller(t)
	withReset(p)
	var order []string
	host.resetOrder = func(s string) { order = append(order, s) }
	host.resetOut = ResetOutcome{Reset: true, Base: "origin/main (abc1234)"}
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if strings.Join(order, ",") != "reset,launch" {
		t.Fatalf("order = %v: the tree is reset BEFORE the agent is launched", order)
	}
	if len(host.resets) != 1 || host.resets[0] != p.cfg.WorktreePath("repo/iris") {
		t.Fatalf("resets = %v", host.resets)
	}
	if r := only(t, srv); r.outcome != Succeeded {
		t.Fatalf("%+v", r)
	}
}

func TestWorkerStartRefusedResetLaunchesNothingAndSaysWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		out  ResetOutcome
		want string
	}{
		"dirty":    {ResetOutcome{Code: CodeWorktreeDirty, Detail: "the worktree has uncommitted or untracked changes; nothing was moved"}, CodeWorktreeDirty},
		"unpushed": {ResetOutcome{Code: CodeWorktreeUnpushed, Detail: "HEAD is on no remote branch", Rescued: []string{"refs/hadron/rescue/x/work abc123"}}, CodeWorktreeUnpushed},
		"failed":   {ResetOutcome{Code: CodeResetFailed, Detail: "git fetch origin failed"}, CodeResetFailed},
	} {
		p, srv, host := testPoller(t)
		withReset(p)
		host.resetOut = tc.out
		srv.notDone["s1"] = 1
		srv.batches = [][]Operation{{startOp()}}
		step(t, p)
		r := only(t, srv)
		if r.outcome != Failed || !strings.HasPrefix(r.reason, tc.want+":") || !strings.Contains(r.reason, "nothing was launched") {
			t.Errorf("%s: %+v", name, r)
		}
		if len(host.launches) != 0 {
			t.Errorf("%s: a refused reset launched an agent", name)
		}
		if len(tc.out.Rescued) > 0 && !strings.Contains(r.reason, tc.out.Rescued[0]) {
			t.Errorf("%s: the rescue refs must be named in the report: %q", name, r.reason)
		}
	}
}

func TestWorkerStartResetErrorIsAFailureNotAGuess(t *testing.T) {
	p, srv, host := testPoller(t)
	withReset(p)
	host.resetErr = errors.New("git status: permission denied")
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if r := only(t, srv); r.outcome != Failed || !strings.HasPrefix(r.reason, CodeResetFailed) {
		t.Fatalf("%+v", r)
	}
	if len(host.launches) != 0 {
		t.Fatal("an unreadable tree must not start")
	}
}

func TestWorkerStartWithoutGitStartsWithNoReset(t *testing.T) {
	p, srv, host := testPoller(t)
	withReset(p)
	host.resetOut = ResetOutcome{} // not a Git work tree: nothing done, nothing refused
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if len(host.launches) != 1 {
		t.Fatalf("a worker without Git must start: launches = %d", len(host.launches))
	}
	if r := only(t, srv); r.outcome != Succeeded {
		t.Fatalf("%+v", r)
	}
}

// Nothing is moved under a process that works in the tree: the reset comes
// after the occupancy checks.
func TestWorkerStartDoesNotResetATreeAProcessWorksIn(t *testing.T) {
	p, srv, host := testPoller(t)
	withReset(p)
	host.occupant = "pid 77 (claude)"
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	if len(host.resets) != 0 || len(host.launches) != 0 {
		t.Fatalf("resets=%v launches=%d", host.resets, len(host.launches))
	}
	if r := only(t, srv); r.outcome != Failed || !strings.Contains(r.reason, "already working") {
		t.Fatalf("%+v", r)
	}
}

// A start that was already bound (a re-delivered operation) is never reset: it
// would move the tree under a running session.
func TestWorkerStartAlreadyBoundIsNotReset(t *testing.T) {
	p, srv, host := testPoller(t)
	withReset(p)
	srv.batches = [][]Operation{{startOp()}} // the first ask answers SUCCEEDED: already bound
	step(t, p)
	if len(host.resets) != 0 || len(host.launches) != 0 {
		t.Fatalf("resets=%v launches=%d", host.resets, len(host.launches))
	}
}

// Jill's local P3: an Inspect error is not "does not exist". An untrusted repo
// config says what it is, and nothing is launched.
func TestWorkerStartReportsAnInspectErrorAsItIs(t *testing.T) {
	p, srv, host := testPoller(t)
	host.inspectErr = &UntrustedConfigError{Why: "the repository's own config sets filter.evil.smudge, which is not on the allowlist"}
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	r := only(t, srv)
	if r.outcome != Failed || !strings.Contains(r.reason, "untrusted repo config") || strings.Contains(r.reason, "does not exist") {
		t.Fatalf("%+v", r)
	}
	if strings.HasPrefix(r.reason, CodeResetFailed) {
		t.Fatalf("a worker that does not reset on start is not a reset refusal: %q", r.reason)
	}
	if len(host.launches) != 0 {
		t.Fatal("nothing may launch on a tree that could not be inspected")
	}
}

// Chip (1606a5d7): a worker that resets on start and whose tree cannot be inspected
// is refused like every other reset refusal: the failure reason leads with
// WORKTREE_RESET_FAILED and names the error, and nothing launches.
func TestWorkerStartResetRefusalOnAnInspectErrorLeadsWithItsCode(t *testing.T) {
	p, srv, host := testPoller(t)
	withReset(p)
	host.inspectErr = errors.New("could not examine x/.git: permission denied")
	srv.notDone["s1"] = 1
	srv.batches = [][]Operation{{startOp()}}
	step(t, p)
	r := only(t, srv)
	if r.outcome != Failed || !strings.HasPrefix(r.reason, CodeResetFailed+": ") || !strings.Contains(r.reason, "permission denied") || !strings.Contains(r.reason, "nothing was launched") {
		t.Fatalf("%+v", r)
	}
	if len(host.launches) != 0 || len(host.resets) != 0 {
		t.Fatalf("launches=%d resets=%v", len(host.launches), host.resets)
	}
}
