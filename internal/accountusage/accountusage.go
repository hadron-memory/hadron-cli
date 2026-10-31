// Package accountusage is the pure logic behind `hadron team usage` (cli#1472):
// read each configured model account's rate-limit snapshot (and optionally a
// ccusage JSON file the operator produced), validate everything before any
// record exists, and shape one `account-usage` record per account.
//
// It reads exactly two kinds of file per account and nothing else: the
// snapshot at <configDir>/hadron-usage/rate-limits.json and the optional
// ccusage file named in the config. It never runs a subprocess and never
// reads anything else under a Claude config dir (credentials and transcripts
// live there). Errors name a path and a reason class, never file contents.
package accountusage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	// Collection is the Hadron object type the records are written under.
	Collection = "account-usage"
	// SchemaVersion is the record shape version (golden-pinned).
	SchemaVersion = 1
	// SnapshotRel is where the operator's status line writes the snapshot,
	// relative to an account's config dir.
	SnapshotRel = "hadron-usage/rate-limits.json"

	maxSnapshotBytes = 1 << 20
	maxCcusageBytes  = 16 << 20
	maxLabelLen      = 64
)

// ValidationError is a refusal of the operator's input (exit 2): a config,
// snapshot or ccusage file that is missing, malformed or out of range.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &ValidationError{fmt.Sprintf(format, a...)} }

// Account is one configured subscription account.
type Account struct {
	Label       string   `yaml:"label"`
	ConfigDir   string   `yaml:"configDir"`
	Family      string   `yaml:"family"`
	CcusageFile string   `yaml:"ccusageFile"`
	Workers     []string `yaml:"workers"`
	// workersSet is true when the config named a workers list (even empty).
	workersSet bool
}

// Config is the host-local YAML file (`--config`).
type Config struct {
	// Memory is where records are written (a memory ref); `-m` overrides it.
	Memory string `yaml:"memory"`
	// Host labels this host in each record; empty means the hostname.
	Host     string    `yaml:"host"`
	Accounts []Account `yaml:"accounts"`
}

// LoadConfig reads and validates the config file. Unknown keys are refused so
// a typo cannot silently drop an account.
func LoadConfig(path string) (*Config, error) {
	raw, err := readRegular(path, 1<<20, "config")
	if err != nil {
		return nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, bad("config %s is empty", path)
		}
		return nil, bad("config %s is not valid (unknown key or wrong type); its contents are not echoed", path)
	}
	// One document only: a trailing document (even a malformed one) is refused,
	// so a config that is not wholly valid never reaches a write.
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, bad("config %s must hold exactly one YAML document; its contents are not echoed", path)
	}
	// Re-decode to learn which accounts named a workers list.
	var probe struct {
		Accounts []struct {
			Workers *[]string `yaml:"workers"`
		} `yaml:"accounts"`
	}
	_ = yaml.Unmarshal(raw, &probe)
	for i := range cfg.Accounts {
		if i < len(probe.Accounts) && probe.Accounts[i].Workers != nil {
			cfg.Accounts[i].workersSet = true
		}
	}
	if len(cfg.Accounts) == 0 {
		return nil, bad("config %s lists no accounts", path)
	}
	seen := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		if err := validateLabel(a.Label); err != nil {
			return nil, err
		}
		if seen[a.Label] {
			return nil, bad("config: account label %q appears twice", a.Label)
		}
		seen[a.Label] = true
		if a.Family == "" {
			a.Family = "claude"
		}
		if a.Family != "claude" {
			return nil, bad("config: account %q: family %q is not supported yet (claude only)", a.Label, a.Family)
		}
		if !filepath.IsAbs(a.ConfigDir) {
			return nil, bad("config: account %q: configDir must be an absolute path", a.Label)
		}
		if a.CcusageFile != "" && !filepath.IsAbs(a.CcusageFile) {
			return nil, bad("config: account %q: ccusageFile must be an absolute path", a.Label)
		}
		for _, w := range a.Workers {
			if err := validateWorkerName(a.Label, w); err != nil {
				return nil, err
			}
		}
	}
	return &cfg, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// validateLabel: a label is free text the operator chose, never an email
