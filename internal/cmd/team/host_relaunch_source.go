package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// desiredRow is the worker-row shape both desired-state documents select
// (genqlient generates a separate Go type per document); the generated
// getters let one mapper serve the host-wide and the targeted read alike.
type desiredRow interface {
	GetWorkerId() string
	GetFamily() string
	GetDesired() *gen.DesiredRunState
	GetReason() gen.DesiredStateReason
	GetOpenSessionId() *string
	GetWorktrees() []string
}

// rowFromDesired copies one desired row into its hostpoller shape.
func rowFromDesired(row desiredRow) hostpoller.RelaunchWorker {
	worker := hostpoller.RelaunchWorker{WorkerID: row.GetWorkerId(), Family: row.GetFamily(), Reason: string(row.GetReason()), Worktrees: append([]string{}, row.GetWorktrees()...)}
	if row.GetDesired() != nil {
		worker.Desired = string(*row.GetDesired())
	}
	if row.GetOpenSessionId() != nil {
		worker.SessionID = *row.GetOpenSessionId()
	}
	return worker
}

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
		out.Workers = append(out.Workers, rowFromDesired(row))
	}
	return out, nil
}

// RelaunchWorker is the fresh read about ONE worker (cli#2816,
// hadron-server#4693): the host key asks for the named worker's row alone,
// without a session (cor:agt:070:04). Its answer keeps the three outcomes
// apart (cor:agt:020:03, cor:agt:020:08): a row is the worker's fresh state;
// an empty list is the selector's own definite answer — this host key is not
// shown the worker as desired on this host — and any failure is an error,
// never an empty answer. A failure never falls back to the host-wide list
// either: only the selector's own refusal does (a server that predates #4693
// refuses exactly this document), and only while the host-wide read then
// succeeds, so the pin says the server lacks the selector and nothing else.
// The pin is looked at again every refusedReprobeEvery, so a server upgraded
// while the host runs is noticed without a restart.
func (s *hostServer) RelaunchWorker(ctx context.Context, hostID, workerID string) (hostpoller.RelaunchSnapshot, error) {
	if s.serverURL == "" || hostID == "" || workerID == "" || strings.TrimSpace(workerID) != workerID {
		return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source identity is unavailable")
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	if at := s.noSelectorAt.Load(); at == 0 || now().Sub(time.Unix(0, at)) >= refusedReprobeEvery {
		response, err := gen.HostRelaunchWorker(api.WithoutSession(ctx), s.client, s.app, hostID, workerID)
		if err == nil {
			s.noSelectorAt.Store(0)
			if response == nil || response.MyApp == nil || response.MyApp.Id == "" || response.HostDesiredState == nil || response.HostDesiredState.HostId != hostID {
				return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source returned no canonical App or matching host")
			}
			state := response.HostDesiredState
			out := hostpoller.RelaunchSnapshot{AppID: response.MyApp.Id, HostID: state.HostId, Server: strings.TrimRight(s.serverURL, "/"), SweepMode: string(state.SweepMode), Workers: []hostpoller.RelaunchWorker{}}
			for _, row := range state.Workers {
				if row == nil || row.WorkerId != workerID {
					return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source returned a worker the read did not name")
				}
				if len(out.Workers) > 0 {
					return hostpoller.RelaunchSnapshot{}, errors.New("relaunch source returned the named worker more than once")
				}
				out.Workers = append(out.Workers, rowFromDesired(row))
			}
			return out, nil
		}
		if !api.IsGraphQLValidationOnlyFor(err, "worker") {
			mapped := api.MapError(err)
			// The read's local cause names the server's classification when
			// it has one (an unknown host answers HOST_NOT_FOUND): the message
			// alone does not say the refusal was about the host identity.
			if code := api.ServerCodeOf(err); code != "" && !strings.Contains(mapped.Error(), code) {
				return hostpoller.RelaunchSnapshot{}, fmt.Errorf("%w: %s", mapped, code)
			}
			return hostpoller.RelaunchSnapshot{}, mapped
		}
		// The server may lack the selector. Ask the host-wide document; only
		// if it succeeds is the selector proved missing — no pin otherwise, so
		// a broken host-wide read is never mistaken for an old server.
		wide, werr := s.RelaunchSnapshot(ctx, hostID)
		if werr != nil {
			return hostpoller.RelaunchSnapshot{}, werr
		}
		s.noSelectorAt.Store(now().UnixNano())
		return narrowSnapshotToWorker(wide, workerID), nil
	}
	wide, err := s.RelaunchSnapshot(ctx, hostID)
	if err != nil {
		return hostpoller.RelaunchSnapshot{}, err
	}
	return narrowSnapshotToWorker(wide, workerID), nil
}

// narrowSnapshotToWorker keeps a host-wide answer's identity and at most the
// named worker's rows, copied into a list of their own: the fallback's answer
// has the shape the selector's answer would have had.
func narrowSnapshotToWorker(wide hostpoller.RelaunchSnapshot, workerID string) hostpoller.RelaunchSnapshot {
	out := wide
	out.Workers = []hostpoller.RelaunchWorker{}
	for _, row := range wide.Workers {
		if row.WorkerID == workerID {
			out.Workers = append(out.Workers, row)
		}
	}
	return out
}
