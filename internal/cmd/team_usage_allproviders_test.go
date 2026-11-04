package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1659: `team usage` tracks every provider. Fixtures only.

const codexRollout = `{"timestamp":"2026-11-04T11:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":777001}},"rate_limits":{"primary":{"used_percent":15.0,"window_minutes":300,"resets_at":1793810000},"secondary":{"used_percent":30.0,"window_minutes":10080,"resets_at":1794000000},"credits":{"balance":"BALANCE-LEAK"},"plan_type":"PLAN-LEAK"}}}` + "\n" +
	`{"type":"response_item","payload":{"text":"CONVERSATION-LEAK"}}` + "\n"

func mixedFixture(t *testing.T) (cfg string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "codex")
	day := filepath.Join(home, "sessions", "2026", "11", "04")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-11-04T11-00-00-x.jsonl"), []byte(codexRollout), 0o600); err != nil {
		t.Fatal(err)
	}
	zai := filepath.Join(root, "zai")
	_ = os.MkdirAll(zai, 0o755)
	cfg = filepath.Join(root, "usage.yaml")
	body := "memory: hrn:mem:acme.com:team\nhost: orca-1\naccounts:\n" +
		"  - label: cx\n    configDir: " + home + "\n    family: codex\n" +
		"  - label: zai\n    configDir: " + zai + "\n    family: glm\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTeamUsageCollectCodexAndGlmRecords(t *testing.T) {
	cfg := mixedFixture(t)
	srv, us := newUsageServer(t, noObjects)
	out, err := runUsage(t, srv.URL, "collect", "--config", cfg, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(us.creates) != 2 {
		t.Fatalf("want 2 records, got %d", len(us.creates))
	}
	cx, zai := fieldsOf(t, us.creates[0]), fieldsOf(t, us.creates[1])
	if cx["family"] != "codex" || cx["primaryPct"] != 15.0 || cx["primaryMinutes"] != float64(300) || cx["secondaryMinutes"] != float64(10080) || cx["sourceState"] != "ok" {
		t.Errorf("codex record: %v", cx)
	}
	if _, has := cx["fiveHourPct"]; has {
		t.Errorf("codex windows must not be relabelled as 5h/7d: %v", cx)
	}
	if zai["family"] != "glm" || zai["sourceState"] != "no-local-source" || zai["label"] != "zai" {
		t.Errorf("glm record: %v", zai)
	}
	for _, leak := range []string{"BALANCE-LEAK", "PLAN-LEAK", "CONVERSATION-LEAK", "777001", "credits", "plan_type"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q reached --json output", leak)
		}
		if b, _ := os.ReadFile(cfg); strings.Contains(string(b), leak) {
			t.Fatal("fixture leak")
		}
		for _, c := range us.creates {
			if strings.Contains(strings.Join([]string{usageJSON(c)}, ""), leak) {
				t.Errorf("%q reached a written record", leak)
			}
		}
	}
	// Human output carries no leak either.
	srv2, _ := newUsageServer(t, noObjects)
	human, err := runUsage(t, srv2.URL, "collect", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"BALANCE-LEAK", "PLAN-LEAK", "CONVERSATION-LEAK"} {
		if strings.Contains(human, leak) {
			t.Errorf("%q reached the table", leak)
		}
	}
}

func TestTeamUsageShowListsCodexWindowsAndGlmFinding(t *testing.T) {
	objs := []string{
		`{"label":"cx","family":"codex","host":"h","observedAt":"2090-10-31T10:00:00Z","sourceState":"ok","primaryPct":10,"primaryMinutes":300,"primaryResetsAt":"2090-10-31T15:00:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"cx","family":"codex","host":"h","observedAt":"2090-10-31T11:00:00Z","sourceState":"ok","primaryPct":40,"primaryMinutes":300,"primaryResetsAt":"2090-10-31T15:00:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"zai","family":"glm","host":"h","observedAt":"2090-10-31T11:00:00Z","sourceState":"no-local-source","workers":[],"workersConfigured":false,"schemaVersion":1}`,
		`{"label":"cl","family":"claude","host":"h","observedAt":"2090-10-31T11:00:00Z","fiveHourPct":90,"fiveHourResetsAt":"2090-10-31T15:00:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`,
	}
	srv, _ := newUsageServer(t, `{"data":{"findObjects":{"objects":[`+strings.Join(objs, ",")+`],"total":4}}}`)
	out, err := runUsage(t, srv.URL, "show", "-m", "hrn:mem:acme.com:team")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"zai", "no local source", "40% of 300m", "PRIMARY", "SOURCE"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}
	// 30%/h from 10% to 40% projects 100% at 13:00Z, before the 15:00 reset; the
	// 90%-used Claude window has no burn, so it does not rank.
	if !strings.Contains(out, "2090-10-31T13:00:00Z") {
		t.Errorf("codex exhaustion missing:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 7 && f[0] == "cl" && f[7] != "—" {
			t.Errorf("a no-burn 90%% Claude window must not rank: %s", l)
		}
	}
}

func TestTeamUsageClaudeOnlyShowHasNoExtraColumns(t *testing.T) {
	objs := `{"label":"cl","family":"claude","host":"h","observedAt":"2090-10-31T11:00:00Z","fiveHourPct":90,"fiveHourResetsAt":"2090-10-31T15:00:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`
	srv, _ := newUsageServer(t, `{"data":{"findObjects":{"objects":[`+objs+`],"total":1}}}`)
	out, err := runUsage(t, srv.URL, "show", "-m", "hrn:mem:acme.com:team")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "PRIMARY") || strings.Contains(out, "SOURCE") {
		t.Errorf("a Claude-only listing changed:\n%s", out)
	}
	js, _ := runUsage(t, srv.URL, "show", "-m", "hrn:mem:acme.com:team", "--json")
	for _, k := range []string{`"primary"`, `"secondary"`, `"sourceState"`, `"minutes"`} {
		if strings.Contains(js, k) {
			t.Errorf("a Claude account's --json gained %s:\n%s", k, js)
		}
	}
}

func usageJSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}
