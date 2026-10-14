// Package teamrouter is the Go port of the Orca attention router
// (hadron-server scripts/orca-attention-router, TypeScript) — cli#829 (T7).
//
// The acceptance spec is the TS suite, ported 1:1 into these packages' tests
// (138 tests, 15 describe blocks, at server main f0ab31a0 — #1845's per-roster
// terminal pin). The TS router shelled out to `hadron api`; this port speaks
// GraphQL natively through the Server seam below and keeps exec-ing only the
// `orca` CLI (terminal list/wait/read/send — no Orca SDK exists, and the CLI
// boundary serves the two-signal design).
//
// Stubs in this file exist so the ported suite compiles and runs RED first;
// the implementation lands per block until the suite is green.
package teamrouter

import "context"

// Config is the router's host-local configuration (cli#829 design notes:
// placement follows the CLI's own config conventions).
type Config struct {
	App                 string        `json:"app"`
	Channel             string        `json:"channel"`
	OrcaCli             string        `json:"orcaCli"`
	StateDir            string        `json:"stateDir"`
	Roster              []RosterEntry `json:"roster"`
	BusyWaitMS          int           `json:"busyWaitMs"`
	SubmitWaitS         int           `json:"submitWaitS"`
	CoordinatorWatchMin float64       `json:"coordinatorWatchMinutes"`
}

// RosterEntry names one worker this router may wake. Terminal (#1845,
// "the pin") optionally fixes the row's MAIN terminal; Coordinator and
// Lifecycle (#1826) opt the row into the liveness rule. The JSON keys are
// the TS router's, so an existing roster file at
// ~/.config/hadron-orca-attention-router/ loads unchanged (#829).
type RosterEntry struct {
	Worker      string `json:"worker"`
	Name        string `json:"name"`
	Worktree    string `json:"worktree"`
	Terminal    string `json:"terminal,omitempty"`
	Coordinator bool   `json:"coordinator,omitempty"`
	Lifecycle   bool   `json:"lifecycle,omitempty"`
}

// Terminal is one `orca terminal list` row.
type Terminal struct {
	Handle        string  `json:"handle"`
	WorktreePath  string  `json:"worktreePath"`
	AgentIdentity *string `json:"agentIdentity"`
	Connected     bool    `json:"connected"`
	Writable      bool    `json:"writable"`
	LastOutputAt  *int64  `json:"lastOutputAt"` // null = untracked (#7398)
}

// RunResult is one exec'd `orca` invocation's outcome.
type RunResult struct {
	Code   int
	Stdout string
	Stderr string
}

// OrcaRunner execs the `orca` CLI. argv[0] is the verb path after the binary.
type OrcaRunner func(ctx context.Context, argv []string) RunResult

// AttentionItem is one due worker row from the v2 paged attention surface.
type AttentionItem struct {
	Worker          string `json:"worker"`
	WorkerName      string `json:"workerName"`
	Channel         string `json:"channel"`
	Unread          int    `json:"unread"`
	UnreadMentions  int    `json:"unreadMentions"`
	FirstUnreadSeq  *int   `json:"firstUnreadSeq"` // null = no read anchor (generic text)
	LastRelevantSeq int    `json:"lastRelevantSeq"`
	LastSeq         int    `json:"lastSeq"`
}

// AttentionPage is one drained page of the attention scan.
type AttentionPage struct {
	ScanComplete   bool            `json:"scanComplete"`
	NextPage       string          `json:"nextPage"`
	AdoptableSince string          `json:"adoptableSince"`
	Items          []AttentionItem `json:"items"`
	IncompleteScan bool            `json:"incompleteScan"` // errors present: refuse (#1700 class)
}

// AttentionRequest addresses the paged surface: Since adopts a token, Page
// walks pages. Both empty = the tokenless fresh re-read before each send
// (#1616).
type AttentionRequest struct {
	Since string
	Page  string
}

// RegisterRow is the winning attendeeRegister row for the team Channel.
type RegisterRow struct {
	NamedBy     string `json:"namedBy"` // WORKER rows count; EVERYONE rows never do
	ChannelID   string `json:"channel"`
	MentionOnly bool   `json:"mentionOnly"`
	OwnerType   string `json:"ownerType,omitempty"` // ORGANIZATION rows count: the check reads the effective registration
}

// CursorResult is a worker's channelReadState — the liveness source attention
// cannot see (#1826). FieldErrs carries legal GraphQL errors-with-data: an
// integer fragment alongside errors is NOT live (#1829 P1-1).
type CursorResult struct {
	LastSeen  *int
	FieldErrs []string
}

