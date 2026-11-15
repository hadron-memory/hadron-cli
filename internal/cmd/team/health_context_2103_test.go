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

// A numeric-only projection of a real orca1 claude-glm / glm-5.3 record.
// Cwd is replaced; content, session ids and other fields are omitted.
func health2103Transcript(t *testing.T, root, cwd string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/glm-context-2103.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	rec["cwd"] = cwd
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, strings.NewReplacer("/", "-", "\\", "-", ":", "-", ".", "-").Replace(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHealth2103SelectsTranscriptByTUI(t *testing.T) {
	for _, tc := range []struct {
		family, tui string
		want        bool
	}{
		{"glm", "claude", true}, {"kimi", "claude", true}, {"codex", "claude", true},
		{"claude", "", true}, {"codex", "", false}, {"glm", "", false}, {"kimi", "", false},
		{"claude", "codex", false}, {"claude", "unknown", false},
	} {
		t.Run(tc.family+"/"+tc.tui, func(t *testing.T) {
			root := t.TempDir()
			account := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			wt := filepath.Join(root, "worker")
			if err := os.MkdirAll(wt, 0o755); err != nil {
				t.Fatal(err)
			}
			health2103Transcript(t, filepath.Join(account, "projects"), wt)
			cfg := &hostpoller.Config{WorktreesRoot: root, Workers: map[string]hostpoller.WorkerConfig{"w1": {Family: tc.family, TUI: tc.tui, Worktrees: []string{"worker"}, ClaudeConfigDir: account}}}
			m := healthContext(cfg, &hostpoller.State{}, "w1", "", &contextsize.Measurer{})
			if tc.want {
				if m == nil || m.Tokens != 16991 || m.MeasuredAt.Format(time.RFC3339Nano) != "2026-11-14T20:51:22.382Z" {
					t.Fatalf("got %+v", m)
				}
			} else if m != nil {
				t.Fatalf("unsupported reader measured %+v", m)
			}
			if cfg.Workers["w1"].Family != tc.family {
				t.Fatal("provider identity changed")
			}
		})
	}
}

func TestHealth2103ExplicitTUIReachesWire(t *testing.T) {
	account := t.TempDir()
	health1181WorkerExtra = "    tui: claude\n    claude_config_dir: " + jsonStr(account) + "\n"
	old := healthContextMeasurer
	t.Cleanup(func() { health1181WorkerExtra = ""; healthContextMeasurer = old })
	healthContextMeasurer = func(m *contextsize.Measurer, reader string, trees []string, roots contextsize.Roots) *contextsize.Measurement {
		if roots.ClaudeProjects != filepath.Join(account, "projects") {
			t.Fatalf("wrong account root: %+v", roots)
		}
		health2103Transcript(t, roots.ClaudeProjects, trees[0])
		return m.Measure(reader, trees, roots, time.Now())
	}
	sent, out := health2063Run(t)
	rep := health2063Report(t, sent)
	if rep["contextTokens"] != float64(16991) || rep["contextMeasuredAt"] != "2026-11-14T20:51:22.382Z" {
		t.Fatalf("wire report %v", rep)
	}
	if strings.Contains(out, "glm-5.3") {
		t.Fatal("non-numeric transcript field leaked")
	}
}
