package delivery

import (
	"encoding/json"
	"regexp"
	"time"
)

// RelaunchStampFile is what `team session reset-context` leaves beside the
// session binding (cli#2161, C12): the agent asked for an immediate relaunch
// of this session after it ends. It is worker-written, so it proves nothing
// alone: the poller takes it as half of an exit proof, the other half being
// the exit of the agent it launched, and C5's Runner still applies every
// guard and the cap. A stamp can only make a relaunch C5 would make happen
// sooner.
const RelaunchStampFile = "hadron-team-relaunch.json"

// AgentPIDEnv names the agent's own pid in its environment: an Orca launch
// exports the launching shell's $$, which the exec keeps, so it is the pid
// the launcher pinned. `team session reset-context` ends exactly that
// process, and only when it is the command's own ancestor.
const AgentPIDEnv = "HADRON_AGENT_PID"

// RelaunchStamp is the stamp's whole shape (version 1).
type RelaunchStamp struct {
	Version     int       `json:"version"`
	WorkerID    string    `json:"workerId"`
	SessionID   string    `json:"sessionId"`
	Checkpoint  string    `json:"checkpoint"`
	RequestedAt time.Time `json:"requestedAt"`
}

// relaunchStampClockSkew is how far in the future a stamp may claim to be:
// writer and reader share one host clock, so only a rounding's worth.
const relaunchStampClockSkew = 5 * time.Second

// matches reports whether a parsed stamp names exactly this worker and
// session and was written within ttl of now.
func (s RelaunchStamp) matches(worker, session string, now time.Time, ttl time.Duration) bool {
	if s.Version != 1 || !ValidCheckpointURN(s.Checkpoint) || worker == "" || session == "" || s.WorkerID != worker || s.SessionID != session || s.RequestedAt.IsZero() {
		return false
	}
	age := now.Sub(s.RequestedAt)
	return age >= -relaunchStampClockSkew && age <= ttl
}

// checkpointURNRE is the only checkpoint a stamp may name: a v2 node URN in
// checkpoints:, the shape reset-context writes. The value reaches the new
// agent's first prompt, so nothing else is let through.
var checkpointURNRE = regexp.MustCompile(`^hrn:node:[a-z0-9.-]{1,253}:[A-Za-z0-9_.-]{1,200}:checkpoints:[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}$`)

// ValidCheckpointURN says whether s is a checkpoint URN a stamp may carry.
func ValidCheckpointURN(s string) bool { return checkpointURNRE.MatchString(s) }

func parseRelaunchStamp(b []byte) (RelaunchStamp, bool) {
	var s RelaunchStamp
	if json.Unmarshal(b, &s) != nil {
		return RelaunchStamp{}, false
	}
	return s, true
}
