package healthfix

import (
	"strings"
	"testing"
)

// hadron-cli#2027 — the decision core's guard matrix. Every row is one rule
// of the issue's acceptance or Xabat's CHECKED gate; each names the mutant it
// kills (the sweep that would stop the wrong process, close the wrong tab, or
// act inside a guarded window).

func agents(xs ...Agent) []Agent { return xs }

func supervised(pid int) Agent {
	return Agent{PID: pid, Base: "codex", Supervised: true, StartTicks: 100}
}
func stray(pid int, b string) Agent {
	return Agent{PID: pid, Base: b, Supervised: false, StartTicks: 50}
}

func wt(a []Agent, tabs ...Tab) Worktree {
	return Worktree{WorkerID: "w1", WorkerName: "Iris", Path: "/wt", Agents: a, Tabs: tabs}
}

func applying() Mode { return Mode{Applying: true} }

func find(p Plan, action string) []Fix {
	var out []Fix
	for _, f := range p.Fixes {
		if f.Action == action {
			out = append(out, f)
		}
	}
	return out
}

// Guard: the only agent is never stopped, supervised or not.
func TestLoneAgentYieldsNothing(t *testing.T) {
	for _, a := range []Agent{supervised(1), stray(2, "claude")} {
		p := Decide(wt([]Agent{a}), applying())
		if len(p.Fixes) != 0 || len(p.Alerts) != 0 || len(p.Skips) != 0 {
			t.Fatalf("a lone agent produced a plan: %+v", p)
		}
	}
}

// Acceptance: with exactly one supervised agent, the others are stopped —
// the supervised one is never a subject.
func TestSupervisedKeptUnsupervisedStopped(t *testing.T) {
	p := Decide(wt(agents(supervised(1), stray(2, "claude"))), applying())
	fixes := find(p, StopAgent)
	if len(fixes) != 1 || fixes[0].Subject != "pid 2 (claude): " || fixes[0].Worker != "w1" {
		t.Fatalf("want one stop for pid 2: %+v", p.Fixes)
	}
	if len(p.Alerts) != 0 {
		t.Fatalf("a clean duplicate is not an alert: %+v", p.Alerts)
	}
}

// Every unsupervised duplicate is named when there are several.
func TestEveryUnsupervisedDuplicateIsNamed(t *testing.T) {
	p := Decide(wt(agents(supervised(1), stray(2, "claude"), stray(3, "codex"))), applying())
	if got := find(p, StopAgent); len(got) != 2 {
		t.Fatalf("want stops for pid 2 and pid 3: %+v", p.Fixes)
	}
}

// Acceptance: two unsupervised agents — nothing stopped, one alert.
func TestAllUnsupervisedAlertsOnly(t *testing.T) {
	p := Decide(wt(agents(stray(1, "claude"), stray(2, "codex"))), applying())
	if len(p.Fixes) != 0 || len(p.Alerts) != 1 || p.Alerts[0] != (Alert{"w1", "unsupervised-agents", 2}) {
		t.Fatalf("want one unsupervised-agents alert of 2: %+v", p)
	}
}

// Two supervised agents is an anomaly: fail closed to an alert, stop nothing —
// the sweep cannot tell which supervised one is the right one.
func TestMultipleSupervisedFailsClosed(t *testing.T) {
	p := Decide(wt(agents(supervised(1), supervised(2), stray(3, "claude"))), applying())
	if len(p.Fixes) != 0 || len(p.Alerts) != 1 || p.Alerts[0] != (Alert{"w1", "multiple-supervised", 2}) {
		t.Fatalf("want one multiple-supervised alert and no fixes: %+v", p)
	}
}

// Acceptance: an extra bare-shell tab closes only while a solid tab remains.
func TestBareTabClosesWhileASolidTabRemains(t *testing.T) {
	p := Decide(wt(nil, Tab{"t-solid", false}, Tab{"t-bare", true}), applying())
	fixes := find(p, CloseTab)
	if len(fixes) != 1 || fixes[0].Subject != "tab t-bare" {
		t.Fatalf("want one close for t-bare: %+v", p.Fixes)
	}
}

