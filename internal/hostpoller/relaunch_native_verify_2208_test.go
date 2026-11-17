//go:build linux

package hostpoller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

type differentPinnedIdentity2208 struct{ ProcessHandle }

func (h differentPinnedIdentity2208) Identity() ProcessIdentity {
	got := h.ProcessHandle.Identity()
	got.StartTime += "-different"
	return got
}

// The kernel handle belongs only to this private test process. Terminal list
// and close are in-memory replies: no real Orca tab or agent is launched.
func TestCleanupUsesNativeVerifier2208(t *testing.T) {
	for _, tc := range []struct {
		name                                                                      string
		wrongPID, extraPID, unproven, probeError, done, unclosed, changedIdentity bool
		wantClose                                                                 int
	}{
		{name: "native pinned process positive", wantClose: 1},
		{name: "wrong PID", wrongPID: true},
		{name: "extra match", extraPID: true},
		{name: "unproven", unproven: true},
		{name: "probe error", probeError: true},
		{name: "agent exited", done: true},
		{name: "agent close unconfirmed", unclosed: true},
		{name: "pinned identity differs", changedIdentity: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _ := testPoller(t)
			handle, err := (LinuxProcessControl{}).Observe(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(handle.Close)
			pinned := handle
			if tc.changedIdentity {
				pinned = differentPinnedIdentity2208{handle}
			}
			work := t.TempDir()
			rows := map[string]any{"ok": true, "result": map[string]any{"terminals": []map[string]any{{"handle": "new", "tabId": "new-tab", "worktreePath": work, "agentIdentity": "claude"}, {"handle": "old", "tabId": "old-tab", "worktreePath": work, "connected": false}}}}
			raw, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			closes, verifies := 0, 0
			agent := &OrcaAgent{pid: os.Getpid(), handle: "new", h: pinned, done: make(chan struct{}), unclosed: tc.unclosed, l: OrcaLauncher{Run: func(_ context.Context, argv []string) (int, string, string) {
				if len(argv) > 1 && argv[1] == "close" {
					closes++
					return 0, `{"ok":true,"result":{}}`, ""
				}
				return 0, string(raw), ""
			}}}
			if tc.done {
				close(agent.done)
			}
			pids := []int{os.Getpid()}
			if tc.wrongPID {
				pids = []int{-1}
			}
			if tc.extraPID {
				pids = append(pids, os.Getpid())
			}
			p.cleanupRelaunchTerminalsWith(context.Background(), agent, hostrelaunch.Target{Worktree: work}, func(context.Context, hostrelaunch.Target) (hostrelaunch.Observation, error) {
				var err error
				if tc.probeError {
					err = errors.New("synthetic census refusal")
				}
				return hostrelaunch.Observation{Proven: !tc.unproven, PIDs: pids}, err
			}, func(ctx context.Context, a Agent, target hostrelaunch.Target, proof hostrelaunch.Observation) error {
				verifies++
				return verifyRelaunchProcess(ctx, a, target, proof)
			})
			if closes != tc.wantClose {
				t.Fatalf("native verifier boundary: closes=%d want %d", closes, tc.wantClose)
			}
			wantVerify := 1
			if tc.unproven || tc.probeError {
				wantVerify = 0
			}
			if verifies != wantVerify {
				t.Fatalf("native verifies=%d want %d", verifies, wantVerify)
			}
		})
	}
}
