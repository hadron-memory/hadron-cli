package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/contextsize"
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
	healthContextMeasurer = func(family string, _ []string) *contextsize.Measurement {
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
	healthContextMeasurer = func(string, []string) *contextsize.Measurement { return nil }
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
	healthContextMeasurer = func(family string, trees []string) *contextsize.Measurement {
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
		return contextsize.Measure(family, trees, contextsize.DefaultRoots(), time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC))
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
	healthContextMeasurer = func(string, []string) *contextsize.Measurement {
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
	healthContextMeasurer = func(string, []string) *contextsize.Measurement { return nil }
	_, n, _, err := health1181RunFixture(t, "OLDSERVER_ALWAYS", 0, true, "", health2063Idle, `{"hostId":"own"}`)
	if err == nil || n != 1 {
		t.Fatalf("err=%v writes=%d (an unmeasured batch must not be retried)", err, n)
	}
}