// Guard: a worktree whose every tab is bare is a no_agent report, not a
// close — emptying the worktree of tabs is relaunch work (#1947).
func TestAllBareTabsAreNeverClosed(t *testing.T) {
	p := Decide(wt(nil, Tab{"t-a", true}, Tab{"t-b", true}), applying())
	if len(p.Fixes) != 0 {
		t.Fatalf("nothing to close when no solid tab remains: %+v", p.Fixes)
	}
}

// A lone tab closes nothing, bare or not.
func TestLoneTabClosesNothing(t *testing.T) {
	p := Decide(wt(nil, Tab{"t-only", true}), applying())
	if len(p.Fixes) != 0 {
		t.Fatalf("a lone tab produced a fix: %+v", p.Fixes)
	}
}

// Gate edge 3: a resetting worker is skipped entirely — even a plain
// duplicate is left for the next sweep, so a relaunch is never stopped
// mid-window.
func TestResettingWorkerSkippedEntirely(t *testing.T) {
	w := wt(agents(supervised(1), stray(2, "claude")), Tab{"t-bare", true}, Tab{"t-solid", false})
	w.Resetting = true
	p := Decide(w, applying())
	if len(p.Fixes) != 0 || len(p.Alerts) != 0 || len(p.Skips) != 1 || p.Skips[0] != (Skip{"w1", "resetting"}) {
		t.Fatalf("a resetting worker must yield only the skip: %+v", p)
	}
}

// Gate edge 4: under a pause the plan is still reported, never applied, and
// the worker is marked so the receipt can say why.
func TestPauseReportsWithoutMarkingFixesApplied(t *testing.T) {
	p := Decide(wt(agents(supervised(1), stray(2, "claude"))), Mode{Applying: true, Paused: true})
	if len(find(p, StopAgent)) != 1 {
		t.Fatalf("the pause must still report the fix: %+v", p.Fixes)
	}
	if len(p.Skips) != 1 || p.Skips[0] != (Skip{"w1", "team-paused"}) {
		t.Fatalf("the pause must mark the worker: %+v", p.Skips)
	}
	// A worker with nothing to do earns no pause row.
	q := Decide(wt(nil), Mode{Applying: true, Paused: true})
	if len(q.Skips) != 0 {
		t.Fatalf("a quiet worktree is not marked paused: %+v", q.Skips)
	}
}

// The mode never changes the decision, only the caller's application of it.
func TestDryModeDecidesTheSame(t *testing.T) {
	dry := Decide(wt(agents(supervised(1), stray(2, "claude"))), Mode{})
	wet := Decide(wt(agents(supervised(1), stray(2, "claude"))), applying())
	if strings.Join(subjects(dry), "|") != strings.Join(subjects(wet), "|") {
		t.Fatalf("mode changed the decision: %+v vs %+v", dry, wet)
	}
}

// The --json contract: empty plans carry empty slices, not null.
func TestEmptyPlanCarriesEmptySlices(t *testing.T) {
	p := Decide(wt(nil), applying())
	if p.Fixes == nil || p.Alerts == nil || p.Skips == nil {
		t.Fatalf("nil slice in a plan: %+v", p)
	}
}

// The subject names the pid, the agent base and the redacted display line,
// in that order — it is the operator's only way to tell WHICH process.
func TestSubjectNamesPidBaseThenDisplay(t *testing.T) {
	a := stray(42, "claude")
	a.Display = "/home/u/bin/claude --model glm"
	p := Decide(wt(agents(supervised(1), a)), applying())
	if got := find(p, StopAgent)[0].Subject; got != "pid 42 (claude): /home/u/bin/claude --model glm" {
		t.Fatalf("subject %q", got)
	}
}

func subjects(p Plan) []string {
	out := []string{}
	for _, f := range p.Fixes {
		out = append(out, f.Action+" "+f.Subject)
	}
	return out
}
