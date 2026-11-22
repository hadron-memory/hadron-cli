package team

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

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
		return exitcode.Newf(exitcode.Usage, "every worker launches through Orca (launch_via: orca, the default), which needs Linux (the launched agent is pinned by pidfd); this host cannot run workers")
	}
	run := orcaterm.LaunchGuard(orcaterm.Exec(cfg.OrcaLaunch.OrcaBin, orcaLaunchCallTimeout))
	l := hostpoller.OrcaLauncher{
		Run: func(ctx context.Context, argv []string) (int, string, string) {
			r := run(ctx, argv)
			return r.Code, r.Stdout, r.Stderr
		},
		StateDir: filepath.Join(filepath.Dir(cfg.StateFile), "runs"),
		Proc:     hostpoller.LinuxProcessControl{},
	}
	p.EnableOrcaLaunch(l.LaunchFunc(), hostpoller.ConfigProfiles{Cfg: cfg, Get: p.Current})
	return nil
}
