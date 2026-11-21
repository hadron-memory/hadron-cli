//go:build !windows

package hostpoller

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The census finds only processes working IN the worktree: cwd at the root
// or under it, never a sibling. A process elsewhere is another worktree's
// business — stopping it from this worktree's sweep would be the wrong tree.
func TestScanWorktreeAgentsScopesOnCwd(t *testing.T) {
	requireProc(t, procRoot)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	bin := filepath.Join(root, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawn := func(dir string) *exec.Cmd {
		c := exec.Command(bin)
		c.Dir = dir
		startFixture2078(t, c)
		time.Sleep(50 * time.Millisecond)
		return c
	}
	inside := spawn(sub)
	spawn(outside)
	found := ScanWorktreeAgents(root)
	if len(found) != 1 || found[0].PID != inside.Process.Pid || found[0].Base != "claude" || found[0].Supervised {
		t.Fatalf("census = %+v, want exactly the process in %s", found, sub)
	}
	if found[0].StartTicks == 0 {
		t.Fatal("census lost the start-time ticks the stop must re-verify")
	}
}

// hadron-cli#2027 — the duplicate census's identification rules. agentBase is
// the closed set: each row names an identification the sweep must NOT make
// (the wrapper counted as its child's agent, a "-c" command line counted as
// an invocation, a passing mention counted as a process).

func TestAgentBaseIdentifiesOnlyAgents(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"direct claude", []string{"/usr/local/bin/claude", "--model", "glm"}, "claude"},
		{"direct codex", []string{"codex"}, "codex"},
		{"shebang shim names the agent", []string{"/bin/sh", "/home/u/bin/claude"}, "claude"},
		{"bash shim", []string{"/bin/bash", "/opt/codex"}, "codex"},
		{"the supervisor wrapper is never an agent", []string{"/bin/sh", "/home/u/bin/worker-supervise", "/home/u/bin/claude"}, ""},
		{"direct supervisor is never an agent", []string{"/usr/local/bin/worker-supervise", "--family", "codex"}, ""},
		{"-c is not an invocation", []string{"/bin/sh", "-c", "claude --help"}, ""},
		{"a passing mention is not a process", []string{"/bin/sh", "/home/u/bin/worker-supervise", "--print", "claude"}, ""},
		{"an unrelated script", []string{"/bin/sh", "/home/u/bin/tui"}, ""},
		{"an unrelated binary", []string{"/bin/cat", "/home/u/bin/claude"}, ""},
		{"empty", nil, ""},
	} {
		if got := agentBase(tc.argv); got != tc.want {
			t.Errorf("%s: agentBase(%q) = %q, want %q", tc.name, tc.argv, got, tc.want)
		}
	}
}

// procStat reads ppid and start ticks after the LAST ')' — comm can hold
// spaces and parens, and miscounting it names the wrong parent (a supervised
// agent read as unsupervised gets stopped).
func TestProcStatCountsAfterTheLastParen(t *testing.T) {
	fields := make([]string, 44)
	for i := range fields {
		fields[i] = "0"
	}
	fields[1] = "4242" // ppid, stat field 4
	fields[19] = "987" // start ticks, stat field 22
	stat := "1234 (a name (with parens)) " + strings.Join(fields, " ")
	ppid, start, ok := parseStatLine(stat)
	if !ok || ppid != 4242 || start != 987 {
		t.Fatalf("ppid=%d start=%d ok=%v", ppid, start, ok)
	}
	if _, _, ok := parseStatLine("1234 (short) S 1"); ok {
		t.Fatal("a truncated stat line must not parse")
	}
}

