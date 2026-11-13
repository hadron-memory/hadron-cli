package delivery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeRecordPID(t *testing.T, dir, handle string, pid int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".hadron-team"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"handle":"` + handle + `","wrapperPid":` + itoa(pid) + `,"writtenAt":"2026-11-13T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, ".hadron-team", "terminal.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// foreignClaim registers a second worker (Ruin) whose record names term_p.
func foreignClaim(t *testing.T, p *plainHarness, pid int) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "ruin-plain")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecordPID(t, other, "term_p", pid)
	p.d.Registered = func() Registered { return Registered{"Joshua": {p.plain}, "Ruin": {other}} }
}

// Check (b): a foreign claim counts only while its wrapper may be alive.
func TestPlainDirCheckB_LiveForeignClaimHolds(t *testing.T) {
	p := newPlainHarness(t)
	foreignClaim(t, p, 4242)
	p.d.ProcessGone = func(int) bool { return false }
	p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
	p.wantNoSend()
}

func TestPlainDirCheckB_DeadForeignClaimDoesNotBlock(t *testing.T) {
	p := newPlainHarness(t)
	foreignClaim(t, p, 4242)
	var asked []int
	p.d.ProcessGone = func(pid int) bool { asked = append(asked, pid); return true }
	o := p.deliver(p.batch())
	if o.Kind != Delivered || o.Terminal != "term_p" {
		t.Fatalf("outcome %+v, want delivered (a dead wrapper's file is not a claim)", o)
	}
	if len(asked) == 0 || asked[0] != 4242 {
		t.Fatalf("liveness asked about %v, want the record's wrapperPid", asked)
	}
}

// A missing, zero or negative pid is unknown, and unknown counts as live.
func TestPlainDirCheckB_UnknownPIDCountsAsLive(t *testing.T) {
	for name, pid := range map[string]int{"zero": 0, "negative": -7} {
		t.Run(name, func(t *testing.T) {
			p := newPlainHarness(t)
			foreignClaim(t, p, pid)
			p.d.ProcessGone = func(int) bool { return true } // must not even be asked
			p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
			p.wantNoSend()
		})
	}
	t.Run("absent", func(t *testing.T) {
		p := newPlainHarness(t)
		foreignClaim(t, p, 1)
		other := ""
		for _, dirs := range p.d.Registered() {
			for _, d := range dirs {
				if d != p.plain {
					other = d
				}
			}
		}
		if err := os.WriteFile(filepath.Join(other, ".hadron-team", "terminal.json"), []byte(`{"handle":"term_p"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		p.d.ProcessGone = func(int) bool { return true }
		p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
	})
}

// The recipient's own record is never discounted by liveness.
func TestPlainDirOwnRecordIgnoresItsOwnPIDLiveness(t *testing.T) {
	p := newPlainHarness(t)
	writeRecordPID(t, p.plain, "term_p", 4242)
	p.d.ProcessGone = func(int) bool { return true }
	if o := p.deliver(p.batch()); o.Kind != Delivered {
		t.Fatalf("outcome %+v, want delivered", o)
	}
}

// The real test: only ESRCH is "gone". A live process, a process under
// another user (EPERM for a non-root caller) and a non-positive pid all count
// as live.
func TestClaimLiveBranchesUseTheRealProcessTest(t *testing.T) {
	d := &Deliverer{}
	if !d.claimLive(os.Getpid()) {
		t.Fatal("own pid read as dead")
	}
	if !d.claimLive(1) { // alive; EPERM for a non-root caller
		t.Fatal("pid 1 read as dead")
	}
	for _, bad := range []int{0, -1} {
		if !d.claimLive(bad) {
			t.Fatalf("pid %d read as dead", bad)
		}
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("no true(1)")
	}
	if d.claimLive(cmd.Process.Pid) {
		t.Skip("pid reused already") // the one honest race of this test
	}
}

func TestPlainDirNilRegisteredHoldsRatherThanNarrows(t *testing.T) {
	p := newPlainHarness(t)
	foreignClaim(t, p, 4242)
	p.d.Registered = nil
	p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
	p.wantNoSend()
}

// A registered source that omits the recipient is merged into, not replaced:
// the other workers' claims survive.
func TestPlainDirRegisteredMissingRecipientStillSeesOtherClaims(t *testing.T) {
	p := newPlainHarness(t)
	other := filepath.Join(t.TempDir(), "ruin-plain")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecordPID(t, other, "term_p", 4242)
	p.d.Registered = func() Registered { return Registered{"Ruin": {other}} }
	p.d.ProcessGone = func(int) bool { return false }
	p.wantHold(p.deliver(p.batch()), HoldTerminalClaimed)
	p.wantNoSend()
}

// Rook P2-1: the binding of the directory the recorded terminal sits in is
// checked too, even when that checkout is registered nowhere on this host.
func TestPlainDirTerminalInAnUnregisteredBoundCheckoutHolds(t *testing.T) {
	p := newPlainHarness(t)
	zed := filepath.Join(t.TempDir(), "zed")
	if err := os.MkdirAll(filepath.Join(zed, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.bound[zed] = "w-zed"
	p.orca.terms[0].WorktreePath = zed
	p.wantHold(p.deliver(p.batch()), HoldTerminalBoundElsewhere)
	p.wantNoSend()
}

func TestPlainDirTerminalInAnUnboundOrOwnCheckoutIsAllowed(t *testing.T) {
	p := newPlainHarness(t)
	creator := filepath.Join(t.TempDir(), "creator")
	if err := os.MkdirAll(filepath.Join(creator, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.orca.terms[0].WorktreePath = creator
	if o := p.deliver(p.batch()); o.Kind != Delivered {
		t.Fatalf("unbound creator checkout: %+v", o)
	}
}

// Gloria P3: two of one worker's plain dirs sharing a handle: every dir's
// binding is checked, not only the first.
func TestPlainDirEveryPlainDirBindingMustNameTheWorker(t *testing.T) {
	p := newPlainHarness(t)
	second := filepath.Join(t.TempDir(), "joshua-plain-2")
	writeRecord(t, second, "term_p")
	if err := os.WriteFile(filepath.Join(second, ".hadron-team", "hadron-team-session.json"), []byte(`{"workerId":"w-other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p.r.Worktrees = []string{p.plain, second}
	p.d.Bound = func(w string) string {
		if w == p.plain || w == second {
			return plainBoundWorker(w)
		}
		return p.bound[w]
	}
	p.d.Registered = func() Registered { return Registered{"Joshua": {p.plain, second}} }
	p.wantHold(p.deliver(p.batch()), HoldBindingMismatch)
	p.wantNoSend()
}

// Rook P3-3: the record cap and a dangling .git link.
func TestPlainDirOversizedRecordIsNoRecord(t *testing.T) {
	p := newPlainHarness(t)
	big := `{"handle":"term_p","pad":"` + strings.Repeat("x", maxTerminalRecord) + `"}`
	if err := os.WriteFile(filepath.Join(p.plain, ".hadron-team", "terminal.json"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	p.wantHold(p.deliver(p.batch()), HoldNoTerminalRecord)
}

func TestADanglingGitLinkIsNotAPlainDirectory(t *testing.T) {
	d := t.TempDir()
	if err := os.Symlink(filepath.Join(d, "nowhere"), filepath.Join(d, ".git")); err != nil {
		t.Skip("no symlinks")
	}
	if isPlainDir(d) {
		t.Fatal("a directory with a (dangling) .git entry was read as plain")
	}
}
