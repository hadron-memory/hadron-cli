//go:build unix

package hostpoller

import (
	"fmt"
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
