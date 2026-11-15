package hostpoller

import (
	"context"
	"strings"
	"testing"
)

// hadron-cli#2117: a context-cap reset (hadron-server#4258) names no task.

func capOp(taskID any, set bool) Operation {
	op := resetOp()
	op.Args = map[string]any{"workerId": "id-iris", "sessionId": "s1", "resetId": "r1", "cause": "context-cap"}
	if set {
		op.Args["taskId"] = taskID
	}
	return op
}

// The handler accepts a reset with no taskId (absent, null or empty) and runs
// the same protocol as a task-end reset; resetId and sessionId stay required.
func TestCapResetWithoutATaskIdIsNotMalformed2117(t *testing.T) {
	for name, op := range map[string]Operation{
		"absent": capOp(nil, false),
		"null":   capOp(nil, true),
		"empty":  capOp("", true),
	} {
		t.Run(name, func(t *testing.T) {
			x := newResetFixture(t)
			x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
			x.p.carryOut(context.Background(), op)
			// The fixture's fake server answers nothing useful, so the reset
			// ends in a refusal further on: what matters is that it was NOT
			// refused as malformed and that it reached the claim step.
			for _, r := range x.srv.finalReports() {
				if strings.Contains(r.reason, "names no taskId") || strings.Contains(r.reason, "malformed operation") {
					t.Fatalf("a cap reset was refused as malformed: %+v", r)
				}
			}
			if len(x.resets.calls) == 0 || x.resets.calls[0] != "claim" {
				t.Fatalf("the cap reset did not reach the claim step: calls %v", x.resets.calls)
			}
		})
	}
}

func TestResetStillRequiresItsIdAndSession2117(t *testing.T) {
	for _, arg := range []string{"resetId", "sessionId"} {
		t.Run(arg, func(t *testing.T) {
			x := newResetFixture(t)
			op := capOp(nil, false)
			delete(op.Args, arg)
			r := x.run(t, op)
			wantReason(t, r, "HOST_UNSUPPORTED")
			if want := "malformed operation: it names no " + arg; !strings.HasSuffix(r.reason, want) {
				t.Fatalf("reason %q, want suffix %q", r.reason, want)
			}
		})
	}
}

// Recovery of a cap reset registers no task, and the checkpoint names the cause
// the operation's args carry (hadron-server#4258) instead of an empty
// "finished task" line.
func TestCapResetRegistersNoTaskAndNamesItsCause2117(t *testing.T) {
	x := newFwdFixture(t)
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	x.p.runReset(context.Background(), capOp(nil, false), "iris", "repo/iris", GitTreeState{InGit: true, Upstream: "origin/iris/work"}, x.h, x.agent)
	x.wantCalls(t,
		"claim@0 pid=4242",
		"register@1 s1/", // no task: the recovery step is sent without one
		"authorize@1 pid=4242",
		"stopped@1 late=0s",
		"relaunched@1 pid=5151")
	if x.fr.registeredTask != "" {
		t.Fatalf("a cap reset registered task %q", x.fr.registeredTask)
	}
	c := x.fr.checkpoint.Content
	if !strings.Contains(c, "context-cap") {
		t.Fatalf("the checkpoint does not name the cause:\n%s", c)
	}
	if strings.Contains(c, "finished task") {
		t.Fatalf("a no-task checkpoint prints a finished-task line:\n%s", c)
	}
	for _, line := range strings.Split(c, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), ": ") || strings.TrimSpace(line) == "- task:" {
			t.Fatalf("an empty value line %q in:\n%s", line, c)
		}
	}
}

// A server that did not say why (no cause in the args) still never prints an
// empty line.
func TestCapResetWithoutAReportedCauseSaysSo2117(t *testing.T) {
	x := newFwdFixture(t)
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	op := capOp(nil, false)
	delete(op.Args, "cause")
	x.p.runReset(context.Background(), op, "iris", "repo/iris", GitTreeState{InGit: true, Upstream: "origin/iris/work"}, x.h, x.agent)
	c := x.fr.checkpoint.Content
	if strings.Contains(c, "finished task") || strings.Contains(c, ": \n") {
		t.Fatalf("an empty line:\n%s", c)
	}
	if !strings.Contains(c, "no task") {
		t.Fatalf("the checkpoint must say the reset names no task:\n%s", c)
	}
}

// A task-end reset is unchanged: it registers its task and prints the line.
func TestTaskEndResetIsUnchanged2117(t *testing.T) {
	x := newFwdFixture(t)
	x.run(t) // its args are unchanged: no cause
	if x.fr.registeredTask != "t1" {
		t.Fatalf("registered task %q, want t1", x.fr.registeredTask)
	}
	if !strings.Contains(x.fr.checkpoint.Content, "- finished task: t1\n") {
		t.Fatalf("the finished-task line changed:\n%s", x.fr.checkpoint.Content)
	}
}

// A task-end reset that carries cause: task-end in its args (the closed set allows
// it, with or without a task) still prints its task line and no cause line.
func TestTaskEndResetWithACauseArgPrintsItsTaskLine2117(t *testing.T) {
	x := newFwdFixture(t)
	x.p.register(context.Background()) //nolint:errcheck // fills workerRefs
	op := resetOp()
	op.Args["cause"] = "task-end"
	x.p.runReset(context.Background(), op, "iris", "repo/iris", GitTreeState{InGit: true, Upstream: "origin/iris/work"}, x.h, x.agent)
	if !strings.Contains(x.fr.checkpoint.Content, "- finished task: t1\n") || strings.Contains(x.fr.checkpoint.Content, "reset because") {
		t.Fatalf("%s", x.fr.checkpoint.Content)
	}
}
