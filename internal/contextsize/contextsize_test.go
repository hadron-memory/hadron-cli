package contextsize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeRec(cwd, ts string, in, read, create int, extra string) string {
	return `{"type":"assistant","cwd":"` + cwd + `","timestamp":"` + ts + `","message":{"model":"claude-x","content":[{"type":"text","text":"SECRET TRANSCRIPT TEXT"}],"usage":{"input_tokens":` +
		itoa(in) + `,"cache_read_input_tokens":` + itoa(read) + `,"cache_creation_input_tokens":` + itoa(create) + `}}` + extra + "}\n"
}
func itoa(n int) string { return fmtInt(n) }
func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestClaudeSumsInputAndCacheOfLastTurn(t *testing.T) {
	wt := t.TempDir()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, claudeRec(wt, "2026-11-15T10:00:00.000Z", 1, 10, 100, "")+
		claudeRec(wt, "2026-11-15T10:05:00.000Z", 2, 5699, 74805, "")+
		`{"type":"user","message":{"content":"hi"}}`+"\n")
	m, ok := ClaudeFromFile(p, wt, now)
	if !ok || m.Tokens != 80506 || !m.MeasuredAt.Equal(time.Date(2026, 11, 15, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("got %+v %v", m, ok)
	}
}

func TestClaudeSkipsSidechainAndZeroAndSynthetic(t *testing.T) {
	wt := t.TempDir()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, claudeRec(wt, "2026-11-15T10:00:00Z", 3, 0, 7, "")+
		claudeRec(wt, "2026-11-15T10:05:00Z", 9, 9, 9, `,"isSidechain":true`)+
		claudeRec(wt, "2026-11-15T10:06:00Z", 0, 0, 0, ""))
	m, ok := ClaudeFromFile(p, wt, now)
	if !ok || m.Tokens != 10 {
		t.Fatalf("got %+v %v", m, ok)
	}
}

func TestClaudeNeverZeroAndRefusesBadInput(t *testing.T) {
	wt := t.TempDir()
	d := t.TempDir()
	cases := map[string]string{
		"zero only":     claudeRec(wt, "2026-11-15T10:00:00Z", 0, 0, 0, ""),
		"malformed":     "{not json\n{\"type\":\n",
		"empty":         "",
		"wrong cwd":     claudeRec("/elsewhere", "2026-11-15T10:00:00Z", 1, 2, 3, ""),
		"future":        claudeRec(wt, "2026-11-15T12:05:00Z", 1, 2, 3, ""),
		"bad timestamp": claudeRec(wt, "yesterday", 1, 2, 3, ""),
		"no newline":    strings.TrimSuffix(claudeRec(wt, "2026-11-15T10:00:00Z", 1, 2, 3, ""), "\n"),
	}
	for name, body := range cases {
		p := filepath.Join(d, strings.ReplaceAll(name, " ", "_")+".jsonl")
		write(t, p, body)
		if m, ok := ClaudeFromFile(p, wt, now); ok || m != nil {
			t.Errorf("%s: want none, got %+v", name, m)
		}
	}
	if m, ok := ClaudeFromFile(filepath.Join(d, "missing.jsonl"), wt, now); ok || m != nil {
		t.Errorf("missing: got %+v", m)
	}
}

func TestClaudeSymlinkRefused(t *testing.T) {
	wt := t.TempDir()
	d := t.TempDir()
	real := filepath.Join(d, "real.jsonl")
	write(t, real, claudeRec(wt, "2026-11-15T10:00:00Z", 1, 2, 3, ""))
	link := filepath.Join(d, "link.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	if m, ok := ClaudeFromFile(link, wt, now); ok || m != nil {
		t.Fatalf("symlink followed: %+v", m)
	}
}