// address (acceptance 3), so an @ is refused.
func validateLabel(l string) error {
	switch {
	case strings.TrimSpace(l) == "":
		return bad("config: an account has no label")
	case strings.Contains(l, "@"):
		return bad("config: an account label contains @; a label is a local name, never an email address (the label is not echoed)")
	case hasControl(l) || !utf8.ValidString(l):
		return bad("config: an account label contains a control character")
	case len(l) > maxLabelLen:
		return bad("config: an account label is longer than %d bytes", maxLabelLen)
	}
	return nil
}

func validateWorkerName(label, w string) error {
	if strings.TrimSpace(w) == "" || strings.Contains(w, "@") || hasControl(w) || len(w) > maxLabelLen {
		return bad("config: account %q: a workers entry is empty, too long, or not a plain worker name", label)
	}
	return nil
}

// readRegular reads a regular file of at most max bytes. A symlink is refused:
// a link inside a Claude config dir could point at credentials, and the point
// of this reader is that it only ever opens the two files it was told to.
func readRegular(path string, max int64, what string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, bad("%s %s is not a regular file", what, path)
	}
	if fi.Size() > max {
		return nil, bad("%s %s is larger than %d bytes", what, path, max)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, bad("%s %s is larger than %d bytes", what, path, max)
	}
	return b, nil
}

// Window is one rate-limit window as the harness reported it.
type Window struct {
	UsedPct  float64
	ResetsAt time.Time
}

// Snapshot is a validated rate-limits.json. A missing file is "no data":
// Present is false and both windows are nil.
type Snapshot struct {
	Present    bool
	ObservedAt time.Time
	FiveHour   *Window
	SevenDay   *Window
}

type rawWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

type rawSnapshot struct {
	ObservedAt *float64   `json:"observedAt"`
	FiveHour   *rawWindow `json:"five_hour"`
	SevenDay   *rawWindow `json:"seven_day"`
}

func unixSeconds(v float64) (time.Time, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v != math.Trunc(v) || v > 4102444800 { // year 2100
		return time.Time{}, false
	}
	return time.Unix(int64(v), 0).UTC(), true
}

func parseWindow(path, name string, w *rawWindow) (*Window, error) {
	if w == nil {
		return nil, nil
	}
	if w.UsedPercentage == nil || w.ResetsAt == nil {
		return nil, bad("snapshot %s: %s needs both used_percentage and resets_at", path, name)
	}
	p := *w.UsedPercentage
	if math.IsNaN(p) || p < 0 || p > 100 {
		return nil, bad("snapshot %s: %s used_percentage is outside 0-100", path, name)
	}
	t, ok := unixSeconds(*w.ResetsAt)
	if !ok {
		return nil, bad("snapshot %s: %s resets_at is not a Unix time in seconds", path, name)
	}
	return &Window{UsedPct: p, ResetsAt: t}, nil
}

// ReadSnapshot reads one account's snapshot. A file that does not exist yet is
// no data, not an error; anything unreadable or malformed is refused.
func ReadSnapshot(a Account) (Snapshot, error) {
	path := filepath.Join(a.ConfigDir, SnapshotRel)
	raw, err := readRegular(path, maxSnapshotBytes, "snapshot")
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, nil
	}
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return Snapshot{}, err
		}
		return Snapshot{}, bad("snapshot %s cannot be read", path)
	}
	var rs rawSnapshot
	if err := json.Unmarshal(raw, &rs); err != nil {
		return Snapshot{}, bad("snapshot %s is not valid JSON of the documented shape; its contents are not echoed", path)
	}
	if rs.ObservedAt == nil {
		return Snapshot{}, bad("snapshot %s has no observedAt", path)
	}
	obs, ok := unixSeconds(*rs.ObservedAt)
	if !ok {
		return Snapshot{}, bad("snapshot %s: observedAt is not a Unix time in seconds", path)
	}
	s := Snapshot{Present: true, ObservedAt: obs}
	if s.FiveHour, err = parseWindow(path, "five_hour", rs.FiveHour); err != nil {
		return Snapshot{}, err
	}
	if s.SevenDay, err = parseWindow(path, "seven_day", rs.SevenDay); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// Block is one ccusage 5-hour block: only the fields collect uses.
