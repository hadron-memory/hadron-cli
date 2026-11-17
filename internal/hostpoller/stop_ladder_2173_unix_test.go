//go:build unix

package hostpoller

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The stop ladder, asserted on a MODELLED group (hadron-cli#2173): which signals go
// to which group, in what order, and what the report says for each state the probe
// shows. No real process and no wall-clock dependence beyond milliseconds, so a host
// saturated with runnable processes cannot change an outcome; the real-process smoke
// tests prove the same ladder against a real kernel with deadlines sized for load.
//
// The model sits on the seams every stop test already uses: signalGroup records and
// (per script) "kills" the group, probeGroup answers ESRCH once the group is gone.
// A mutant that bypasses signalGroup (a signal sent to the leader alone) records
// nothing and is red; one that drops a rung leaves the ladder short and is red.
type ladderModel struct {
	mu      sync.Mutex
	signals []string
	// diesOn is the signal after which the modelled group is gone ("" never).
	diesOn syscall.Signal
	gone   bool
}

func (m *ladderModel) install(t *testing.T) {
	t.Helper()
	realSignal, realProbe, realWait := signalGroup, probeGroup, postKillWait
	signalGroup = func(pgid int, sig syscall.Signal) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.signals = append(m.signals, fmt.Sprintf("%v->%d", sig, pgid))
		if m.diesOn != 0 && sig == m.diesOn {
			m.gone = true
		}
		return nil
	}
	probeGroup = func(int) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.gone {
			return syscall.ESRCH
		}
		return nil
	}
	postKillWait = 150 * time.Millisecond
	t.Cleanup(func() { signalGroup, probeGroup, postKillWait = realSignal, realProbe, realWait })
}

func (m *ladderModel) got() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.signals, ",")
}

func TestTheStopLadderOnAModelledGroup2173(t *testing.T) {
	const pgid = 999991
	term := fmt.Sprintf("%v->%d", syscall.SIGTERM, pgid)
	kill := fmt.Sprintf("%v->%d", syscall.SIGKILL, pgid)
	cases := []struct {
		name     string
		diesOn   syscall.Signal
		stopped  bool
		detail   string // substring
		wantSigs string
	}{
		{"TERM takes the group down", syscall.SIGTERM, true, "terminated the launched process group", term},
		{"TERM ignored, KILL takes it down", syscall.SIGKILL, true, "killed the launched process group", term + "," + kill},
		{"nothing takes it down", 0, false, "did not exit within", term + "," + kill},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &ladderModel{diesOn: c.diesOn}
			m.install(t)
			a := &execAgent{pid: pgid, grace: 60 * time.Millisecond}
			stopped, detail := a.Terminate()
			if stopped != c.stopped || !strings.Contains(detail, c.detail) {
				t.Errorf("stopped=%v %q, want stopped=%v containing %q", stopped, detail, c.stopped, c.detail)
			}
			if got := m.got(); got != c.wantSigs {
				t.Errorf("signals = %q, want exactly %q (each to the launched group, in this order)", got, c.wantSigs)
			}
		})
	}
	t.Run("an already-gone group is signalled nothing", func(t *testing.T) {
		m := &ladderModel{gone: true}
		m.install(t)
		a := &execAgent{pid: pgid, grace: 60 * time.Millisecond}
		stopped, detail := a.Terminate()
		if !stopped || detail != "" || m.got() != "" {
			t.Errorf("stopped=%v %q signals=%q, want a silent confirmed stop with zero signals", stopped, detail, m.got())
		}
	})
}

// The production signalGroup and probeGroup address the GROUP (a negative number to
// kill(2)), never the leader alone (hadron-cli#2173, §10). The modelled ladder above
// replaces signalGroup, so it cannot see a fault inside its own body; this pins the
// real bodies. The group's leader has EXITED and been reaped, so only a group-wide
// address can still reach the surviving member: a leader-only kill(pgid) answers ESRCH.
// Nothing here depends on timing: the member (sleep 600) outlives the test until the
// group signal kills it.
func TestTheDefaultGroupSignalAddressesTheGroupNotTheLeader2173(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	c := exec.Command("sh", "-c", "sleep 600 & exit 0")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := c.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	if err := c.Wait(); err != nil { // the leader is gone and reaped; the member lives
		t.Fatalf("the leader must exit cleanly: %v", err)
	}
	// Both primitives must still see the group through the surviving member.
	if err := probeGroup(pgid); err != nil {
		t.Fatalf("probeGroup must address the group (the member is alive), got %v", err)
	}
	if err := signalGroup(pgid, 0); err != nil {
		t.Fatalf("signalGroup must address the group (the member is alive), got %v", err)
	}
	// And a kill through it must take the member down: the group becomes gone.
	if err := signalGroup(pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("signalGroup(SIGKILL): %v", err)
	}
	deadline := time.Now().Add(120 * time.Second)
	for !groupGone(pgid) {
		if time.Now().After(deadline) {
			t.Fatal("the group never went after a group SIGKILL")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
