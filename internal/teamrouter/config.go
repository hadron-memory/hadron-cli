package teamrouter

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config is the router's host-local configuration. Its JSON keys are
// router.ts's, so an existing ~/.config/hadron-orca-attention-router config
// loads unchanged (cli#829: config migrates). `hadronCli` is accepted and
// ignored: this port speaks GraphQL natively.
type Config struct {
	// Fingerprint identifies the exact bytes loaded, without exposing state tokens.
	Fingerprint string        `json:"-"`
	App         string        `json:"app"`
	Channel     string        `json:"channel"`
	OrcaCli     string        `json:"orcaCli"`
	StateDir    string        `json:"stateDir"`
	Roster      []RosterEntry `json:"roster"`
	// BusyWaitMS is the idle gate's tui-idle wait (default 60 s).
	BusyWaitMS int `json:"busyWaitMs"`
	// SubmitWaitS is `orca terminal send --wait-submit` (default 15 s).
	SubmitWaitS int `json:"submitWaitS"`
	// CoordinatorWatchMin is the #1826 liveness rule's N: minutes of
	// continuous idle a flagged worker must exceed (strictly) before its
	// liveness nudge may fire. Positive and finite; default 20.
	CoordinatorWatchMin float64 `json:"coordinatorWatchMinutes"`
}

// RosterEntry names one worker this router may wake.
type RosterEntry struct {
	// Worker is the Worker's permanent id.
	Worker string `json:"worker"`
	Name   string `json:"name"`
	// Worktree is the Orca worktree this worker is driven in.
	Worktree string `json:"worktree"`
	// Terminal (#1845) pins the handle of this worker's MAIN terminal. A pin
	// resolves FIRST, but only ever to a terminal IN this row's worktree,
	// connected and writable — a stale pin falls through with its reason.
	Terminal string `json:"terminal,omitempty"`
	// Coordinator / Lifecycle (#1826) opt the row into the liveness rule:
	// a watch a passive wait can strand.
	Coordinator bool `json:"coordinator,omitempty"`
	Lifecycle   bool `json:"lifecycle,omitempty"`
}

func (e RosterEntry) flagged() bool { return e.Coordinator || e.Lifecycle }

// DefaultOrcaCli is Orca's bundled CLI on macOS; elsewhere `orca` on PATH.
func DefaultOrcaCli() string {
	if runtime.GOOS == "darwin" {
		return "/Applications/Orca.app/Contents/Resources/bin/orca"
	}
	return "orca"
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// realPath resolves symlinks, falling back to the absolute path.
func realPath(p string) string {
	p = expandHome(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// LoadConfig reads and validates the router config. Validation is strict
// where a permissive read once passed a typo silently: roster entries need
// worker, name and worktree; the watch flags are JSON booleans; a pin is a
// non-empty string; no worker, pin or worktree appears twice; numbers are
// JSON numbers.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, routerErrorf("config: %v", err)
	}
	return parseConfig(b, path)
}

func parseConfig(b []byte, path string) (*Config, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		return nil, routerErrorf("config %s is not a JSON object", path)
	}
	c := &Config{Fingerprint: fmt.Sprintf("%x", sha256.Sum256(b)), OrcaCli: DefaultOrcaCli(), BusyWaitMS: 60_000, SubmitWaitS: 15, CoordinatorWatchMin: 20}
	// The roster is validated first, as in router.ts, so a config with
	// several faults names the same one either router would.
	roster, err := loadRoster(raw["roster"])
	if err != nil {
		return nil, err
	}
	c.Roster = roster
	for _, f := range []struct {
		key string
		dst *string
	}{{"app", &c.App}, {"channel", &c.Channel}, {"stateDir", &c.StateDir}} {
		if err := json.Unmarshal(raw[f.key], f.dst); err != nil || *f.dst == "" {
			return nil, routerErrorf(`config needs "app", "channel" and "stateDir"`)
		}
	}
	c.StateDir = expandHome(c.StateDir)
	if v, ok := raw["orcaCli"]; ok {
		if err := json.Unmarshal(v, &c.OrcaCli); err != nil || c.OrcaCli == "" {
			return nil, routerErrorf(`"orcaCli" must be a non-empty string`)
		}
	}
	for _, f := range []struct {
		key string
		dst *int
	}{{"busyWaitMs", &c.BusyWaitMS}, {"submitWaitS", &c.SubmitWaitS}} {
		if v, ok := raw[f.key]; ok {
			n, isNum := jsonInt(v)
			if !isNum || bytes.HasPrefix(bytes.TrimSpace(v), []byte(`"`)) || n <= 0 || n > math.MaxInt32 {
				return nil, routerErrorf("%s must be a positive integer JSON number (got %s)", f.key, v)
			}
			*f.dst = int(n)
		}
	}
	// A JSON NUMBER or absent — never a coerced true/"20" (#1829 local
	// P2-7) — and the ms product must stay finite.
	if v, ok := raw["coordinatorWatchMinutes"]; ok {
		var m float64
		if err := json.Unmarshal(v, &m); err != nil || bytes.HasPrefix(bytes.TrimSpace(v), []byte(`"`)) {
			return nil, routerErrorf("coordinatorWatchMinutes must be a JSON number (got %s)", v)
		}
		if math.IsInf(m, 0) || math.IsNaN(m) || m <= 0 || math.IsInf(m*60_000, 0) {
			return nil, routerErrorf("coordinatorWatchMinutes must be a positive finite number of minutes (got %s)", v)
		}
		c.CoordinatorWatchMin = m
	}
	return c, nil
}

