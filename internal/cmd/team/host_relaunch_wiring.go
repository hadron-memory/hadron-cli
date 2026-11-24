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
// still decide every action. Every worker launches through Orca (cli#2914), so every host wires it.
func wireRelaunch(p *hostpoller.Poller, cfg *hostpoller.Config, server *hostServer) error {
	if !cfg.UsesOrcaLaunch() {
		return nil
	}
	return p.EnableRelaunch(hostpoller.RelaunchDependencies{
		Source:       server,
		ReportSweep:  server.RecordRecoverySweep,
		Ready:        managedDaemonProbe(cfg),
		Notice:       server.ReportRelaunch,
		EndAgentless: server.EndAgentless,
	})
}

func configuredOrcaBin(cfg *hostpoller.Config) string {
	bin := cfg.OrcaLaunch.OrcaBin
	if bin == "" {
		bin = cfg.Delivery.OrcaBin
	}
	if bin == "" {
		bin = "orca"
	}
	return bin
}

// managedDaemonProbe is shared by the C5 owner and the C1 queue-only sweep.
// It checks readiness; it never creates a terminal or starts a daemon.
func managedDaemonProbe(cfg *hostpoller.Config) func(context.Context) error {
	client := orcaterm.NewClient(orcaRunner(configuredOrcaBin(cfg)))
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return relaunchDaemonReady(checkCtx, func(ctx context.Context) ([]byte, error) {
			return exec.CommandContext(ctx, "systemctl", "--user", "list-units", "orca-daemon-*.scope", "--state=running", "--no-legend", "--plain", "--no-pager").Output()
		}, func(ctx context.Context) error { _, err := client.List(ctx); return err })
	}
}

// relaunchDaemonReady implements the Linux-only, fail-closed C5 preflight
// by design: a restart must never act on a daemon it cannot prove is the one
// running here, so absence of proof is a refusal, not a guess. Scope evidence is
// local to the poller user and host.
// A successful serve status or an empty terminal list alone proves nothing.
func relaunchDaemonReady(ctx context.Context, scopes func(context.Context) ([]byte, error), list func(context.Context) error) error {
	raw, err := scopes(ctx)
	if err != nil {
		return fmt.Errorf("daemon scope proof unavailable: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return fmt.Errorf("%w: no managed daemon scope", hostpoller.ErrOrcaDown)
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
	if err := list(ctx); err != nil {
		return fmt.Errorf("%w: terminal list unavailable", hostpoller.ErrOrcaDown)
	}
	return nil
}
