package team

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// wireResets adds the existing handler only for the operator's declaration.
// No declaration is inferred from having an implementation or an Orca path.
func wireResets(p *hostpoller.Poller, cfg *hostpoller.Config, srv *hostServer) error {
	if !cfg.ResetContextEnabled() {
		return nil
	}
	if runtime.GOOS != "linux" {
		return exitcode.Newf(exitcode.Usage, "worker.reset-context requires Linux; remove the capability on this host")
	}
	client := orcaterm.NewClient(orcaRunner(cfg.ResetContext.OrcaBin))
	p.EnableResets(srv, hostpoller.ResetDeps{
		Proc: hostpoller.LinuxProcessControl{}, Git: hostpoller.GitTree,
		Idle: resetIdleProof(client, delivery.WorktreeBinding, hostpoller.ValidateResetTarget),
	})
	return nil
}

type resetTerminalReader interface {
	List(context.Context) ([]orcaterm.Terminal, error)
	ReadScreen(context.Context, string) *orcaterm.Screen
}

// resetIdleProof returns codes only, never screen text, drafts or argv. It
// re-reads both target and terminal after classifying the screen; uncertainty
// costs a refusal. The reset protocol separately re-proves idle after claim.
func resetIdleProof(client resetTerminalReader, binding func(string) delivery.Binding, process func(hostpoller.ResetIdleTarget) error) func(context.Context, hostpoller.ResetIdleTarget) error {
	return func(ctx context.Context, target hostpoller.ResetIdleTarget) error {
		if target.WorkerID == "" || target.SessionID == "" || target.Process.PID <= 0 || target.Process.StartTime == "" || (target.Family != "claude" && target.Family != "codex") {
			return errors.New("TARGET_UNKNOWN")
		}
		root, err := filepath.EvalSymlinks(target.Worktree)
		if err != nil || !filepath.IsAbs(root) {
			return errors.New("WORKTREE_UNREADABLE")
		}
		matchesBinding := func() bool {
			b := binding(target.Worktree)
			return !b.Unusable && b.WorkerID == target.WorkerID && b.SessionID == target.SessionID
		}
		if !matchesBinding() {
			return errors.New("BINDING_CHANGED")
		}
		if err := process(target); err != nil {
			return err
		}
		readTerminal := func() (orcaterm.Terminal, error) {
			list, err := client.List(ctx)
			if err != nil {
				return orcaterm.Terminal{}, errors.New("TERMINAL_UNREADABLE")
			}
			var found []orcaterm.Terminal
			for _, t := range list {
				path, err := filepath.EvalSymlinks(t.WorktreePath)
				if err == nil && path == root && t.Identity() != "" {
					found = append(found, t)
				}
			}
			if len(found) != 1 || found[0].Handle == "" {
				return orcaterm.Terminal{}, errors.New("TERMINAL_AMBIGUOUS")
			}
			t := found[0]
			if !t.Connected || !t.Writable || t.Identity() != target.Family {
				return orcaterm.Terminal{}, errors.New("TERMINAL_CHANGED")
			}
			return t, nil
		}
		t, err := readTerminal()
		if err != nil {
			return err
		}
		obs := orcaterm.Classify(t, client.ReadScreen(ctx, t.Handle), time.Now())
		if obs.Status != terminalstatus.Idle || obs.Prompt != terminalstatus.PromptEmpty || obs.Family != target.Family {
			return errors.New("NOT_IDLE")
		}
		again, err := readTerminal()
		if err != nil {
			return err
		}
		if again.Handle != t.Handle {
			return errors.New("TERMINAL_CHANGED")
		}
		if err := process(target); err != nil {
			return err
		}
		if !matchesBinding() {
			return errors.New("BINDING_CHANGED")
		}
		return nil
	}
}
