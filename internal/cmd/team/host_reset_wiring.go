package team

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
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

// resetIdleProof refuses until C3 supplies a verifiable process/terminal
// association. ExecHost launches without a controlling tty; an Orca screen at
// the same worktree cannot prove that process idle. Current targets are all
// unbound, even when their binding/process match and another screen is idle.
// The reader is intentionally unused: no screen may be read as a substitute
// for that missing association. This callback has no successful return path.
func resetIdleProof(_ resetTerminalReader, binding func(string) delivery.Binding, process func(hostpoller.ResetIdleTarget) error) func(context.Context, hostpoller.ResetIdleTarget) error {
	return func(_ context.Context, target hostpoller.ResetIdleTarget) error {
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
		return errors.New("TARGET_UNBOUND")
	}
}
