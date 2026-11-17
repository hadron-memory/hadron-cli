package team

import (
	"context"
	"errors"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// EndAgentless asks the server to end a SHORT_LIVED worker's open session on
// this host's census proof that no agent runs (hadron-server#4292, cli#2201).
// It is a host-key call with no worker session behind it: the server checks
// that this host runs the worker, that the session is the worker's, that the
// worker is short-lived and holds no open task, and refuses otherwise with a
// closed code, which the caller reports as the sweep line's reason.
func (s *hostServer) EndAgentless(ctx context.Context, e hostpoller.AgentlessEnd) (hostpoller.AgentlessEndResult, error) {
	if e.AppID == "" || e.WorkerID == "" || e.SessionID == "" {
		return hostpoller.AgentlessEndResult{}, errors.New("the agentless end names no App, worker or session")
	}
	ctx, cancel := context.WithTimeout(api.WithoutSession(ctx), 30*time.Second)
	defer cancel()
	resp, err := gen.HostEndAgentlessSession(ctx, s.client, &gen.EndAgentlessSessionInput{
		AppRef: e.AppID, WorkerRef: e.WorkerID, SessionId: e.SessionID, Reason: gen.AgentlessEndReasonAgentAbsent,
	})
	if err != nil {
		if api.IsGraphQLValidationOnlyFor(err, "endAgentlessSession") {
			return hostpoller.AgentlessEndResult{}, &hostpoller.AgentlessRefusal{Code: hostpoller.AgentlessUnsupported}
		}
		// A closed code on a normal response is a refusal: nothing was written.
		// A 5xx envelope can follow a committed write, so it is not one.
		if code := api.ServerCodeOf(err); code != "" && !api.FromNon200Envelope(err) {
			return hostpoller.AgentlessEndResult{}, &hostpoller.AgentlessRefusal{Code: code}
		}
		return hostpoller.AgentlessEndResult{}, api.MapError(err)
	}
	if resp == nil || resp.EndAgentlessSession.SessionId != e.SessionID {
		return hostpoller.AgentlessEndResult{}, errors.New("the server's answer does not name the session that was asked to end")
	}
	return hostpoller.AgentlessEndResult{Ended: resp.EndAgentlessSession.Ended}, nil
}
