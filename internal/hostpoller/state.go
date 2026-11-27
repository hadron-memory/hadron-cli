package hostpoller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

// State is what the poller remembers across restarts: the worktrees it
// created (so the next registration lists them — the server does not add a
// created worktree to the registration itself), and the worktrees it must
// keep because a worker.end on them failed (cor:agt:020:10: never remove a
// worktree after a failed end).
type State struct {
	SweepOutage *SweepOutageRecord `json:"sweepOutage,omitempty"`
	path        string
	mu          sync.Mutex
	// Created maps a configured worker reference to the worktree tokens this
	// poller created for it.
	Created map[string][]string `json:"created"`
	// Keep maps a worktree token to why it must not be removed.
	Keep map[string]string `json:"keep"`
	// HostID is the id the server derived from the host key at the last
	// registration — how `status` finds this host's own registration, since
	// a label is not unique.
	HostID string `json:"hostId"`
	// configuredOnly is set for a host that runs from the platform (hadron-cli#3237, SetFromPlatform): a
	// worker's trees are then only the ones this poll resolved (its Config's Worktrees), never a token
	// recorded as created, so every reader of a worker's trees gets a tree with a path. Not persisted.
	configuredOnly bool
	// Agents are the agents this poller launched, by worker id: the
	// worktree each runs in and its process id. A launched agent outlives a
	// poller restart (it runs in its own session), so the in-memory record
	// alone would forget it and let worktree.remove take a tree from under
	// a live agent. A reused pid only makes a check refuse: the safe way.
	Agents map[string]AgentRecord `json:"agents"`
}

// AgentRecord is one launched agent.
type AgentRecord struct {
	Worktree string `json:"worktree"`
	PID      int    `json:"pid"`
	// Start is the launch's kernel start time, observed while it ran (empty
	// when not observed): with PID, its launch identity, so a restarted
	// poller can tell this launch from another at the same pid (cli#1582).
	Start string `json:"start,omitempty"`
}

// EmptyState is a state with no records and NO file: it cannot fail, and it
// cannot be saved over a real state file (save refuses a state without one).
// A caller that must carry on without an unreadable file relies on both.
func EmptyState() *State {
	return &State{Created: map[string][]string{}, Keep: map[string]string{}, Agents: map[string]AgentRecord{}}
}

// LoadState reads the state file; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	s := EmptyState()
	s.path = path
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, err
	}
	if s.Created == nil {
		s.Created = map[string][]string{}
	}
	if s.Keep == nil {
		s.Keep = map[string]string{}
	}
	if s.Agents == nil {
		s.Agents = map[string]AgentRecord{}
	}
	return s, nil
}

func (s *State) save() error {
	if s.path == "" {
		return errors.New("poller state has no file to save to")
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(writtenDir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// worktreesFor is the configured worktrees plus the ones created, unique and
// sorted.
// SetFromPlatform marks the state of a host that runs from the platform (see configuredOnly).
func (s *State) SetFromPlatform() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configuredOnly = true
}

func (s *State) worktreesFor(ref string, configured []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	created := s.Created[ref]
	if s.configuredOnly {
		created = nil
	}
	for _, t := range append(append([]string{}, configured...), created...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func (s *State) addCreated(ref, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.Created[ref] {
		if t == token {
			return nil
		}
	}
	s.Created[ref] = append(s.Created[ref], token)
	return s.save()
}

func (s *State) removeCreated(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ref, ts := range s.Created {
		kept := ts[:0]
		for _, t := range ts {
			if t != token {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(s.Created, ref)
		} else {
			s.Created[ref] = kept
		}
	}
	return s.save()
}

func (s *State) keep(token, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Keep[token] = reason
	return s.save()
}

func (s *State) keepReason(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.Keep[token]
	return r, ok
}

func (s *State) recordAgent(workerID, token string, pid int, start string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Agents[workerID] = AgentRecord{Worktree: token, PID: pid, Start: start}
	return s.save()
}

// dropAgent forgets a worker's launched agent: a start that failed leaves no
// entry behind (#1474), so a dead pid is never consulted again and a state
// file read by an operator names only agents that were left running.
func (s *State) dropAgent(workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Agents, workerID)
	return s.save()
}

// agentPIDsIn lists the recorded agent pids in a worktree.
func (s *State) agentPIDsIn(token string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pids []int
	for _, a := range s.Agents {
		if a.Worktree == token && a.PID > 0 {
			pids = append(pids, a.PID)
		}
	}
	return pids
}

// LaunchAnchor is a launch this poller recorded for a worker, by pid and the
// kernel start time (clock ticks, /proc stat field 22) observed while it ran:
// the process a census agent must be, or run under, to count as the worker's
// own (cli#2059; it replaced the wrapper's name as the supervision anchor).
type LaunchAnchor struct {
	PID        int
	StartTicks int64
}

// LaunchAnchorsAt is the recorded launches of the workers whose recorded
// worktree is path (worktreesRoot joined with the recorded token). A record
// with no pid, or a start that is not a tick count, yields an anchor that
// cannot match (StartTicks 0): without the start a reused pid could pass.
func (s *State) LaunchAnchorsAt(worktreesRoot, path string) []LaunchAnchor {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []LaunchAnchor{}
	for _, a := range s.Agents {
		if a.PID <= 0 || !sameDir(filepath.Join(worktreesRoot, a.Worktree), path) {
			continue
		}
		ticks, err := strconv.ParseInt(a.Start, 10, 64)
		if err != nil || ticks <= 0 {
			ticks = 0
		}
		out = append(out, LaunchAnchor{PID: a.PID, StartTicks: ticks})
	}
	return out
}

// recordedAgent is the agent last recorded for the worker (zero when none).
func (s *State) recordedAgent(workerID string) AgentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Agents[workerID]
}

// recordedWorktree is the worktree a recorded agent of this worker ran in.
func (s *State) recordedWorktree(workerID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.Agents[workerID]
	return a.Worktree, ok
}

// ownerOf is the worker reference a worktree token is registered for, from
// the configuration or this poller's creations.
func (s *State) ownerOf(token string, cfg *Config) (string, bool) {
	for _, ref := range cfg.WorkerRefs() {
		w, _ := cfg.Worker(ref)
		if contains(s.worktreesFor(ref, w.Worktrees), token) {
			return ref, true
		}
	}
	return "", false
}

func (s *State) setHostID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || id == s.HostID {
		return nil
	}
	s.HostID = id
	return s.save()
}

// RegisteredHostID is the host id saved at the last registration.
func (s *State) RegisteredHostID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HostID
}

// Snapshot copies the state for display.
func (s *State) Snapshot() (map[string][]string, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := map[string][]string{}
	for k, v := range s.Created {
		c[k] = append([]string{}, v...)
	}
	k := map[string]string{}
	for t, r := range s.Keep {
		k[t] = r
	}
	return c, k
}

// sameDir says two spellings name one directory: the same cleaned path, or the
// same path once symlinks are resolved (a worktrees root reached through a
// link). A path that cannot be resolved is compared as cleaned, never wider.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
