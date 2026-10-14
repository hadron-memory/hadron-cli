package hostpoller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// State is what the poller remembers across restarts: the worktrees it
// created (so the next registration lists them — the server does not add a
// created worktree to the registration itself), and the worktrees it must
// keep because a worker.end on them failed (cor:agt:020:10: never remove a
// worktree after a failed end).
type State struct {
	path string
	mu   sync.Mutex
	// Created maps a configured worker reference to the worktree tokens this
	// poller created for it.
	Created map[string][]string `json:"created"`
	// Keep maps a worktree token to why it must not be removed.
	Keep map[string]string `json:"keep"`
}

// LoadState reads the state file; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	s := &State{path: path, Created: map[string][]string{}, Keep: map[string]string{}}
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
	return s, nil
}

func (s *State) save() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
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
func (s *State) worktreesFor(ref string, configured []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	for _, t := range append(append([]string{}, configured...), s.Created[ref]...) {
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
