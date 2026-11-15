package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/contextsize"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

const health2063OK = `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"u"}],"refused":[]}}}`
const health2063Idle = `[{"contract":"terminal-status@1","worker":"Iris","status":"idle","prompt":"empty","reason":null,"lastTurnAt":null,"checkedAt":"2026-10-31T02:00:00Z"}]`

func health2063Report(t *testing.T, sent map[string]any) map[string]any {
	t.Helper()
	in := sent["input"].(map[string]any)
	return in["reports"].([]any)[0].(map[string]any)
}

func health2063Run(t *testing.T) (map[string]any, string) {
	t.Helper()
	out, n, sent, err := health1181RunFixture(t, health2063OK, 0, true, "", health2063Idle, `{"hostId":"own"}`)
	if err != nil || n != 1 {
		t.Fatalf("err=%v writes=%d", err, n)
	}
	return sent, out
}

func TestHealth2063SendsBothFieldsTogether(t *testing.T) {
	at := time.Date(2026, 10, 31, 1, 59, 0, 0, time.UTC)
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	var gotFamily string
	healthContextMeasurer = func(_ *contextsize.Measurer, family string, _ []string, _ contextsize.Roots) *contextsize.Measurement {
		gotFamily = family
		return &contextsize.Measurement{Tokens: 183412, MeasuredAt: at}
	}
	sent, out := health2063Run(t)
	rep := health2063Report(t, sent)
	if rep["contextTokens"] != float64(183412) || rep["contextMeasuredAt"] != "2026-10-31T01:59:00Z" || gotFamily != "codex" {
		t.Fatalf("report %v family %q", rep, gotFamily)
	}
	if !strings.Contains(out, `"contextTokens": 183412`) {
		t.Fatalf("receipt lacks the reading: %s", out)
	}
}

func TestHealth2063NoMeasurementSendsNeitherField(t *testing.T) {
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	healthContextMeasurer = func(_ *contextsize.Measurer, _ string, _ []string, _ contextsize.Roots) *contextsize.Measurement {
		return nil
	}
	sent, out := health2063Run(t)
	rep := health2063Report(t, sent)
	if _, ok := rep["contextTokens"]; ok {
		t.Fatalf("tokens sent: %v", rep)
	}
	if _, ok := rep["contextMeasuredAt"]; ok {
		t.Fatalf("time sent: %v", rep)
	}
	if strings.Contains(out, "contextTokens") {
		t.Fatalf("receipt: %s", out)
	}
}

