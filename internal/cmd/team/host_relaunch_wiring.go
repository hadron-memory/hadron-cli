package team

import (
	"context"

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
		Ready:  func(ctx context.Context) error { _, err := client.List(ctx); return err },
		Notice: server.ReportRelaunch,
	})
}