// Server is the typed GraphQL seam (native, never `hadron api`). All methods
// run under the App-key scope attention:deliver.
type Server interface {
	AttentionPage(ctx context.Context, req AttentionRequest) (AttentionPage, error)
	RegisterRow(ctx context.Context, workerID string) (RegisterRow, error)
	ReadCursor(ctx context.Context, workerID string) (CursorResult, error)
	ChannelHead(ctx context.Context, channelRef string) (int, error)
}

// The router never marks anything read (router.ts header): the mentions
// walk's mark is the WORKER's own step, through its own session, named in
// the nudge text — a mark-read capability on the router's App key would
// widen the §10 surface for nothing. (Joshua #11553.)

// Outcome is one poll's result: who was nudged, who was held and why.
type Outcome struct {
	Nudged  []string
	Held    map[string]string
	Dropped []string
	Covered []string
	Adopted bool
}

// RouterError distinguishes a held poll (recorded, retried) from a failed one
// (nothing changes).
type RouterError struct {
	Reason string
}

func (e *RouterError) Error() string { return "teamrouter: " + e.Reason }

// PollOnce runs one scan→gate→nudge cycle. RED-FIRST STUB: unimplemented.
func PollOnce(ctx context.Context, c *Config, s Server, orca OrcaRunner, log func(string), now func() int64) (*Outcome, error) {
	return nil, &RouterError{Reason: "unimplemented"}
}

// NudgeText renders the wake text for a due worker: the bounded read
// anchor (#1805), the large-gap mentions-only branch (#1811), the frozen
// backward walk (#1827). RED-FIRST STUB.
func NudgeText(item AttentionItem) string { return "" }

// PromptLineIsEmpty proves the screen's prompt box empty for the family
// (placeholder for codex, empty box + status hint for claude; trailing
// non-chrome content holds). RED-FIRST STUB.
func PromptLineIsEmpty(tail []string, family string) bool { return false }

// Screen is one `orca terminal read` result: the rendered tail, the optional
// draft field, and the source (only "screen" carries the two-signal witness).
type Screen struct {
	Source string
	Tail   []string
	Draft  string
}

// ScreenAgentFamily reads positive family chrome only; "" = unresolved.
// RED-FIRST STUB.
func ScreenAgentFamily(sc *Screen) string { return "" }

// ResolveTerminal maps a roster row to its agent terminal, returning the
// handle, the agent family, and how it was resolved ("identity", "screen"
// — the #1823 flap fallback — or "pin", #1845). RED-FIRST STUB.
func ResolveTerminal(entry RosterEntry, terminals []Terminal, c *Config, orca OrcaRunner) (handle, family, how string, err error) {
	return "", "", "", &RouterError{Reason: "unimplemented"}
}

// LoadConfig reads and validates the router's config. RED-FIRST STUB.
func LoadConfig(path string) (*Config, error) { return nil, &RouterError{Reason: "unimplemented"} }

// State is the token/ledger/liveness persistence (0700/0600, single-writer).
type State struct{ dir string }

func NewState(dir string) *State { return &State{dir: dir} }
func (s *State) Token() string   { return "" } // RED-FIRST STUB

func (s *State) ClearToken() {}

// LedgerNudge records a delivered nudge's mention frontier.
type LedgerNudge struct {
	RelevantSeq int
}

// Ledger is the durable delivery record: per-worker nudged frontiers and
// hold-start times (the long-hold log's clock).
type Ledger struct {
	Nudged    map[string]LedgerNudge
	HeldSince map[string]int64
}

func (s *State) Ledger() Ledger { return Ledger{} } // RED-FIRST STUB

func (s *State) SaveLedger(l Ledger) {} // RED-FIRST STUB

// Status renders the router's current state for `team router status`.
// RED-FIRST STUB.
func Status(c *Config, s Server) (string, error) { return "", &RouterError{Reason: "unimplemented"} }

// DescribeOutcome renders an Outcome for logs/status. RED-FIRST STUB.
func DescribeOutcome(o *Outcome) string { return "" }

// BreakStaleLock reclaims a lock whose owner a racer judged stale, guarding
// against races: "retry" = broken (or already gone), take it; "busy" = the
// lock changed under the stale judgement and now looks live — stand down and
// retry the whole acquire. RED-FIRST STUB.
func BreakStaleLock(lockDir, staleOwner string) (verdict string, err error) {
	return "", &RouterError{Reason: "unimplemented"}
}