// End to end through the real files: the request carries numbers only, never a
// byte of transcript text (the request-log pin of #2063).
func TestHealth2063RequestCarriesNoTranscriptText(t *testing.T) {
	codex := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	// The fixture's worktrees_root is its own temp dir; find it through the reader.
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	healthContextMeasurer = func(_ *contextsize.Measurer, family string, trees []string, roots contextsize.Roots) *contextsize.Measurement {
		if len(trees) != 1 {
			t.Fatalf("trees %v", trees)
		}
		p := filepath.Join(codex, "sessions", "2026", "10", "31", "rollout-1.jsonl")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		body := `{"timestamp":"2026-10-31T01:00:00Z","type":"session_meta","payload":{"id":"x","cwd":` + jsonStr(trees[0]) + `}}` + "\n" +
			`{"timestamp":"2026-10-31T01:30:00Z","type":"response_item","payload":{"type":"message","content":[{"text":"PRIVATE_TRANSCRIPT_TEXT"}]}}` + "\n" +
			`{"timestamp":"2026-10-31T01:31:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":777}}}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return contextsize.Measure(family, trees, roots, time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC))
	}
	sent, out := health2063Run(t)
	rep := health2063Report(t, sent)
	if rep["contextTokens"] != float64(777) || rep["contextMeasuredAt"] != "2026-10-31T01:31:00Z" {
		t.Fatalf("report %v", rep)
	}
	raw, _ := json.Marshal(sent)
	if strings.Contains(string(raw), "PRIVATE_TRANSCRIPT_TEXT") || strings.Contains(out, "PRIVATE_TRANSCRIPT_TEXT") {
		t.Fatalf("transcript text leaked: %s %s", raw, out)
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

// A server from before #4238 refuses the whole batch for the unknown pair; the
// sweep must still store terminal health, without the pair, in one retry.
func TestHealth2063OldServerStillStoresTerminalHealth(t *testing.T) {
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	healthContextMeasurer = func(_ *contextsize.Measurer, _ string, _ []string, _ contextsize.Roots) *contextsize.Measurement {
		return &contextsize.Measurement{Tokens: 100, MeasuredAt: time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC)}
	}
	out, n, sent, err := health1181RunFixture(t, "OLDSERVER", 0, true, "", health2063Idle, `{"hostId":"own"}`)
	if err != nil || n != 2 {
		t.Fatalf("err=%v writes=%d (want one refused attempt then one store)", err, n)
	}
	rep := health2063Report(t, sent)
	if _, ok := rep["contextTokens"]; ok {
		t.Fatalf("retry still carries the pair: %v", rep)
	}
	if _, ok := rep["contextMeasuredAt"]; ok || strings.Contains(out, "contextTokens") {
		t.Fatalf("retry/receipt still carries the pair: %v %s", rep, out)
	}
}

// Any other failure is not retried, and an unmeasured batch is never retried.
func TestHealth2063OnlyTheUnknownFieldRefusalIsRetried(t *testing.T) {
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	healthContextMeasurer = func(_ *contextsize.Measurer, _ string, _ []string, _ contextsize.Roots) *contextsize.Measurement {
		return nil
	}
	_, n, _, err := health1181RunFixture(t, "OLDSERVER_ALWAYS", 0, true, "", health2063Idle, `{"hostId":"own"}`)
	if err == nil || n != 1 {
		t.Fatalf("err=%v writes=%d (an unmeasured batch must not be retried)", err, n)
	}
}

// Julius P2 / Rook F2: only the exact pre-execution refusal is retried. A
// resolver or auth error may follow a write, so a measured batch is sent once.
func TestHealth2063ExecutionErrorsAreNeverRetried(t *testing.T) {
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	healthContextMeasurer = func(_ *contextsize.Measurer, _ string, _ []string, _ contextsize.Roots) *contextsize.Measurement {
		return &contextsize.Measurement{Tokens: 100, MeasuredAt: time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC)}
	}
	for name, wire := range map[string]string{
		"resolver text": `RAW:{"data":null,"errors":[{"message":"contextTokens: unknown field in storage result","path":["setHostWorkerTerminals"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		"forbidden":     `RAW:{"data":null,"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
		"mixed":         `RAW:{"data":null,"errors":[{"message":"Field \"contextTokens\" is not defined by type \"WorkerTerminalReportInput\"."},{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	} {
		_, n, _, err := health1181RunFixture(t, wire, 0, true, "", health2063Idle, `{"hostId":"own"}`)
		if err == nil || n != 1 {
			t.Errorf("%s: err=%v attempts=%d, want one failed attempt", name, err, n)
		}
	}
}

// Jill P2: on a host whose Codex workers write under an account home, the
// worker's own codex_home decides where the rollout is read, not the sweep's env.
func TestHealth2063WorkerCodexHomeDecidesTheRoot(t *testing.T) {
	account := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir()) // the sweep's own home: empty
	health1181WorkerExtra = "    codex_home: " + jsonStr(account) + "\n"
	defer func() { health1181WorkerExtra = "" }()
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old }()
	healthContextMeasurer = func(_ *contextsize.Measurer, family string, trees []string, roots contextsize.Roots) *contextsize.Measurement {
		if roots.CodexSessions != filepath.Join(account, "sessions") {
			t.Errorf("codex root %q, want the worker's account home", roots.CodexSessions)
		}
		p := filepath.Join(roots.CodexSessions, "2026", "10", "31", "rollout-a.jsonl")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		body := `{"timestamp":"2026-10-31T01:00:00Z","type":"session_meta","payload":{"cwd":` + jsonStr(trees[0]) + `}}` + "\n" +
			`{"timestamp":"2026-10-31T01:31:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":4242}}}}` + "\n"
		_ = os.WriteFile(p, []byte(body), 0o600)
		return contextsize.Measure(family, trees, roots, time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC))
	}
	sent, _ := health2063Run(t)
	if rep := health2063Report(t, sent); rep["contextTokens"] != float64(4242) {
		t.Fatalf("report %v", rep)
	}
}

func TestHealth2063RootsDefaultWhenWorkerConfiguresNone(t *testing.T) {
	t.Setenv("CODEX_HOME", "/sweep/codex")
	t.Setenv("CLAUDE_CONFIG_DIR", "/sweep/claude")
	got := healthContextRoots(hostpoller.WorkerConfig{})
	if got.CodexSessions != "/sweep/codex/sessions" || got.ClaudeProjects != "/sweep/claude/projects" {
		t.Fatalf("%+v", got)
	}
	got = healthContextRoots(hostpoller.WorkerConfig{CodexHome: "/a/h", ClaudeConfigDir: "/a/c"})
	if got.CodexSessions != "/a/h/sessions" || got.ClaudeProjects != "/a/c/projects" {
		t.Fatalf("%+v", got)
	}
}

// Discovery belongs to one invocation, shared by all its workers and discarded
// before the next invocation; otherwise a row-local cache still scans per worker.
func TestHealth2095SharesMeasurerWithinSweepOnly(t *testing.T) {
	old := healthContextMeasurer
	defer func() { healthContextMeasurer = old; health1181WorkerExtra = "" }()
	health1181WorkerExtra = "  w2:\n    family: codex\n    launch: [fake]\n    worktrees: [jane]\n"
	regs := `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"w1","worker":{"id":"w1","name":"Iris"},"worktrees":["iris"]},{"workerId":"w2","worker":{"id":"w2","name":"Jane"},"worktrees":["jane"]}]}]}}`
	status := strings.TrimSuffix(health2063Idle, "]") + "," + strings.TrimPrefix(strings.ReplaceAll(health2063Idle, "Iris", "Jane"), "[")
	wire := `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"u1"},{"id":"w2","urn":"u2"}],"refused":[]}}}`
	var seen [2][]*contextsize.Measurer
	for i := range seen {
		healthContextMeasurer = func(m *contextsize.Measurer, _ string, _ []string, _ contextsize.Roots) *contextsize.Measurement {
			seen[i] = append(seen[i], m)
			return nil
		}
		_, n, _, err := health1181RunFixture(t, wire, 0, true, regs, status, `{"hostId":"own"}`)
		if err != nil || n != 1 {
			t.Fatalf("sweep %d: writes=%d err=%v", i, n, err)
		}
		if len(seen[i]) != 2 || seen[i][0] == nil || seen[i][0] != seen[i][1] {
			t.Fatalf("sweep %d did not share one measurer: %v", i, seen[i])
		}
	}
	if seen[0][0] == seen[1][0] {
		t.Fatal("a new sweep must not retain the previous sweep's candidate cache")
	}
}