func loadRoster(raw json.RawMessage) ([]RosterEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []RosterEntry{}, nil
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, routerErrorf(`"roster" must be an array of objects`)
	}
	out := make([]RosterEntry, 0, len(rows))
	workers, pins, trees := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		show := func() string { b, _ := json.Marshal(row); return string(b) }
		var e RosterEntry
		for _, f := range []struct {
			key string
			dst *string
		}{{"worker", &e.Worker}, {"name", &e.Name}, {"worktree", &e.Worktree}} {
			if err := json.Unmarshal(row[f.key], f.dst); err != nil || *f.dst == "" {
				return nil, routerErrorf(`roster entry %s lacks "%s"`, show(), f.key)
			}
		}
		// #1826 — booleans or absent; a truthy string must not silently count.
		for _, f := range []struct {
			key string
			dst *bool
		}{{"coordinator", &e.Coordinator}, {"lifecycle", &e.Lifecycle}} {
			if v, ok := row[f.key]; ok {
				if err := json.Unmarshal(v, f.dst); err != nil || string(bytes.TrimSpace(v)) == "null" {
					return nil, routerErrorf(`roster entry %s: "%s" must be a boolean`, show(), f.key)
				}
			}
		}
		// #1845 — a non-empty string handle or absent.
		if v, ok := row["terminal"]; ok {
			if err := json.Unmarshal(v, &e.Terminal); err != nil || strings.TrimSpace(e.Terminal) == "" {
				return nil, routerErrorf(`roster entry %s: "terminal" must be a non-empty terminal handle string`, show())
			}
		}
		if workers[e.Worker] {
			return nil, routerErrorf("roster names a worker twice")
		}
		workers[e.Worker] = true
		// Two rows pinning one terminal would deliver one worker's nudge to
		// another's terminal — the one-worktree rule's failure, one layer up.
		if e.Terminal != "" {
			if pins[e.Terminal] {
				return nil, routerErrorf("roster pins one terminal for two workers")
			}
			pins[e.Terminal] = true
		}
		// Two workers on one worktree resolve to the same terminal: one nudge
		// recorded as delivered twice, one worker never told (@codex P2, #1490).
		tree := realPath(e.Worktree)
		if trees[tree] {
			return nil, routerErrorf("roster assigns one worktree to two workers")
		}
		trees[tree] = true
		out = append(out, e)
	}
	return out, nil
}

// routerErrorf builds a RouterError: a failure that leaves the adopted token
// untouched.
func routerErrorf(format string, args ...any) *RouterError {
	return &RouterError{Reason: fmt.Sprintf(format, args...)}
}
