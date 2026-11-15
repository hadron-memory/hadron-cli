//go:build linux

package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// Rodolfo F1: this real ExecHost process has no controlling tty. An unrelated
// idle screen at its worktree is not evidence that this process is idle.
func TestResetIdleProofRefusesUnboundNoTTY2074(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\nexec -a claude /bin/sleep 300\n"), 0755); err != nil {
		t.Fatal(err)
	}
	agent, err := (hostpoller.ExecHost{TerminateGrace: 50 * time.Millisecond}).Launch(context.Background(), hostpoller.LaunchSpec{Argv: []string{bin}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = agent.Terminate() })
	h, err := (hostpoller.LinuxProcessControl{}).Observe(agent.PID())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", agent.PID()))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(string(raw)[strings.LastIndexByte(string(raw), ')')+1:])
	if len(parts) < 5 || parts[4] != "0" {
		t.Fatal("fixture has a controlling tty")
	}
	target := hostpoller.ResetIdleTarget{WorkerRef: "worker:probe", WorkerID: "w-private", SessionID: "s-private-2074", Worktree: dir, Family: "claude", Process: h.Identity()}
	if err := hostpoller.ValidateResetTarget(target); err != nil {
		t.Fatalf("real process identity is not a valid control: %v", err)
	}
	family := "claude"
	rule := "────────────────────────────────────────────────────────────"
	reader := &resetReader{rows: []orcaterm.Terminal{{Handle: "unrelated-terminal", WorktreePath: dir, AgentIdentity: &family, Connected: true, Writable: true}}, screen: &orcaterm.Screen{Source: "screen", Tail: []string{"⏺ Done.", "✻ Cogitated for 1m 3s", rule, "❯ ", rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"}}}
	if got := orcaterm.Classify(reader.rows[0], reader.screen, time.Now()); got.Status != terminalstatus.Idle {
		t.Fatal("unrelated screen is not the intended idle control")
	}
	binding := func(string) delivery.Binding {
		return delivery.Binding{WorkerID: target.WorkerID, SessionID: target.SessionID}
	}
	if err := resetIdleProof(reader, binding, hostpoller.ValidateResetTarget)(context.Background(), target); err == nil {
		t.Fatal("non-tty target accepted an unrelated idle screen")
	}
}
