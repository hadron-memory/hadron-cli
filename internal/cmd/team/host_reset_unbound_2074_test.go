//go:build linux

package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	// Use a native helper that signals after its runtime has started. A
	// successful exec alone does not tell this test that its census control
	// is ready. The production validator below is still called only once.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(exe, bin); err != nil {
		image, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, image, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ready := filepath.Join(dir, "ready")
	agent, err := (hostpoller.ExecHost{TerminateGrace: 50 * time.Millisecond}).Launch(context.Background(), hostpoller.LaunchSpec{
		Argv: []string{bin, "-test.run=^TestResetNoTTYAgentFixture2074$", "-test.timeout=2m"},
		Dir:  dir, Env: []string{"PATH=/usr/bin:/bin", "HADRON_C2C_TEST_READY=" + ready},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = agent.Terminate() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(ready)
		if err == nil {
			if string(b) != strconv.Itoa(agent.PID()) {
				t.Fatal("readiness came from another process")
			}
			break
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatalf("native fixture did not become ready: %v", err)
		}
		select {
		case <-agent.Done():
			t.Fatal("native fixture exited before readiness")
		case <-time.After(10 * time.Millisecond):
		}
	}
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
		// These are post-failure diagnostics, not the validator's own
		// earlier census. Only this fixture's PID and private directory
		// are inspected; no environment or real agent data is printed.
		agents := hostpoller.ScanWorktreeAgents(dir, nil)
		pids := make([]int, 0, len(agents))
		for _, a := range agents {
			pids = append(pids, a.PID)
		}
		cwd, cwdErr := os.Readlink(fmt.Sprintf("/proc/%d/cwd", agent.PID()))
		argv, argvErr := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", agent.PID()))
		t.Fatalf("ready real process is not a valid control: %v; target=%+v; post-failure census=%d pids=%v cwd=%q (%v) argv=%q (%v)",
			err, h.Identity(), len(agents), pids, cwd, cwdErr, argv, argvErr)
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

// Subprocess-only native agent: argv[0] is claude, with no exec transition or
// descendants after readiness. The parent owns and terminates its group.
func TestResetNoTTYAgentFixture2074(t *testing.T) {
	ready := os.Getenv("HADRON_C2C_TEST_READY")
	if ready == "" {
		t.Skip("subprocess fixture")
	}
	if err := publishResetFixtureReady2074(ready, os.Getpid(), nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}

// The hook holds the exact create-to-write window from Jerry F1 open.
func TestResetFixtureReadyPublishesAtomically2098(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	exposed := false
	err := publishResetFixtureReady2074(ready, 12345, func() {
		_, err := os.Stat(ready)
		exposed = !os.IsNotExist(err)
	})
	if err != nil {
		t.Fatal(err)
	}
	if exposed {
		t.Fatal("readiness is visible before its PID is complete")
	}
	got, err := os.ReadFile(ready)
	if err != nil || string(got) != "12345" {
		t.Fatalf("published readiness %q: %v", got, err)
	}
}
func publishResetFixtureReady2074(ready string, pid int, beforeWrite func()) error {
	f, err := os.CreateTemp(filepath.Dir(ready), ".ready-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer func() { _ = f.Close(); _ = os.Remove(temp) }()
	if beforeWrite != nil {
		beforeWrite()
	}
	if _, err = f.WriteString(strconv.Itoa(pid)); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, ready)
}