type Block struct {
	Start  time.Time
	Tokens float64
	Cost   float64
}

type rawBlock struct {
	StartTime   string   `json:"startTime"`
	IsGap       bool     `json:"isGap"`
	TotalTokens *float64 `json:"totalTokens"`
	CostUSD     *float64 `json:"costUSD"`
}

// ReadCcusage reads a `ccusage blocks --json` file the operator produced. An
// empty path means none was configured (nil, nil).
func ReadCcusage(a Account) ([]Block, error) {
	if a.CcusageFile == "" {
		return nil, nil
	}
	raw, err := readRegular(a.CcusageFile, maxCcusageBytes, "ccusage file")
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return nil, err
		}
		return nil, bad("ccusage file %s cannot be read", a.CcusageFile)
	}
	var doc struct {
		Blocks []rawBlock `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Blocks == nil {
		return nil, bad("ccusage file %s is not valid `ccusage blocks --json` output; its contents are not echoed", a.CcusageFile)
	}
	var out []Block
	for _, b := range doc.Blocks {
		if b.IsGap {
			continue
		}
		t, terr := time.Parse(time.RFC3339, b.StartTime)
		if terr != nil || b.TotalTokens == nil || b.CostUSD == nil || *b.TotalTokens < 0 || *b.CostUSD < 0 {
			return nil, bad("ccusage file %s has a block without a valid startTime, totalTokens and costUSD", a.CcusageFile)
		}
		out = append(out, Block{Start: t.UTC(), Tokens: *b.TotalTokens, Cost: *b.CostUSD})
	}
	return out, nil
}

// Record is the `account-usage` object's fields (schemaVersion 1). Absent
// values are omitted, never 0. The shape is pinned by a golden test: the
// portal reads it without the CLI.
type Record struct {
	Label               string   `json:"label"`
	Family              string   `json:"family"`
	Host                string   `json:"host"`
	ObservedAt          string   `json:"observedAt"`
	FiveHourPct         *float64 `json:"fiveHourPct,omitempty"`
	FiveHourResetsAt    *string  `json:"fiveHourResetsAt,omitempty"`
	SevenDayPct         *float64 `json:"sevenDayPct,omitempty"`
	SevenDayResetsAt    *string  `json:"sevenDayResetsAt,omitempty"`
	TokensSinceLast     *float64 `json:"tokensSinceLast,omitempty"`
	EstCostUsdSinceLast *float64 `json:"estCostUsdSinceLast,omitempty"`
	// CcusageBlocks is the checkpoint the next collection differences against:
	// the ccusage blocks that could still be growing at observation (started
	// within ActiveWindow of it). Present, possibly empty, whenever a ccusage
	// file was read.
	CcusageBlocks     *[]BlockCheckpoint `json:"ccusageBlocks,omitempty"`
	Workers           []string           `json:"workers"`
	WorkersConfigured bool               `json:"workersConfigured"`
	SchemaVersion     int                `json:"schemaVersion"`
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Prepared is one account's validated input, ready to become a record.
type Prepared struct {
	Account  Account
	Snapshot Snapshot
	Blocks   []Block
}

// Prepare validates every account's input before anything is written. The
// first problem refuses the whole run (nothing is recorded for any account).
func Prepare(cfg *Config) ([]Prepared, error) {
	out := make([]Prepared, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		snap, err := ReadSnapshot(a)
		if err != nil {
			return nil, err
		}
		blocks, err := ReadCcusage(a)
		if err != nil {
			return nil, err
		}
		out = append(out, Prepared{Account: a, Snapshot: snap, Blocks: blocks})
	}
	return out, nil
}

// ActiveWindow bounds how long a ccusage block can keep growing (blocks are
// 5-hour windows; the margin covers a late final write). A block that started
// longer before an observation is final, so the checkpoint omits it.
const ActiveWindow = 6 * time.Hour

// BlockCheckpoint is one still-growing ccusage block's totals.
type BlockCheckpoint struct {
	Start  string  `json:"start"`
	Tokens float64 `json:"tokens"`
	Cost   float64 `json:"costUsd"`
}

// Previous is the checkpoint carried by this account's previous record on this
// host and family: its observation time and recorded blocks (nil when that
// record carried no checkpoint).
type Previous struct {
	ObservedAt time.Time
	Blocks     *[]BlockCheckpoint
}

// BuildRecord shapes one record. observedAt is the snapshot's own observation
// time when there is a snapshot, else now. With a ccusage file the record
// carries its still-growing blocks as the checkpoint. The since-last fields
// are the growth of blocks that started within ActiveWindow of the previous
// observation (a newer block counts in full, a known block by its increase;
// older blocks are final and contribute nothing), so growth inside a running
// block counts and blocks evicted from a rolling file do not matter. They stay
// absent when there is no ccusage file or previous checkpoint, or a known
// block's totals went down: an interval that cannot be measured is unknown,
// not zero.
func BuildRecord(p Prepared, host string, now time.Time, prev Previous) Record {
	r := Record{
		Label: p.Account.Label, Family: p.Account.Family, Host: host,
		ObservedAt: rfc(now), Workers: []string{}, WorkersConfigured: p.Account.workersSet, SchemaVersion: SchemaVersion,
	}
	if p.Account.workersSet {
		r.Workers = append(r.Workers, p.Account.Workers...)
	}
	if p.Snapshot.Present {
		r.ObservedAt = rfc(p.Snapshot.ObservedAt)
		if w := p.Snapshot.FiveHour; w != nil {
			pct, at := w.UsedPct, rfc(w.ResetsAt)
			r.FiveHourPct, r.FiveHourResetsAt = &pct, &at
		}
		if w := p.Snapshot.SevenDay; w != nil {
			pct, at := w.UsedPct, rfc(w.ResetsAt)
			r.SevenDayPct, r.SevenDayResetsAt = &pct, &at
		}
	}
	if p.Blocks != nil {
		observed, _ := time.Parse(time.RFC3339, r.ObservedAt)
		cp := []BlockCheckpoint{}
		for _, b := range p.Blocks {
			if b.Start.After(observed.Add(-ActiveWindow)) {
				cp = append(cp, BlockCheckpoint{Start: rfc(b.Start), Tokens: b.Tokens, Cost: b.Cost})
			}
		}
		r.CcusageBlocks = &cp
		if tok, cost, ok := growth(p.Blocks, prev); ok {
			r.TokensSinceLast, r.EstCostUsdSinceLast = &tok, &cost
		}
	}
	return r
}

// growth is the usage added since prev, or ok=false when it cannot be
// measured.
func growth(blocks []Block, prev Previous) (tok, cost float64, ok bool) {
	if prev.Blocks == nil || prev.ObservedAt.IsZero() {
		return 0, 0, false
	}
	known := map[string]BlockCheckpoint{}
	for _, c := range *prev.Blocks {
		known[c.Start] = c
	}
	cutoff := prev.ObservedAt.Add(-ActiveWindow)
	for _, b := range blocks {
		if !b.Start.After(cutoff) {
			continue
		}
		c, seen := known[rfc(b.Start)]
		if !seen {
			tok += b.Tokens
			cost += b.Cost
			continue
		}
		if b.Tokens < c.Tokens || b.Cost < c.Cost {
			return 0, 0, false
		}
		tok += b.Tokens - c.Tokens
		cost += b.Cost - c.Cost
	}
	return tok, cost, true
}
