//go:build !windows

package hostpoller

import (
	"path/filepath"
	"testing"
)

// cli#2059: the supervision walk, pinned with no live process. A synthetic
// process table stands in for /proc: pid -> parent, start ticks, and whether
// it works inside the worktree. The pids are large and synthetic so a walk
// that fell through to the real /proc could not resolve them by accident.
type fakeProcs map[int]struct {
	ppid  int
	start int64
	in    bool
}

func (f fakeProcs) install(t *testing.T) {
	t.Helper()
	oldStat, oldIn := statOf, inRootOf
	statOf = func(pid int) (int, int64, bool) {
		p, ok := f[pid]
		return p.ppid, p.start, ok
	}
	inRootOf = func(pid int, _ string) bool { return f[pid].in }
	t.Cleanup(func() { statOf, inRootOf = oldStat, oldIn })
}

func TestSupervisedChainWalksAnyAncestorToTheBoundary2059(t *testing.T) {
	const agent, shim, shell, outside = 710004, 710003, 710002, 710001
	procs := fakeProcs{
		agent:   {ppid: shim, start: 500, in: true},
		shim:    {ppid: shell, start: 400, in: true},
		shell:   {ppid: outside, start: 300, in: true},
		outside: {ppid: 1, start: 200, in: false}, // a terminal emulator above the worktree
	}
	procs.install(t)
	root := t.TempDir()
	for _, tc := range []struct {
		name    string
		anchors []LaunchAnchor
		want    bool
	}{
		{"the agent is itself the recorded launch", []LaunchAnchor{{agent, 500}}, true},
		{"the parent shim is the launch", []LaunchAnchor{{shim, 400}}, true},
		{"the terminal shell several layers up is the launch", []LaunchAnchor{{shell, 300}}, true},
		{"a launch past the worktree boundary is nobody's", []LaunchAnchor{{outside, 200}}, false},
		{"a reused pid (start differs) is not the launch", []LaunchAnchor{{shell, 301}}, false},
		{"a record with no start never matches", []LaunchAnchor{{shell, 0}}, false},
		{"a pid that is not in the chain", []LaunchAnchor{{710099, 300}}, false},
		{"no record at all", nil, false},
		{"a stale record beside the right one still finds it", []LaunchAnchor{{710099, 1}, {shell, 300}}, true},
	} {
		if got := supervisedChain(agent, root, tc.anchors); got != tc.want {
			t.Errorf("%s: supervisedChain = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A pid cycle (a reuse race) must cost one bounded walk, never a hang, and
// answers unsupervised when no recorded launch is on it.
func TestSupervisedChainEndsOnACycle2059(t *testing.T) {
	procs := fakeProcs{
		710010: {ppid: 710011, start: 1, in: true},
		710011: {ppid: 710010, start: 2, in: true},
	}
	procs.install(t)
	if supervisedChain(710010, t.TempDir(), []LaunchAnchor{{710099, 3}}) {
		t.Fatal("a cycle with no recorded launch must read unsupervised")
	}
}

// LaunchAnchorsAt is the state's record of a worker's launch, as the walk's
// input: by worktree, with the start as a tick count and an unusable start
// kept as an anchor that cannot match.
func TestLaunchAnchorsAtReadsTheRecordedLaunches2059(t *testing.T) {
	root := t.TempDir()
	st := &State{Agents: map[string]AgentRecord{
		"a": {Worktree: "wt-a", PID: 101, Start: "777"},
		"b": {Worktree: "wt-b", PID: 202, Start: "888"},
		"c": {Worktree: "wt-a", PID: 303, Start: ""},
		"d": {Worktree: "wt-a", PID: 404, Start: "not-ticks"},
		"e": {Worktree: "wt-a", PID: 0, Start: "5"},
	}}
	got := map[int]int64{}
	for _, a := range st.LaunchAnchorsAt(root, filepath.Join(root, "wt-a")) {
		got[a.PID] = a.StartTicks
	}
	want := map[int]int64{101: 777, 303: 0, 404: 0}
	// A path spelled with a trailing slash or a dot segment is the same worktree.
	if len(st.LaunchAnchorsAt(root+"/", filepath.Join(root, "wt-a")+"/./")) != 3 {
		t.Fatal("anchors must match a worktree path however it is spelled")
	}
	if len(got) != len(want) {
		t.Fatalf("anchors = %v, want %v (wt-b is another worktree, pid 0 is no launch)", got, want)
	}
	for pid, ticks := range want {
		if g, ok := got[pid]; !ok || g != ticks {
			t.Fatalf("anchors = %v, want %v", got, want)
		}
	}
}

// A record with no start, against a process whose start also reads 0, is still
// not a match: 0 equals 0 would otherwise prove a launch nobody observed.
func TestSupervisedChainNeverMatchesAnUnobservedStart2059(t *testing.T) {
	const agent = 710020
	fakeProcs{agent: {ppid: 1, start: 0, in: true}}.install(t)
	if supervisedChain(agent, t.TempDir(), []LaunchAnchor{{agent, 0}}) {
		t.Fatal("a launch recorded with no start matched a process reading start 0")
	}
}
