package accountusage

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures only: no real rollout, account or network (cli#1659).

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "usage.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func rollout(t *testing.T, home, day, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tokenCount(ts string, pPct float64, pMin int, pReset int64, sPct float64, sReset int64) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":123456}},"rate_limits":{"primary":{"used_percent":` + ftoa(pPct) + `,"window_minutes":` + itoa(pMin) + `,"resets_at":` + itoa64(pReset) + `},"secondary":{"used_percent":` + ftoa(sPct) + `,"window_minutes":10080,"resets_at":` + itoa64(sReset) + `},"credits":{"balance":"42.5"},"plan_type":"pro"}}}` + "\n"
}

func ftoa(f float64) string { b, _ := json.Marshal(f); return string(b) }
func itoa(i int) string     { return ftoa(float64(i)) }
func itoa64(i int64) string { b, _ := json.Marshal(i); return string(b) }

func codexAcct(home string) Account {
	return Account{Label: "cx", ConfigDir: home, Family: "codex"}
}

func TestConfigAcceptsCodexAndGlmButNotOthers(t *testing.T) {
	for _, fam := range []string{"codex", "glm"} {
		cfg, err := LoadConfig(writeCfg(t, "accounts:\n  - label: a\n    configDir: /x/home\n    family: "+fam+"\n"))
		if err != nil || cfg.Accounts[0].Family != fam {
			t.Fatalf("%s: %v", fam, err)
		}
	}
	if _, err := LoadConfig(writeCfg(t, "accounts:\n  - label: a\n    configDir: /x/home\n    family: zai\n")); err == nil {
		t.Fatal("an unknown family must be refused")
	}
	if _, err := LoadConfig(writeCfg(t, "accounts:\n  - label: a\n    configDir: /x/../home\n    family: codex\n")); err == nil {
		t.Fatal("a codex home that is not a clean absolute path must be refused")
	}
}

func TestCodexReadsLatestTokenCountWithOwnMinutes(t *testing.T) {
	home := t.TempDir()
	body := tokenCount("2026-11-04T10:00:00.000Z", 10, 300, 1793800000, 5, 1794000000) +
		`not json at all SECRETLINE` + "\n" +
		`{"timestamp":"2026-11-04T10:30:00.000Z","type":"response_item","payload":{"text":"CONVERSATION-TEXT"}}` + "\n" +
		tokenCount("2026-11-04T11:00:00.000Z", 15, 300, 1793810000, 30, 1794000000)
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T10-00-00-aaa.jsonl", body)
	s, err := ReadCodex(codexAcct(home))
	if err != nil || !s.Present {
		t.Fatalf("%v %+v", err, s)
	}
	if s.Primary == nil || s.Primary.UsedPct != 15 || s.Primary.Minutes != 300 || s.Primary.ResetsAt.Unix() != 1793810000 {
		t.Fatalf("primary %+v", s.Primary)
	}
	if s.Secondary == nil || s.Secondary.UsedPct != 30 || s.Secondary.Minutes != 10080 {
		t.Fatalf("secondary %+v", s.Secondary)
	}
	if s.ObservedAt.Format(time.RFC3339) != "2026-11-04T11:00:00Z" {
		t.Fatalf("observedAt %v", s.ObservedAt)
	}
}

func TestCodexNewestRolloutWinsOverOlder(t *testing.T) {
	home := t.TempDir()
	rollout(t, home, "2026/11/03", "rollout-2026-11-03T09-00-00-old.jsonl", tokenCount("2026-11-03T09:00:00.000Z", 90, 300, 1793700000, 90, 1793900000))
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T08-00-00-mid.jsonl", tokenCount("2026-11-04T08:00:00.000Z", 40, 60, 1793800000, 40, 1794000000))
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T09-00-00-new.jsonl", tokenCount("2026-11-04T09:00:00.000Z", 7, 300, 1793810000, 8, 1794000000))
	s, _ := ReadCodex(codexAcct(home))
	if s.Primary == nil || s.Primary.UsedPct != 7 {
		t.Fatalf("%+v", s.Primary)
	}
}

func TestCodexNoTokenCountIsNoReadingNotZero(t *testing.T) {
	home := t.TempDir()
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T09-00-00-x.jsonl", `{"type":"session_meta","payload":{"id":"x"}}`+"\n")
	s, err := ReadCodex(codexAcct(home))
	if err != nil || s.Present || s.Primary != nil || s.Secondary != nil {
		t.Fatalf("%v %+v", err, s)
	}
	rec := BuildRecord(Prepared{Account: codexAcct(home), Codex: s}, "h", time.Unix(1793800000, 0), Previous{})
	if rec.SourceState != "no-reading-yet" || rec.PrimaryPct != nil || rec.SecondaryPct != nil {
		t.Fatalf("%+v", rec)
	}
	if empty, _ := ReadCodex(codexAcct(t.TempDir())); empty.Present {
		t.Fatal("a home with no sessions is no reading")
	}
}

func TestCodexSkipsMalformedAndOversizedLinesWithoutEchoing(t *testing.T) {
	home := t.TempDir()
	huge := `{"timestamp":"2026-11-04T12:00:00.000Z","type":"event_msg","payload":{"type":"token_count","pad":"` + strings.Repeat("A", 3<<20) + `","rate_limits":{"primary":{"used_percent":99,"window_minutes":300,"resets_at":1793810000}}}}` + "\n"
	bad := `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":150,"window_minutes":300,"resets_at":1793810000}}}}` + "\n"
	body := tokenCount("2026-11-04T09:00:00.000Z", 12, 300, 1793810000, 13, 1794000000) + huge + bad + "{truncated\n"
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T09-00-00-x.jsonl", body)
	s, err := ReadCodex(codexAcct(home))
	if err != nil {
		t.Fatal(err)
	}
	if s.Primary == nil || s.Primary.UsedPct != 12 {
		t.Fatalf("a skipped line must leave the last valid reading: %+v", s.Primary)
	}
}

