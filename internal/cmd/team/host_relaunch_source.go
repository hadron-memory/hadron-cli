package team

import (
	"context"
	"errors"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

func (s *hostServer) RelaunchSnapshot(ctx context.Context, hostID string) (hostpoller.RelaunchSnapshot, error) {
	if s.serverURL == "" || hostID == "" {
		return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source identity is unavailable")
	}
	response, err := gen.HostRelaunchSnapshot(api.WithoutSession(ctx), s.client, s.app, hostID)
	if err != nil {
		return hostpoller.RelaunchSnapshot{}, api.MapError(err)
	}
	if response == nil || response.MyApp == nil || response.MyApp.Id == "" || response.HostDesiredState == nil || response.HostDesiredState.HostId != hostID {
		return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source returned no canonical App or matching host")
	}
	state := response.HostDesiredState
	out := hostpoller.RelaunchSnapshot{AppID: response.MyApp.Id, HostID: state.HostId, Server: strings.TrimRight(s.serverURL, "/"), SweepMode: string(state.SweepMode), Workers: []hostpoller.RelaunchWorker{}}
	seen := map[string]bool{}
	for _, row := range state.Workers {
		if row == nil || row.WorkerId == "" || seen[row.WorkerId] {
			return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source returned an invalid or duplicate worker")
		}
		seen[row.WorkerId] = true
		worker := hostpoller.RelaunchWorker{WorkerID: row.WorkerId, Family: row.Family, Reason: string(row.Reason), Worktrees: append([]string{}, row.Worktrees...)}
		if row.Desired != nil {
			worker.Desired = string(*row.Desired)
		}
		if row.OpenSessionId != nil {
			worker.SessionID = *row.OpenSessionId
		}
		out.Workers = append(out.Workers, worker)
	}
	return out, nil
}
