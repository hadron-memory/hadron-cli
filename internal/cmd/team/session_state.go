package team

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// The server's canonical per-session states (Session.state, cor:agt:020:13),
// lower-cased for --json, plus "unknown" for everything this CLI cannot say.
const (
	stateLive    = "live"
	stateLapsed  = "lapsed"
	stateDormant = "dormant"
	stateEnded   = "ended"
	// stateUnknown: the server predates Session.state, or answered nothing for
	// the session, or answered a state this build does not know. Never a guess.
	stateUnknown = "unknown"
)

// sessionStateDTO is what `whoami --check` learned from Session.state. It is
// the SERVER's answer verbatim: the CLI holds no expiry, idle or dormancy
// policy, and derives nothing from a clock or from `lapsesAt` (cli#815).
type sessionStateDTO struct {
	Live     *bool
	LapsesAt *string
	State    string
}

// candidateStateDTO is one row of whoami's --check answer when no binding picks
// a single session: the candidate's id plus the same three facts.
type candidateStateDTO struct {
	SessionID string  `json:"sessionId"`
	Live      *bool   `json:"live"`
	LapsesAt  *string `json:"lapsesAt"`
	State     string  `json:"state"`
}

// fetchSessionState reads the server's state for one session through
// GetBoundSessionState, which is separate from GetTeamSession so an older
// server (a validation refusal naming one of the new fields) degrades to
// "unknown" instead of breaking the ordinary read. Any OTHER failure keeps the
// command's existing error mapping: a transport or auth problem is not "unknown".
func fetchSessionState(ctx context.Context, client graphql.Client, id string) (sessionStateDTO, error) {
	resp, err := gen.GetBoundSessionState(ctx, client, id)
	if err != nil {
		for _, field := range []string{"state", "lapsesAt", "isLive"} {
			if api.IsGraphQLValidationFor(err, field) {
				return sessionStateDTO{State: stateUnknown}, nil
			}
		}
		return sessionStateDTO{}, api.MapError(err)
	}
	if resp == nil || resp.Session == nil {
		return sessionStateDTO{State: stateUnknown}, nil
	}
	live := resp.Session.IsLive
	return sessionStateDTO{Live: &live, LapsesAt: resp.Session.LapsesAt, State: stateName(resp.Session.State)}, nil
}

func stateName(s gen.SessionState) string {
	switch s {
	case gen.SessionStateLive:
		return stateLive
	case gen.SessionStateLapsed:
		return stateLapsed
	case gen.SessionStateDormant:
		return stateDormant
	case gen.SessionStateEnded:
		return stateEnded
	}
	return stateUnknown
}

// stateLine renders the `server:` line of `whoami --check` for a checked
// binding. The state decides, not `active` (which is only `endedAt IS NULL`):
// a session can be ENDED by expiry before the server physically reaps it, and
// DORMANT is not "open" or merely resumable.
func stateLine(d whoamiDTO) string {
	state := stateUnknown
	if d.State != nil {
		state = *d.State
	}
	at := func() string {
		if d.LapsesAt != nil {
			return *d.LapsesAt
		}
		return "(instant unknown)"
	}
	switch state {
	case stateLive:
		if d.LapsesAt != nil {
			return "live, lapses at " + *d.LapsesAt
		}
		return "live"
	case stateLapsed:
		return fmt.Sprintf("LAPSED at %s — open but not live; rebind with `hadron team session start --as %s`", at(), d.WorkerName)
	case stateDormant:
		return "DORMANT — not resumable by any agent act; its human holder must rebind"
	case stateEnded:
		if d.Active != nil && *d.Active {
			// Hard-expired before the reap: the row is still open, the session is not.
			return fmt.Sprintf("ENDED — expired and not yet reaped by the server; rebind with `hadron team session start --as %s`", d.WorkerName)
		}
		return livenessLine(d)
	}
	// unknown: the previous answer, honestly labelled.
	if d.Active != nil && *d.Active {
		return "open (live state unknown — the server predates session state)"
	}
	return livenessLine(d)
}

// stateCell is the STATE column of the several-candidates table.
func stateCell(c candidateStateDTO) string {
	switch c.State {
	case stateLive:
		if c.LapsesAt != nil {
			return "live (lapses " + *c.LapsesAt + ")"
		}
		return "live"
	case stateLapsed:
		if c.LapsesAt != nil {
			return "LAPSED " + *c.LapsesAt
		}
		return "LAPSED"
	case stateDormant:
		return "DORMANT"
	case stateEnded:
		return "ENDED"
	}
	return "unknown"
}