func TestCodexRefusesSymlinksAndNonRegularWithoutRawPath(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	rollout(t, outside, "2026/11/04", "rollout-2026-11-04T09-00-00-out.jsonl", tokenCount("2026-11-04T09:00:00.000Z", 99, 300, 1793810000, 99, 1794000000))
	// a day directory that is a symlink to a path outside the home
	if err := os.MkdirAll(filepath.Join(home, "sessions", "2026", "11"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "sessions", "2026", "11", "04"), filepath.Join(home, "sessions", "2026", "11", "04")); err != nil {
		t.Fatal(err)
	}
	s, err := ReadCodex(codexAcct(home))
	if err != nil || s.Present {
		t.Fatalf("a symlinked day dir must not be followed: %v %+v", err, s)
	}
	// a rollout file that is a symlink, and a directory named like a rollout
	dir := filepath.Join(home, "sessions", "2026", "11", "05")
	_ = os.MkdirAll(dir, 0o755)
	target := filepath.Join(outside, "sessions", "2026", "11", "04", "rollout-2026-11-04T09-00-00-out.jsonl")
	_ = os.Symlink(target, filepath.Join(dir, "rollout-2026-11-05T09-00-00-link.jsonl"))
	_ = os.MkdirAll(filepath.Join(dir, "rollout-2026-11-05T10-00-00-dir.jsonl"), 0o755)
	s, err = ReadCodex(codexAcct(home))
	if err != nil || s.Present {
		t.Fatalf("%v %+v", err, s)
	}
	// a symlinked sessions dir
	home2 := t.TempDir()
	_ = os.Symlink(filepath.Join(outside, "sessions"), filepath.Join(home2, "sessions"))
	if s, err = ReadCodex(codexAcct(home2)); err != nil || s.Present {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestCodexRecordStoresOnlyWindowFields(t *testing.T) {
	home := t.TempDir()
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T09-00-00-x.jsonl", tokenCount("2026-11-04T09:00:00.000Z", 15, 300, 1793810000, 30, 1794000000))
	s, _ := ReadCodex(codexAcct(home))
	rec := BuildRecord(Prepared{Account: codexAcct(home), Codex: s}, "h", time.Unix(1793800000, 0), Previous{})
	b, _ := json.Marshal(rec)
	out := string(b)
	for _, banned := range []string{"42.5", "balance", "credits", "plan_type", "pro", "123456", "input_tokens", "CONVERSATION"} {
		if strings.Contains(out, banned) {
			t.Fatalf("%q leaked into the record: %s", banned, out)
		}
	}
	if rec.SourceState != "ok" || rec.PrimaryMinutes == nil || *rec.PrimaryMinutes != 300 || rec.SecondaryMinutes == nil || *rec.SecondaryMinutes != 10080 {
		t.Fatalf("%+v", rec)
	}
	if rec.FiveHourPct != nil || rec.SevenDayPct != nil {
		t.Fatal("codex windows must not be relabelled as the 5h/7d pair")
	}
}

func TestGlmRecordsNoLocalSource(t *testing.T) {
	a := Account{Label: "zai", ConfigDir: t.TempDir(), Family: "glm"}
	p, err := Prepare(&Config{Accounts: []Account{a}})
	if err != nil {
		t.Fatal(err)
	}
	rec := BuildRecord(p[0], "h", time.Unix(1793800000, 0), Previous{})
	if rec.SourceState != "no-local-source" || rec.Label != "zai" || rec.Family != "glm" || rec.PrimaryPct != nil {
		t.Fatalf("%+v", rec)
	}
}

func TestClaudeRecordIsByteIdentical(t *testing.T) {
	rec := BuildRecord(Prepared{Account: Account{Label: "c", Family: "claude"}}, "h", time.Unix(1793800000, 0), Previous{})
	b, _ := json.Marshal(rec)
	want := `{"label":"c","family":"claude","host":"h","observedAt":"2026-11-04T13:46:40Z","workers":[],"workersConfigured":false,"schemaVersion":1}`
	if string(b) != want {
		t.Fatalf("claude record changed:\n%s\n%s", b, want)
	}
}

func rec(label, fam string, extra map[string]any) json.RawMessage {
	m := map[string]any{"label": label, "family": fam, "host": "h", "workers": []string{}, "schemaVersion": 1}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func TestShowViewCarriesCodexWindowsMinutesAndSourceState(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	t0, t1 := "2026-11-04T10:00:00Z", "2026-11-04T11:00:00Z"
	reset := "2026-11-05T10:00:00Z"
	objs := []json.RawMessage{
		rec("cx", "codex", map[string]any{"observedAt": t0, "sourceState": "ok", "primaryPct": 10.0, "primaryMinutes": 300, "primaryResetsAt": reset}),
		rec("cx", "codex", map[string]any{"observedAt": t1, "sourceState": "ok", "primaryPct": 30.0, "primaryMinutes": 300, "primaryResetsAt": reset}),
		rec("zai", "glm", map[string]any{"observedAt": t1, "sourceState": "no-local-source"}),
		rec("cl", "claude", map[string]any{"observedAt": t1, "fiveHourPct": 50.0, "fiveHourResetsAt": reset}),
	}
	vs := View(objs, now)
	by := map[string]AccountView{}
	for _, v := range vs {
		by[v.Label] = v
	}
	cx := by["cx"]
	if cx.Primary == nil || cx.Primary.UsedPct == nil || *cx.Primary.UsedPct != 30 || cx.Primary.Minutes == nil || *cx.Primary.Minutes != 300 || cx.Primary.BurnPctPerHour == nil || *cx.Primary.BurnPctPerHour != 20 {
		t.Fatalf("%+v", cx.Primary)
	}
	if by["zai"].SourceState != "no-local-source" {
		t.Fatalf("%+v", by["zai"])
	}
	b, _ := json.Marshal(by["cl"])
	if strings.Contains(string(b), "primary") || strings.Contains(string(b), "sourceState") || strings.Contains(string(b), "minutes") {
		t.Fatalf("a claude view must not gain keys: %s", b)
	}
}

// ---- #1661 fix head: BLIND + LOCAL findings (pins) ----

func recAt(pct float64, resets string, min int) Record {
	p, r, m := pct, resets, min
	return Record{PrimaryPct: &p, PrimaryResetsAt: &r, PrimaryMinutes: &m}
}

func burnPts(recs ...Record) []point {
	base := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	var out []point
	for i, r := range recs {
		out = append(out, point{rec: r, at: base.Add(time.Duration(i) * time.Hour)})
	}
	return out
}

func primaryView(pts []point) WindowView {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	return windowView(pts, now, func(r Record) (*float64, *string) { return r.PrimaryPct, r.PrimaryResetsAt }, func(r Record) *int { return r.PrimaryMinutes })
}

const sameReset = "2026-11-05T10:00:00Z"

func TestBurnNeverCombinesWindowsOfDifferentLength(t *testing.T) {
	v := primaryView(burnPts(recAt(10, sameReset, 300), recAt(30, sameReset, 10080)))
	if v.BurnPctPerHour != nil || v.ExhaustsAt != nil {
		t.Fatalf("a 300-minute and a 10080-minute window must not form one rate: %+v", v)
	}
	if v.Minutes == nil || *v.Minutes != 10080 {
		t.Fatalf("the current window length must still be shown: %+v", v.Minutes)
	}
}

func TestBurnNeverCombinesAbsentWithPresentMinutes(t *testing.T) {
	old := recAt(10, sameReset, 300)
	old.PrimaryMinutes = nil
	v := primaryView(burnPts(old, recAt(30, sameReset, 300)))
	if v.BurnPctPerHour != nil {
		t.Fatalf("a reading without a length must not combine with one that has it: %+v", v)
	}
}

func TestBurnStillCombinesSameLengthSameReset(t *testing.T) {
	v := primaryView(burnPts(recAt(10, sameReset, 300), recAt(30, sameReset, 300)))
	if v.BurnPctPerHour == nil || *v.BurnPctPerHour != 20 {
		t.Fatalf("same reset and length must still give a rate: %+v", v.BurnPctPerHour)
	}
}

func TestBurnClaudeWindowsWithoutMinutesUnchanged(t *testing.T) {
	a, b := 10.0, 30.0
	r := sameReset
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	v := windowView(burnPts(Record{FiveHourPct: &a, FiveHourResetsAt: &r}, Record{FiveHourPct: &b, FiveHourResetsAt: &r}), now,
		func(r Record) (*float64, *string) { return r.FiveHourPct, r.FiveHourResetsAt }, nil)
	if v.BurnPctPerHour == nil || *v.BurnPctPerHour != 20 {
		t.Fatalf("Claude burn must be unchanged: %+v", v.BurnPctPerHour)
	}
}

func oneRollout(t *testing.T) (string, string) {
	home := t.TempDir()
	rollout(t, home, "2026/11/04", "rollout-2026-11-04T09-00-00-a.jsonl", tokenCount("2026-11-04T09:00:00.000Z", 12, 300, 1793810000, 40, 1794000000))
	return home, filepath.Join(home, "sessions", "2026", "11", "04", "rollout-2026-11-04T09-00-00-a.jsonl")
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, os.ErrPermission }

func TestCodexReadErrorIsAnErrorNotAReading(t *testing.T) {
	home, _ := oneRollout(t)
	wrapRolloutReader = func(io.Reader) io.Reader { return failingReader{} }
	defer func() { wrapRolloutReader = func(r io.Reader) io.Reader { return r } }()
	s, err := ReadCodex(codexAcct(home))
	if err == nil || s.Present {
		t.Fatalf("a read failure must be an error, never a partial reading: %v %+v", err, s)
	}
	if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), "rollout") {
		t.Fatalf("the error must not echo a path: %v", err)
	}
}

func TestCodexUnreadableDirectoryIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind root")
	}
	home, _ := oneRollout(t)
	day := filepath.Join(home, "sessions", "2026", "11", "04")
	if err := os.Chmod(day, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(day, 0o755) }()
	s, err := ReadCodex(codexAcct(home))
	if err == nil || s.Present {
		t.Fatalf("an unreadable day directory is an operational failure, not no-reading-yet: %v %+v", err, s)
	}
	if strings.Contains(err.Error(), home) {
		t.Fatalf("the error must not echo a path: %v", err)
	}
}

