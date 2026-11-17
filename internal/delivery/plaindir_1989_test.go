package delivery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// plainHarness is the base harness with Joshua registered at a PLAIN
// directory (no .git) whose terminal recorded handle term_p, plus a second
// worker, Mira, registered at her own worktree.
type plainHarness struct {
	*harness
	plain, miraTree string
}

func writeRecord(t *testing.T, dir, handle string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".hadron-team"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"handle":"` + handle + `","wrapperPid":1,"writtenAt":"2026-11-13T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, ".hadron-team", "terminal.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newPlainHarness(t *testing.T) *plainHarness {
	t.Helper()
	h := newHarness(t)
	root := t.TempDir()
	p := &plainHarness{harness: h, plain: filepath.Join(root, "joshua-plain"), miraTree: filepath.Join(root, "mira")}
	for _, d := range []string{p.plain, filepath.Join(p.miraTree, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRecord(t, p.plain, "term_p")
	// The plain worker's terminal reports its CREATOR's worktree, as Orca does.
	h.orca.terms = []orcaterm.Terminal{
		{Handle: "term_p", WorktreePath: filepath.Join(root, "creator"), AgentIdentity: ptr("claude"), Connected: true, Writable: true},
		{Handle: "term_m", WorktreePath: p.miraTree, AgentIdentity: ptr("claude"), Connected: true, Writable: true},
	}
	h.orca.screens = map[string][]*orcaterm.Screen{"term_p": {screen(claudeIdle)}, "term_m": {screen(claudeIdle)}}
	h.bound = map[string]string{p.plain: workerID, p.miraTree: "w-mira"}
	h.r = Recipient{WorkerID: workerID, Name: "Joshua", TUI: "claude", Worktrees: []string{p.plain}}
	h.d.Bound = func(w string) string {
		if w == p.plain { // the real default reads .hadron-team/ for a plain dir
			return plainBoundWorker(w)
		}
		return h.bound[w]
	}
	h.d.Registered = func() Registered {
		return Registered{"Joshua": {p.plain}, "Mira": {p.miraTree}}
	}
	writeBinding := func(dir, id string) {
		_ = os.MkdirAll(filepath.Join(dir, ".hadron-team"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, ".hadron-team", "hadron-team-session.json"), []byte(`{"workerId":"`+id+`"}`), 0o600)
	}
	writeBinding(p.plain, workerID)
	return p
}

func TestPlainDirWorkerIsTypedIntoItsRecordedTerminal(t *testing.T) {
	p := newPlainHarness(t)
	o := p.deliver(p.batch())
	if o.Kind != Delivered || o.Terminal != "term_p" {
		t.Fatalf("outcome %+v, want delivered to term_p", o)
	}
}

func TestPlainDirMissingRecordHolds(t *testing.T) {
	for name, setup := range map[string]func(p *plainHarness){
		"absent": func(p *plainHarness) { _ = os.Remove(filepath.Join(p.plain, ".hadron-team", "terminal.json")) },
		"malformed": func(p *plainHarness) {
			_ = os.WriteFile(filepath.Join(p.plain, ".hadron-team", "terminal.json"), []byte("{"), 0o600)
		},
		"empty": func(p *plainHarness) { writeRecord(p.t, p.plain, "") },
		"a symlink": func(p *plainHarness) {
			f := filepath.Join(p.plain, ".hadron-team", "terminal.json")
			real := filepath.Join(p.plain, "real.json")
			_ = os.Rename(f, real)
			_ = os.Symlink(real, f)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlainHarness(t)
			setup(p)
			p.wantHold(p.deliver(p.batch()), HoldNoTerminalRecord)
			p.wantNoSend()
		})
	}
}

func TestPlainDirCheckA_StaleHandleHolds(t *testing.T) {
	for name, mut := range map[string]func(t *orcaterm.Terminal){
		"not listed":   nil,
		"disconnected": func(t *orcaterm.Terminal) { t.Connected = false },
		"not writable": func(t *orcaterm.Terminal) { t.Writable = false },
		"not an agent": func(t *orcaterm.Terminal) { t.AgentIdentity = nil },
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlainHarness(t)
			if mut == nil {
				writeRecord(t, p.plain, "term_gone")
			} else {
				mut(&p.orca.terms[0])
			}
			p.wantHold(p.deliver(p.batch()), HoldTerminalStale)
			p.wantNoSend()
		})
	}
}

func TestPlainDirCheckB_AnotherWorkersRecordNamingTheHandleHolds(t *testing.T) {
	p := newPlainHarness(t)
	other := filepath.Join(t.TempDir(), "ruin-plain")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, other, "term_p")
	p.d.Registered = func() Registered {
		return Registered{"Joshua": {p.plain}, "Ruin": {other}}
	}
	p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
	p.wantNoSend()
}

