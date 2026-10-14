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
	App                  string        `json:"app"`
	Channel              string        `json:"channel"`
	OrcaCli              string        `json:"orcaCli"`
	StateDir             string        `json:"stateDir"`
	Roster               []RosterEntry `json:"roster"`
	BusyWaitMS           int           `json:"busyWaitMs"`
	SubmitWaitS          int           `json:"submitWaitS"`
	CoordinatorWatchMin  float64       `json:"coordinatorWatchMinutes"`
}

// RosterEntry names one worker this router may wake. Pin (#1845) optionally
// fixes the row's MAIN terminal; Watch (#1826) opts the row into the
// coordinator/liveness rule.
type RosterEntry struct {
	Worker   string `json:"worker"`
	Name     string `json:"name"`
	Worktree string `json:"worktree"`
	Pin      string `json:"pin,omitempty"`
	Watch    bool   `json:"watch,omitempty"`
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
	Worker         string `json:"worker"`
	WorkerName     string `json:"workerName"`
	Channel        string `json:"channel"`
	Unread         int    `json:"unread"`
	UnreadMentions int    `json:"unreadMentions"`
	FirstUnreadSeq int    `json:"firstUnreadSeq"`
	LastRelevantSeq int   `json:"lastRelevantSeq"`
	LastSeq        int    `json:"lastSeq"`
}

// AttentionPage is one drained page of the attention scan.
type AttentionPage struct {
	ScanComplete    bool            `json:"scanComplete"`
	NextPage        string          `json:"nextPage"`
	AdoptableSince  string          `json:"adoptableSince"`
	Items           []AttentionItem `json:"items"`
	IncompleteScan  bool            `json:"incompleteScan"` // errors present: refuse (#1700 class)
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
	NamedBy     string `json:"namedBy"`
	ChannelID   string `json:"channel"`
	MentionOnly bool   `json:"mentionOnly"`
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
	// MarkRead advances the router's own session cursor (mentions walk).
	MarkRead(ctx context.Context, through int) error
}

// Outcome is one poll's result: who was nudged, who was held and why.
type Outcome struct {
	Nudged  []string
	Held    map[string]string
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

// NudgeText renders the wake text for a due worker. RED-FIRST STUB.
func NudgeText(item AttentionItem, anchor *ReadAnchor, sessRouter SessionRef) string { return "" }

// ReadAnchor is the bounded read anchor the nudge names (#1805/#1811).
type ReadAnchor struct {
	FirstUnreadSeq int
	SinceSeq       int
	Head           int
	LargeGap       bool
}

// SessionRef names the router's own session for the mark ceiling.
type SessionRef struct{ ID string }

// PromptLineIsEmpty proves the screen's prompt box empty (draft refutation
// rules per family). RED-FIRST STUB.
func PromptLineIsEmpty(family string, tail []string, draft string) bool { return false }

// ScreenAgentFamily reads positive family chrome only; "" = unresolved.
// RED-FIRST STUB.
func ScreenAgentFamily(tail []string) string { return "" }

// ResolveTerminal maps a roster row to its agent terminal. RED-FIRST STUB.
func ResolveTerminal(c *Config, entry RosterEntry, terminals []Terminal, orca OrcaRunner) (*Terminal, string, error) {
	return nil, "", &RouterError{Reason: "unimplemented"}
}

// LoadConfig reads and validates the router's config. RED-FIRST STUB.
func LoadConfig(path string) (*Config, error) { return nil, &RouterError{Reason: "unimplemented"} }

// State is the token/ledger/liveness persistence (0700/0600, single-writer).
type State struct{ dir string }

func NewState(dir string) *State          { return &State{dir: dir} }
func (s *State) Token() string            { return "" } // RED-FIRST STUB
func (s *State) ClearToken()              {}

// Status renders the router's current state for `team router status`.
// RED-FIRST STUB.
func Status(c *Config, s Server) (string, error) { return "", &RouterError{Reason: "unimplemented"} }

// DescribeOutcome renders an Outcome for logs/status. RED-FIRST STUB.
func DescribeOutcome(o *Outcome) string { return "" }

// BreakStaleLock reclaims a lock whose owner cannot be alive. RED-FIRST STUB.
func BreakStaleLock(path string) error { return &RouterError{Reason: "unimplemented"} }