func codexMeta(cwd string) string {
	return `{"timestamp":"2026-11-15T09:00:00Z","type":"session_meta","payload":{"id":"x","cwd":"` + cwd + `"}}` + "\n"
}
func codexTok(ts string, in, total int) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` + fmtInt(total) +
		`},"last_token_usage":{"input_tokens":` + fmtInt(in) + `,"output_tokens":5}}}}` + "\n"
}

func TestCodexLastTurnInputNotTotal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-a.jsonl")
	write(t, p, codexMeta("/w")+codexTok("2026-11-15T10:00:00Z", 100, 100)+codexTok("2026-11-15T10:30:00Z", 250, 350)+
		`{"timestamp":"2026-11-15T10:31:00Z","type":"event_msg","payload":{"type":"token_count","info":null}}`+"\n")
	m, ok := CodexFromFile(p, now)
	if !ok || m.Tokens != 250 || !m.MeasuredAt.Equal(time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("got %+v %v", m, ok)
	}
}

func TestCodexBadInputSendsNothing(t *testing.T) {
	d := t.TempDir()
	for name, body := range map[string]string{
		"malformed": "garbage\n",
		"zero":      codexTok("2026-11-15T10:00:00Z", 0, 0),
		"future":    codexTok("2026-11-15T13:00:00Z", 5, 5),
	} {
		p := filepath.Join(d, name+".jsonl")
		write(t, p, body)
		if m, ok := CodexFromFile(p, now); ok || m != nil {
			t.Errorf("%s: got %+v", name, m)
		}
	}
}

func TestMeasureFindsFilesByWorktreeAndFamily(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(t.TempDir(), "w.t")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := Roots{ClaudeProjects: filepath.Join(root, "projects"), CodexSessions: filepath.Join(root, "sessions")}
	write(t, filepath.Join(claudeProjectDir(roots.ClaudeProjects, wt), "a.jsonl"), claudeRec(wt, "2026-11-15T10:00:00Z", 1, 2, 3, ""))
	write(t, filepath.Join(roots.CodexSessions, "2026/11/15/rollout-1.jsonl"), codexMeta(wt)+codexTok("2026-11-15T10:10:00Z", 42, 42))
	write(t, filepath.Join(roots.CodexSessions, "2026/11/15/rollout-2.jsonl"), codexMeta("/other")+codexTok("2026-11-15T10:20:00Z", 7, 7))

	if m := Measure("claude", []string{wt}, roots, now); m == nil || m.Tokens != 6 {
		t.Fatalf("claude: %+v", m)
	}
	if m := Measure("Codex", []string{wt}, roots, now); m == nil || m.Tokens != 42 {
		t.Fatalf("codex: %+v", m)
	}
	for _, fam := range []string{"glm", "kimi", "", "gemini"} {
		if m := Measure(fam, []string{wt}, roots, now); m != nil {
			t.Errorf("family %q must have no measurer, got %+v", fam, m)
		}
	}
	if m := Measure("claude", []string{filepath.Join(wt, "nope")}, roots, now); m != nil {
		t.Errorf("unknown worktree: %+v", m)
	}
}

func TestCodexZeroTurnFallsBackToPreviousNeverZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-z.jsonl")
	write(t, p, codexTok("2026-11-15T10:00:00Z", 100, 100)+codexTok("2026-11-15T10:10:00Z", 0, 100))
	m, ok := CodexFromFile(p, now)
	if !ok || m.Tokens != 100 {
		t.Fatalf("got %+v %v", m, ok)
	}
}

func TestOverRangeReadingIsNeverReported(t *testing.T) {
	wt := t.TempDir()
	d := t.TempDir()
	p := filepath.Join(d, "c.jsonl")
	write(t, p, claudeRec(wt, "2026-11-15T10:00:00Z", 2147483648, 0, 0, ""))
	if m, ok := ClaudeFromFile(p, wt, now); ok || m != nil {
		t.Fatalf("claude over int32: %+v", m)
	}
	q := filepath.Join(d, "x.jsonl")
	write(t, q, codexTok("2026-11-15T10:00:00Z", 2147483648, 1))
	if m, ok := CodexFromFile(q, now); ok || m != nil {
		t.Fatalf("codex over int32: %+v", m)
	}
	r := filepath.Join(d, "ok.jsonl")
	write(t, r, claudeRec(wt, "2026-11-15T10:00:00Z", 2147483647, 0, 0, ""))
	if m, ok := ClaudeFromFile(r, wt, now); !ok || m.Tokens != 2147483647 {
		t.Fatalf("the bound itself is valid: %+v", m)
	}
}