// A second spelling of the same directory must not hide a claim: the maps
// are built from resolved paths.
func TestPlainDirCheckB_ClaimThroughASymlinkedSpellingHolds(t *testing.T) {
	p := newPlainHarness(t)
	other := filepath.Join(t.TempDir(), "ruin-plain")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, other, "term_p")
	link := filepath.Join(t.TempDir(), "ruin-link")
	if err := os.Symlink(other, link); err != nil {
		t.Skip("no symlinks")
	}
	p.d.Registered = func() Registered { return Registered{"Joshua": {p.plain}, "Ruin": {link}} }
	p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
	p.wantNoSend()
}

func TestPlainDirCheckC_TerminalInAnotherWorkersDirectoryHolds(t *testing.T) {
	p := newPlainHarness(t)
	// The recorded handle is a terminal sitting in Mira's worktree.
	writeRecord(t, p.plain, "term_m")
	p.wantHold(p.deliver(p.batch()), HoldTerminalBoundElsewhere)
	p.wantNoSend()
}

func TestPlainDirCheckC_UsesResolvedPathsForTheTerminalsDirectory(t *testing.T) {
	p := newPlainHarness(t)
	link := filepath.Join(t.TempDir(), "mira-link")
	if err := os.Symlink(p.miraTree, link); err != nil {
		t.Skip("no symlinks")
	}
	p.orca.terms[0].WorktreePath = link
	p.wantHold(p.deliver(p.batch()), HoldTerminalBoundElsewhere)
	p.wantNoSend()
}

func TestPlainDirBindingMismatchKeepsItsOwnHold(t *testing.T) {
	p := newPlainHarness(t)
	_ = os.WriteFile(filepath.Join(p.plain, ".hadron-team", "hadron-team-session.json"), []byte(`{"workerId":"w-someone-else"}`), 0o600)
	p.wantHold(p.deliver(p.batch()), HoldBindingMismatch)
	p.wantNoSend()
}

func TestPlainDirNoBindingHolds(t *testing.T) {
	p := newPlainHarness(t)
	_ = os.Remove(filepath.Join(p.plain, ".hadron-team", "hadron-team-session.json"))
	p.wantHold(p.deliver(p.batch()), HoldBindingMismatch)
	p.wantNoSend()
}

// .git wins: a directory with a .git is a worktree row, read from the git
// dir, and its terminal record is never consulted.
func TestGitWinsOverAPlainRecord(t *testing.T) {
	p := newPlainHarness(t)
	if err := os.MkdirAll(filepath.Join(p.plain, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The git dir holds no binding; the plain cache still says Joshua.
	if got := BoundWorker(p.plain); got != "" {
		t.Fatalf("BoundWorker read the plain cache of a checkout: %q", got)
	}
	// And the recorded terminal is not used: no terminal reports this path.
	o := p.deliver(p.batch())
	if o.Kind != Skipped || o.Reason != "no-terminal" {
		t.Fatalf("outcome %+v, want skipped no-terminal (worktree row, record ignored)", o)
	}
	p.wantNoSend()
}

func TestBoundWorkerReadsAPlainCacheOnlyWithoutGit(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, ".hadron-team"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(d, ".hadron-team", "hadron-team-session.json"), []byte(`{"workerId":"w-x"}`), 0o600)
	if got := BoundWorker(d); got != "w-x" {
		t.Fatalf("plain dir binding = %q", got)
	}
	if got := BoundWorker(filepath.Join(d, "missing")); got != "" {
		t.Fatalf("missing dir binding = %q", got)
	}
}

func TestWorktreeRowIgnoresATerminalRecord(t *testing.T) {
	h := newHarness(t)
	writeRecord(t, h.tree, "term_other") // a stray record beside a real checkout
	o := h.deliver(h.batch())
	if o.Kind != Delivered || o.Terminal != "term_j" {
		t.Fatalf("outcome %+v, want the worktree path match to deliver", o)
	}
}

// The other worker registered the directory through a symlinked spelling;
// the terminal reports the real path. Check (c) must still see the owner.
func TestPlainDirCheckC_RegisteredThroughASymlinkedSpellingHolds(t *testing.T) {
	p := newPlainHarness(t)
	link := filepath.Join(t.TempDir(), "mira-link")
	if err := os.Symlink(p.miraTree, link); err != nil {
		t.Skip("no symlinks")
	}
	writeRecord(t, p.plain, "term_m")
	p.d.Registered = func() Registered { return Registered{"Joshua": {p.plain}, "Mira": {link}} }
	p.wantHold(p.deliver(p.batch()), HoldTerminalBoundElsewhere)
	p.wantNoSend()
}