// hadron-cli#2027 review (P1): one logical agent can run as a CHAIN — the
// supervisor wrapper launches a shim, the shim runs the real binary as a
// child. Counting the chain's members as separate agents made the REAL agent
// (the child, whose parent is the shim, not the wrapper) a stop subject of a
// perfectly healthy worker. The census must answer ONE supervised agent.
//
// The leaf writes a ready file once it runs (#2037 N1): the whole chain is
// provably up before the ONE scan, so a broken collapse cannot hide behind a
// first poll that sees only the top process. Both the shim and the leaf carry
// matched basenames, so the raw census really holds two members to merge.
func TestScanWorktreeAgentsCollapsesAChainToOneSupervisedAgent(t *testing.T) {
	requireProc(t, procRoot)
	root := t.TempDir()
	ready := filepath.Join(root, "leaf-ready")
	shim := filepath.Join(root, "claude") // interpreter-wrapped match
	child := filepath.Join(root, "codex") // interpreter-wrapped match
	if err := os.WriteFile(child, []byte("#!/bin/sh\ntouch \""+ready+"\"\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n\""+child+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(root, "worker-supervise")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\n\""+shim+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(wrap)
	c.Dir = root
	startFixture2078(t, c)
	for i := 0; i < 500 && !chainReady(ready); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !chainReady(ready) {
		t.Fatal("the leaf never ran; the chain is not up")
	}
	found := ScanWorktreeAgents(root)
	if len(found) != 1 {
		t.Fatalf("one scan after the chain is up counted %d agents: %+v", len(found), found)
	}
	if !found[0].Supervised {
		t.Fatalf("the chain's agent must be supervised through its shim: %+v", found[0])
	}
	if found[0].Base != "claude" || !strings.Contains(found[0].Display, string(os.PathSeparator)+"claude") {
		t.Fatalf("the logical agent is the chain's oldest census member (the shim): %+v", found[0])
	}
}

// chainReady reports whether a leaf's ready file exists (#2037 N1).
func chainReady(ready string) bool {
	_, err := os.Stat(ready)
	return err == nil
}

// An agent's own nested one-shot (behind a tool shell that stays in the
// worktree) is the SAME logical agent, not a duplicate: without the merge the
// worker's own tooling would be stopped as an unsupervised second agent.
// The ready file waits out the spawn race (#2037 N1) so the single scan sees
// the whole three-process chain.
func TestScanWorktreeAgentsMergesANestedOneShotIntoItsAgent(t *testing.T) {
	requireProc(t, procRoot)
	root := t.TempDir()
	ready := filepath.Join(root, "leaf-ready")
	// The main agent runs its own tool shell; the tool shell runs a nested
	// one-shot as ITS child — one logical agent, three processes.
	nested := filepath.Join(root, "codex")
	if err := os.WriteFile(nested, []byte("#!/bin/sh\ntouch \""+ready+"\"\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "tool-shell")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n\""+nested+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "claude")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n\""+tool+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(agent)
	c.Dir = root
	startFixture2078(t, c)
	for i := 0; i < 500 && !chainReady(ready); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !chainReady(ready) {
		t.Fatal("the nested one-shot never ran; the chain is not up")
	}
	found := ScanWorktreeAgents(root)
	if len(found) != 1 {
		t.Fatalf("one scan after the chain is up counted %d agents: %+v", len(found), found)
	}
	if found[0].Base != "claude" {
		t.Fatalf("the logical agent is the chain's oldest member: %+v", found[0])
	}
	if found[0].Supervised {
		t.Fatalf("no worker-supervise anywhere in this chain: %+v", found[0])
	}
}

// The collapse itself, deterministically and with no live processes (#2037
// N1): every link of the chain is a census member, so the survivor walk never
// reads /proc. The chain's descendants must merge into its oldest member and
// a same-root stray must survive untouched.
func TestCollapseChainsMergesDescendantsIntoTheOldestMember(t *testing.T) {
	root := t.TempDir()
	// Large synthetic pids: 10-40 are real kernel-thread pids on Linux, so a
	// regression that made the survivor walk consult /proc could resolve them
	// by accident and mask itself (#2055 item 5). No live process is involved.
	found := []rawAgent{
		{agent: WorktreeAgent{PID: 410259, Base: "codex", Display: "/x/codex"}, ppid: 410247},
		{agent: WorktreeAgent{PID: 410247, Base: "claude", Display: "/x/claude"}, ppid: 410234},
		{agent: WorktreeAgent{PID: 410260, Base: "codex", Display: "/y/codex"}, ppid: 1},
		{agent: WorktreeAgent{PID: 410234, Base: "claude", Display: "/x/claude"}, ppid: 1},
	}
	got := collapseChains(found, root)
	if len(got) != 2 {
		t.Fatalf("collapse answered %d logical agents for one chain and one stray: %+v", len(got), got)
	}
	if got[0].PID != 410234 || got[0].Base != "claude" {
		t.Fatalf("the chain's oldest census member survives, not its descendant: %+v", got[0])
	}
	if got[1].PID != 410260 || got[1].Base != "codex" {
		t.Fatalf("a stray with its own root is not part of the chain: %+v", got[1])
	}
	if got[0].PID > got[1].PID {
		t.Fatalf("survivors are sorted by pid: %+v", got)
	}
}

// A scan run from inside an agent's own tree never reports that agent (#2037
// N1): the fake "claude" below is a census match by name, but it is the
// caller's ancestor, so ownAncestry must exclude it — a hand-run sweep inside
// a live agent must not find its own parent as an unsupervised duplicate.
// The scan runs in a re-executed test binary under the fake agent, so the
// exclusion is exercised against real parentage, not a synthetic set.
func TestScanWorktreeAgentsExcludesOwnAncestry(t *testing.T) {
	requireProc(t, procRoot)
	if os.Getenv("DUPES_OWN_ANCESTRY_CHILD") == "1" {
		var out struct {
			Found []WorktreeAgent `json:"found"`
		}
		out.Found = ScanWorktreeAgents(os.Getenv("DUPES_OWN_ANCESTRY_ROOT"))
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("DUPES_OWN_ANCESTRY_OUT"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	result := filepath.Join(root, "scan.json")
	// Control (#2055 item 2): a non-ancestor census match that MUST be
	// reported. Without it this test passes vacuously — a census-breaking
	// mutant that answers nothing also reports no ancestor. The sibling is a
	// direct child of the test process, so it cannot be on the scan child's
	// parent chain; its ready file hands the spawn race to a handshake
	// instead of a sleep.
	siblingReady := filepath.Join(root, "sibling-ready")
	sibling := filepath.Join(root, "codex")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\ntouch \""+siblingReady+"\"\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc := exec.Command(sibling)
	sc.Dir = root
	startFixture2078(t, sc)
	for i := 0; i < 500 && !chainReady(siblingReady); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !chainReady(siblingReady) {
		t.Fatal("the sibling agent never ran; the control fixture is not up")
	}
	fake := filepath.Join(root, "claude") // a census match BY NAME
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n\"" + bin + "\" -test.run=TestScanWorktreeAgentsExcludesOwnAncestry -test.timeout=30s\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(fake)
	c.Dir = root
	c.Env = append(os.Environ(),
		"DUPES_OWN_ANCESTRY_CHILD=1",
		"DUPES_OWN_ANCESTRY_ROOT="+root,
		"DUPES_OWN_ANCESTRY_OUT="+result,
	)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("scan child failed: %v\n%s", err, out)
	}
	b, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the scan child wrote no result: %v", err)
	}
	var got struct {
		Found []WorktreeAgent `json:"found"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("bad result %q: %v", b, err)
	}
	for _, f := range got.Found {
		if f.Base == "claude" {
			t.Fatalf("the caller's own agent ancestor was reported as a duplicate: %+v", got.Found)
		}
	}
	reported := false
	for _, f := range got.Found {
		if f.Base == "codex" {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the non-ancestor sibling was not reported; the census answered nothing and the exclusion passed vacuously: %+v", got.Found)
	}
}
