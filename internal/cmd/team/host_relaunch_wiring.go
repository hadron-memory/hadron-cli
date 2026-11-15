package team

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// wireRelaunch has no independent trigger or RPC. An explicitly typed Orca
// config enables the periodic owner; the fresh server APPLY/desired proofs
// still decide every action. Exec-only hosts keep their existing poller.
func wireRelaunch(p *hostpoller.Poller, cfg *hostpoller.Config, server *hostServer) error {
	if !cfg.UsesOrcaLaunch() {
		return nil
	}
	client := orcaterm.NewClient(orcaRunner(cfg.OrcaLaunch.OrcaBin))
	return p.EnableRelaunch(hostpoller.RelaunchDependencies{
		Source: server,
		Ready: func(ctx context.Context) error {
			checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return relaunchDaemonReady(checkCtx, func(ctx context.Context) ([]byte, error) {
				return exec.CommandContext(ctx, "systemctl", "--user", "list-units", "orca-daemon-*.scope", "--state=running", "--no-legend", "--plain", "--no-pager").Output()
			}, func(ctx context.Context) error { _, err := client.List(ctx); return err })
		},
		Notice: server.ReportRelaunch,
	})
}

// relaunchDaemonReady implements the Linux-only, fail-closed C5 preflight
// (Wolfgang #108866). Scope evidence is local to the poller user and host.
// A successful serve status or an empty terminal list alone proves nothing.
func relaunchDaemonReady(ctx context.Context, scopes func(context.Context) ([]byte, error), list func(context.Context) error) error {
	raw, err := scopes(ctx)
	if err != nil {
		return fmt.Errorf("daemon scope proof unavailable: %w", err)
	}
	rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(rows) != 1 {
		return fmt.Errorf("daemon scope proof requires exactly one running scope")
	}
	fields := strings.Fields(rows[0])
	if len(fields) < 4 || !strings.HasPrefix(fields[0], "orca-daemon-") || !strings.HasSuffix(fields[0], ".scope") || fields[1] != "loaded" || fields[2] != "active" || fields[3] != "running" {
		return fmt.Errorf("daemon scope proof requires exactly one running scope")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return list(ctx)
}
