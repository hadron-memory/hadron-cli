package team

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// AgentNamespacePreflight proves, once at startup, that this host can run an
// agent in its own unprivileged user and PID namespace; a host that forbids it
// refuses here rather than failing every start. Tests replace it.
var AgentNamespacePreflight = func() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ns := hostpoller.AgentNamespace
	return exec.CommandContext(ctx, ns[0], append(append([]string{}, ns[1:]...), "true")...).Run()
}

// orcaLaunchCallTimeout bounds one `orca terminal create|close` call.
const orcaLaunchCallTimeout = 60 * time.Second

// wireOrcaLaunch lets worker.start launch the config's launch_via: orca
// workers through Orca (hadron-cli#2058 C3). Nothing is wired when no worker
// asks for it. The per-launch environment and marker files live in a 0700
// directory beside the poller's state on the home disk, never /tmp (RAM,
// shared inode cap).
func wireOrcaLaunch(p *hostpoller.Poller, cfg *hostpoller.Config) error {
	if !cfg.UsesOrcaLaunch() {
		return nil
	}
	if runtime.GOOS != "linux" {
		return exitcode.Newf(exitcode.Usage, "launch_via: orca needs Linux (the launched agent is pinned by pidfd); remove it on this host")
	}
	if err := AgentNamespacePreflight(); err != nil {
		return exitcode.Newf(exitcode.Usage, "launch_via: orca runs each agent in its own PID namespace, and this host cannot create one (%v); nothing was started", err)
	}
	run := orcaterm.LaunchGuard(orcaterm.Exec(cfg.OrcaLaunch.OrcaBin, orcaLaunchCallTimeout))
	l := hostpoller.OrcaLauncher{
		Run: func(ctx context.Context, argv []string) (int, string, string) {
			r := run(ctx, argv)
			return r.Code, r.Stdout, r.Stderr
		},
		StateDir: filepath.Join(filepath.Dir(cfg.StateFile), "runs"),
		Proc:     hostpoller.LinuxProcessControl{},
		Group:    cfg.OrcaLaunch.Group,
	}
	p.EnableOrcaLaunch(l.LaunchFunc(), hostpoller.ConfigProfiles{Cfg: cfg})
	return nil
}
