package teamrouter

// The v2 delivery source (cli#829 PR 2; plan hadron-server:design:named-heartbeats
// §7): the server decides WHO, WHY and HOW OFTEN through dueWakes; the router
// decides HOW, with the same resolution and send gates as v1. It runs on an
// App key with attention:deliver and no session (cor:agt:080:22), and
// acknowledges each delivered wake with its proof (cor:agt:080:16/:17).
//
// RED-FIRST STUBS: the suite in wakes_test.go pins the contract below.

import "context"

// WakeChannel is one channel range a wake covers.
type WakeChannel struct {
	ID               string
	Name             string
	FromSeq          int
	ThroughSeq       int
	LatestMentionSeq *int
}

// WakeTask is a TASK reason's task (#1708).
type WakeTask struct {
	Ref   string // stable label, e.g. TASK:<id>:assigned
	Title string
	Event string
}

// WakeReason is why a wake is due: a MENTION, a ticker TICK, or a TASK event.
type WakeReason struct {
	Kind   string
	Ticker string // the ticker's name, for TICK
	Task   *WakeTask
}

// Wake is one due wake for one worker.
type Wake struct {
	Worker         string
	WorkerName     string
	Unread         int
	UnreadMentions int
	Channels       []WakeChannel
	Reasons        []WakeReason
	// Proof is handed back to acknowledgeWakes after delivery: server-signed,
	// bound to this requester, good for 15 minutes. Never logged, never typed.
	Proof string
}

// DormantWorker is a dormant worker whose mentions were withheld.
type DormantWorker struct {
	Worker   string
	Name     string
	Mentions int
}

// WakePage is one page of dueWakes.
type WakePage struct {
	Status    string // OK | WAKE_BUDGET_EXHAUSTED
	Withheld  int
	Dormant   []DormantWorker
	Wakes     []Wake
	EndCursor string
	HasNext   bool
}

// AckResult is acknowledgeWakes' answer.
type AckResult struct {
	Acknowledged int
	Unchanged    int
}

// WakeServer is the v2 seam: the due-wake read and the acknowledgement.
type WakeServer interface {
	DueWakes(ctx context.Context, workerRefs []string, after string) (WakePage, error)
	AcknowledgeWakes(ctx context.Context, proofs []string) (AckResult, error)
}

// WakeOutcome is one v2 poll's result.
type WakeOutcome struct {
	Nudged []string `json:"nudged"`
	// Dropped: no longer due at the fresh re-read just before the send.
	Dropped []string          `json:"dropped"`
	Held    map[string]string `json:"held"`
	// AckFailed: delivered, but the acknowledgement was refused or failed.
	// The sent-record keeps the next poll from typing it again.
	AckFailed map[string]string `json:"ackFailed"`
	// Recovered: an earlier delivery whose acknowledgement this poll completed
	// without sending again.
	Recovered []string `json:"recovered"`
	// Would: shadow mode only — who would be woken, and why.
	Would    map[string]string `json:"would"`
	Status   string            `json:"status"`
	Withheld int               `json:"withheld"`
	Dormant  []string          `json:"dormant"`
}

// PollWakes runs one v2 poll. With shadow set it only reads dueWakes:
// no terminal is touched, nothing is sent or acknowledged, no state written.
// RED-FIRST STUB.
func PollWakes(ctx context.Context, c *Config, s WakeServer, orca OrcaRunner, log func(string), now func() int64, shadow bool) (*WakeOutcome, error) {
	return nil, &RouterError{Reason: "unimplemented"}
}

// WakeNudgeText renders a v2 wake as the one line typed into the terminal.
// RED-FIRST STUB.
func WakeNudgeText(w Wake, teamChannel string) string { return "" }

// ReadAppKeyFile reads an App key from a private file. RED-FIRST STUB.
func ReadAppKeyFile(path string) (string, error) {
	return "", &RouterError{Reason: "unimplemented"}
}
