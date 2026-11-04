package team

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/guardedpush"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

func pushIdle(family string) []string {
	name := "c30-restart"
	if family == "codex" {
		name = "x21-done"
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "orcaterm", "testdata", "scratch-2026-10-18", name+".json"))
	if err != nil {
		panic(err)
	}
	var env struct {
		Result struct {
			Terminal struct {
				Tail []string `json:"tail"`
			} `json:"terminal"`
		} `json:"result"`
	}
	if err = json.Unmarshal(raw, &env); err != nil {
		panic(err)
	}
	return env.Result.Terminal.Tail
}

func envelope(v any) orcaterm.Result {
	b, _ := json.Marshal(map[string]any{"ok": true, "result": v})
	return orcaterm.Result{Stdout: string(b)}
}
func TestPushAdapterRecognizedMetadataAndDraft(t *testing.T) {
	for _, family := range []string{"claude", "codex"} {
		t.Run(family, func(t *testing.T) {
			a := pushTerminal{verify: func(_ context.Context, path, _ string) (string, error) {
				if path != "/repo/iris" {
					return "", errors.New("no")
				}
				return "session", nil
			}, run: func(_ context.Context, args []string) orcaterm.Result {
				if args[1] == "list" {
					return envelope(map[string]any{"terminals": []any{map[string]any{"handle": "term", "worktreePath": "/repo/iris", "agentIdentity": family, "connected": true, "writable": true}}})
				}
				return envelope(map[string]any{"terminal": map[string]any{"source": "screen", "tail": pushIdle(family), "draft": ""}})
			}}
			f, err := a.observe(context.Background())
			if err != nil || f.State != guardedpush.Idle || !f.Empty || f.Family != family || !strings.Contains(f.Identity, "session") {
				t.Fatalf("%s %+v %v", family, f, err)
			}
		})
	}
}
func TestPushAdapterAmbiguityAndMalformedHold(t *testing.T) {
	for _, mode := range []string{"duplicate", "disconnected", "missing-writable", "malformed-draft", "unknown-screen", "wrong-interface", "wrong-binding", "source-tail", "read-error", "list-error"} {
		t.Run(mode, func(t *testing.T) {
			row := map[string]any{"handle": "term", "worktreePath": "/repo/iris", "agentIdentity": "claude", "connected": true, "writable": true}
			rows := []any{row}
			if mode == "duplicate" {
				rows = append(rows, row)
			}
			if mode == "disconnected" {
				row["connected"] = false
			}
			if mode == "missing-writable" {
				delete(row, "writable")
			}
			if mode == "wrong-interface" {
				row["agentIdentity"] = "codex"
			}
			a := pushTerminal{verify: func(context.Context, string, string) (string, error) {
				if mode == "wrong-binding" {
					return "", errors.New("PRIVATE-BINDING")
				}
				return "session", nil
			}, run: func(_ context.Context, args []string) orcaterm.Result {
				if args[1] == "list" {
					if mode == "list-error" {
						return orcaterm.Result{Code: 1, Stderr: "SECRET"}
					}
					return envelope(map[string]any{"terminals": rows})
				}
				s := map[string]any{"source": "screen", "tail": pushIdle("claude"), "draft": ""}
				switch mode {
				case "malformed-draft":
					s["draft"] = true
				case "null-draft":
					s["draft"] = nil
				case "missing-draft":
					delete(s, "draft")
				case "source-tail":
					s["source"] = "tail"
				case "unknown-screen":
					s["tail"] = []string{"arbitrary unknown screen"}
				case "read-error":
					return orcaterm.Result{Code: 1, Stdout: "SECRET"}
				}
				return envelope(map[string]any{"terminal": s})
			}}
			f, err := a.observe(context.Background())
			if err == nil && f.State != guardedpush.Unknown {
				t.Fatalf("unsafe proof %+v", f)
			}
		})
	}
}
func TestPushAdapterClearAndSubmitAreSingleCallsWithoutRetry(t *testing.T) {
	for _, family := range []string{"claude", "codex"} {
		t.Run(family, func(t *testing.T) {
			var calls [][]string
			a := pushTerminal{handle: "term", run: func(_ context.Context, args []string) orcaterm.Result {
				calls = append(calls, args)
				return envelope(map[string]any{"send": map[string]any{"accepted": true, "prompt": map[string]any{"requestId": "never-retry", "stages": []string{"turn_started"}}}})
			}}
			if err := a.clear(context.Background(), "unfinished 🐋\nline"); err != nil {
				t.Fatal(err)
			}
			if err := a.submit(context.Background(), "PAYLOAD"); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 {
				t.Fatal("repeated send")
			}
			for i, args := range calls {
				hasEnter := false
				for _, s := range args {
					if s == "--enter" {
						hasEnter = true
					}
					if s == "--retry-request" || s == "--wait-submit" || s == "--interrupt" {
						t.Fatalf("unexpected semantics %s", s)
					}
					if strings.ContainsAny(s, "\x03\x15\x1b") {
						t.Fatal("destructive clear")
					}
				}
				if hasEnter != (i == 1) {
					t.Fatal("clear submitted or submit missing enter")
				}
			}
			if calls[0][5] != strings.Repeat("\x7f", len([]rune("unfinished 🐋\nline"))) {
				t.Fatal("not proven backspace sequence")
			}
		})
	}
}
func TestPushSendFailureDoesNotReplayRequestIDOrLeakError(t *testing.T) {
	calls := 0
	a := pushTerminal{handle: "term", run: func(context.Context, []string) orcaterm.Result {
		calls++
		return orcaterm.Result{Code: 1, Stdout: `{"ok":false,"error":{"code":"SECRET","requestId":"replay-me","message":"PRIVATE-DRAFT"}}`, Stderr: "PRIVATE-KEY"}
	}}
	err := a.submit(context.Background(), "PRIVATE-MESSAGE")
	if calls != 1 || err == nil || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("calls%d error%v", calls, err)
	}
}

