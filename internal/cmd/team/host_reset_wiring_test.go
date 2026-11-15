package team

import (
	"context"
	"errors"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

type resetReader struct {
	rows           []orcaterm.Terminal
	screen         *orcaterm.Screen
	lists, screens int
}

func (r *resetReader) List(context.Context) ([]orcaterm.Terminal, error) {
	r.lists++
	return r.rows, nil
}
func (r *resetReader) ReadScreen(context.Context, string) *orcaterm.Screen {
	r.screens++
	return r.screen
}

// Current ExecHost targets have no verifiable Orca-pty association. Even a
// valid process/binding must refuse; no terminal screen is evidence of idle.
func TestResetIdleProofFailClosed2074(t *testing.T) {
	for _, name := range []string{"unbound", "unknown worker", "unknown session", "unknown pid", "unknown start", "unknown family", "missing tree", "wrong binding", "unusable binding", "changed process"} {
		t.Run(name, func(t *testing.T) {
			target := hostpoller.ResetIdleTarget{WorkerRef: "worker:probe", WorkerID: "w1", SessionID: "s1", Worktree: t.TempDir(), Family: "codex", Process: hostpoller.ProcessIdentity{PID: 42, StartTime: "100"}}
			b := delivery.Binding{WorkerID: "w1", SessionID: "s1"}
			want := "TARGET_UNBOUND"
			wantProc := 1
			switch name {
			case "unknown worker":
				target.WorkerID = ""
				want = "TARGET_UNKNOWN"
				wantProc = 0
			case "unknown session":
				target.SessionID = ""
				want = "TARGET_UNKNOWN"
				wantProc = 0
			case "unknown pid":
				target.Process.PID = 0
				want = "TARGET_UNKNOWN"
				wantProc = 0
			case "unknown start":
				target.Process.StartTime = ""
				want = "TARGET_UNKNOWN"
				wantProc = 0
			case "unknown family":
				target.Family = "unknown"
				want = "TARGET_UNKNOWN"
				wantProc = 0
			case "missing tree":
				target.Worktree += "/missing"
				want = "WORKTREE_UNREADABLE"
				wantProc = 0
			case "wrong binding":
				b.SessionID = "other"
				want = "BINDING_CHANGED"
				wantProc = 0
			case "unusable binding":
				b.Unusable = true
				want = "BINDING_CHANGED"
				wantProc = 0
			case "changed process":
				want = "PROCESS_CHANGED"
			}
			r := &resetReader{}
			calls := 0
			process := func(got hostpoller.ResetIdleTarget) error {
				calls++
				if got != target {
					t.Fatal("target changed")
				}
				if name == "changed process" {
					return errors.New("PROCESS_CHANGED")
				}
				return nil
			}
			err := resetIdleProof(r, func(string) delivery.Binding { return b }, process)(context.Background(), target)
			if err == nil || err.Error() != want {
				t.Fatalf("got %v want %s", err, want)
			}
			if calls != wantProc || r.lists != 0 || r.screens != 0 {
				t.Fatalf("unbound target used terminal evidence: process=%d list=%d screen=%d", calls, r.lists, r.screens)
			}
		})
	}
}