func TestCodexMissingSourceStaysNoReading(t *testing.T) {
	s, err := ReadCodex(codexAcct(filepath.Join(t.TempDir(), "absent")))
	if err != nil || s.Present {
		t.Fatalf("a missing home is no data, not an error: %v %+v", err, s)
	}
}

func TestCodexReadIsBoundedAfterTheSizeCheck(t *testing.T) {
	home, path := oneRollout(t)
	afterRolloutStat = func() {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		// grow the file past the cap after the size check passed
		chunk := make([]byte, 1<<20)
		for i := range chunk {
			chunk[i] = 'x'
		}
		for n := 0; n <= maxRolloutBytes>>20; n++ {
			if _, err := f.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() { afterRolloutStat = func() {} }()
	_, err := ReadCodex(codexAcct(home))
	if err == nil {
		t.Fatal("a file that grows past the cap while read must be refused")
	}
	if strings.Contains(err.Error(), home) {
		t.Fatalf("the size refusal must not echo a path: %v", err)
	}
}

func TestCodexRefusesAFileSwappedForASymlinkBetweenCheckAndOpen(t *testing.T) {
	home, path := oneRollout(t)
	outside := t.TempDir()
	rollout(t, outside, "2026/11/04", "x.jsonl", tokenCount("2026-11-04T09:00:00.000Z", 99, 300, 1793810000, 99, 1794000000))
	afterRolloutStat = func() {
		_ = os.Remove(path)
		if err := os.Symlink(filepath.Join(outside, "sessions", "2026", "11", "04", "x.jsonl"), path); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { afterRolloutStat = func() {} }()
	s, err := ReadCodex(codexAcct(home))
	if s.Present && s.Primary != nil && s.Primary.UsedPct == 99 {
		t.Fatalf("followed a swapped-in symlink: %v", err)
	}
}

func TestBurnNeverCombinesAcrossAReset(t *testing.T) {
	v := primaryView(burnPts(recAt(90, "2026-11-04T10:30:00Z", 300), recAt(5, sameReset, 300)))
	if v.BurnPctPerHour != nil {
		t.Fatalf("a reset starts a new window: %+v", v.BurnPctPerHour)
	}
}