func TestPushCommandSpecificExitContract(t *testing.T) {
	for k, want := range map[guardedpush.Outcome]int{guardedpush.Sent: 0, guardedpush.SentWithDraft: 9, guardedpush.HeldBusy: 10, guardedpush.HeldUnknown: 11, guardedpush.Unconfirmed: 12} {
		if got := pushOutcomeExit(k); got != want {
			t.Fatalf("%s exit%d want%d", k, got, want)
		}
	}
}

// These existing REAL captures omit draft on empty frames; invented always-present
// draft fixtures must not make the no-draft path impossible on the actual host.
func TestPushAdapterExistingScratchCaptureContract(t *testing.T) {
	for _, c := range []struct {
		name, family string
		empty        bool
		draft        bool
	}{{"c01-start", "claude", true, false}, {"c30-restart", "claude", true, false}, {"x01-start", "codex", true, false}, {"x21-done", "codex", true, false}, {"c02-draft", "claude", false, true}, {"c09-multiline", "claude", false, true}, {"x02-draft", "codex", false, true}, {"x04-ml", "codex", false, true}} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "orcaterm", "testdata", "scratch-2026-10-18", c.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			a := pushTerminal{verify: func(context.Context, string, string) (string, error) { return "session", nil }, run: func(_ context.Context, args []string) orcaterm.Result {
				if args[1] == "list" {
					return envelope(map[string]any{"terminals": []any{map[string]any{"handle": "t", "worktreePath": "/repo/iris", "agentIdentity": c.family, "connected": true, "writable": true}}})
				}
				return orcaterm.Result{Stdout: string(raw)}
			}}
			f, err := a.observe(context.Background())
			if err != nil || f.Empty != c.empty || (f.Draft != "") != c.draft {
				t.Fatalf("capture contract: empty%t draft%t error%v", f.Empty, f.Draft != "", err)
			}
		})
	}
}
