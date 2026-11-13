package delivery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

func writeRecordPID(t *testing.T, dir, handle string, pid int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".hadron-team"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"handle":"` + handle + `","wrapperPid":` + strconv.Itoa(pid) + `,"writtenAt":"2026-11-13T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, ".hadron-team", "terminal.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
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

// writeCheckoutBinding plants a session binding (raw content) in gitDir.
func writeCheckoutBinding(t *testing.T, gitDir, content string) {
	t.Helper()
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, bindingFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// checkoutShapes builds an unregistered checkout of each kind, its binding
// file holding content ("" plants none): a plain-git directory, a linked
// worktree, and a plain (no .git) directory.
var checkoutShapes = map[string]func(t *testing.T, root, content string) string{
	"gitdir": func(t *testing.T, root, content string) string {
		t.Helper()
		dir := filepath.Join(root, "plain-git")
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			writeCheckoutBinding(t, filepath.Join(dir, ".git"), content)
		}
		return dir
	},
	"linked": func(t *testing.T, root, content string) string {
		t.Helper()
		dir := filepath.Join(root, "linked")
		gitDir := filepath.Join(root, "gitdirs", "linked")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			writeCheckoutBinding(t, gitDir, content)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	},
	"plain": func(t *testing.T, root, content string) string {
		t.Helper()
		dir := filepath.Join(root, "plain")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			writeCheckoutBinding(t, filepath.Join(dir, ".hadron-team"), content)
		}
		return dir
	},
}

// Caspian D1: the check-(c) binding guard through the PRODUCTION BoundWorker
// (no harness seam), at checkouts registered nowhere: a binding naming another
// worker holds; one naming the recipient is allowed.
func TestPlainDirTerminalCheckoutBindingThroughProductionBoundWorker(t *testing.T) {
	for name, mk := range checkoutShapes {
		t.Run(name, func(t *testing.T) {
			p := newPlainHarness(t)
			p.d.Bound = func(w string) string {
				if w == p.plain {
					return plainBoundWorker(w)
				}
				return BoundWorker(w)
			}
			p.orca.terms[0].WorktreePath = mk(t, t.TempDir(), `{"workerId":"w-other"}`)
			p.wantHold(p.deliver(p.batch()), HoldTerminalBoundElsewhere)
			p.wantNoSend()

			p.orca.terms[0].WorktreePath = mk(t, t.TempDir(), `{"workerId":"`+p.r.WorkerID+`"}`)
			if o := p.deliver(p.batch()); o.Kind != Delivered {
				t.Fatalf("own binding should deliver: %+v", o)
			}
		})
	}
}

// cli#1992 N2: only a binding file that does not exist is "absent" (admit). A
// binding that exists but is malformed, empty of a worker, or unreadable
// holds: a hold costs delay, typing into another worker's terminal breaches
// the floor.
func TestPlainDirTerminalCheckoutBindingAbsentAdmitsMalformedHolds(t *testing.T) {
	for name, mk := range checkoutShapes {
		for _, c := range []struct {
			label, content string
			hold           bool
		}{
			{"absent", "", false},
			{"malformed", `{"workerId":`, true},
			{"not-json", `not json`, true},
			{"empty-worker", `{"workerId":""}`, true},
			{"no-worker-field", `{}`, true},
		} {
			t.Run(name+"/"+c.label, func(t *testing.T) {
				p := newPlainHarness(t)
				p.d.Bound = func(w string) string {
					if w == p.plain {
						return plainBoundWorker(w)
					}
					return BoundWorker(w)
				}
				p.orca.terms[0].WorktreePath = mk(t, t.TempDir(), c.content)
				if !c.hold {
					if o := p.deliver(p.batch()); o.Kind != Delivered {
						t.Fatalf("an absent binding should admit: %+v", o)
					}
					return
				}
				p.wantHold(p.deliver(p.batch()), HoldTerminalBoundElsewhere)
				p.wantNoSend()
			})
		}
	}
}

// A binding that exists but cannot be read as a file (here a directory in
// its place) is unusable, never absent.
func TestBoundWorkerUnreadableBindingIsNotAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", bindingFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := BoundWorker(dir); got != unreadableBinding {
		t.Fatalf("BoundWorker = %q, want unreadableBinding", got)
	}
	plain := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(filepath.Join(plain, ".hadron-team", bindingFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := plainBoundWorker(plain); got != unreadableBinding {
		t.Fatalf("plainBoundWorker = %q, want unreadableBinding", got)
	}
}

// cli#1992 F3: a worker with a healthy worktree row AND a plain directory
// whose terminal record is gone is held whole: the plain directory's hold
// returns first, so nothing is typed into the healthy row either. Documented
// in the how-to; pinned here.
func TestPlainDirMixedRegistrationHoldsWholeWorker(t *testing.T) {
	p := newPlainHarness(t)
	wt := filepath.Join(t.TempDir(), "joshua-wt")
	if err := os.MkdirAll(filepath.Join(wt, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.bound[wt] = p.r.WorkerID
	p.orca.terms = append(p.orca.terms, orcaterm.Terminal{Handle: "term_w", WorktreePath: wt, AgentIdentity: ptr("claude"), Connected: true, Writable: true})
	p.orca.screens["term_w"] = []*orcaterm.Screen{screen(claudeIdle)}
	p.r.Worktrees = []string{wt, p.plain}
	p.d.Registered = func() Registered { return Registered{"Joshua": {wt, p.plain}} }
	if err := os.Remove(filepath.Join(p.plain, ".hadron-team", "terminal.json")); err != nil {
		t.Fatal(err)
	}
	p.wantHold(p.deliver(p.batch()), HoldNoTerminalRecord)
	p.wantNoSend()
}
