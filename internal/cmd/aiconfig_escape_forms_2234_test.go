package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2234 item 3, command level: a server echo that carries ONLY one escaped
// spelling of the submitted key must come out redacted on both request
// branches and in both stdout modes. Keys and spellings are chosen so the JSON
// and Go spellings differ from each other and from the literal (see
// internal/cmd/aiconfig/redact_forms_2234_test.go).
func TestAiConfig2234SingleEscapedFormEchoIsRedacted(t *testing.T) {
	// bs is a single backslash, built apart so the unicode-escape spellings below
	// stay literal text no tool can turn back into the characters they spell.
	bs := string(rune(92))
	for _, tc := range []struct{ name, key, spelling string }{
		{"json-only spelling", "sk-2234-<tag>&", "sk-2234-" + bs + "u003ctag" + bs + "u003e" + bs + "u0026"},
		{"go-only spelling", "sk-2234-\x7f-tail", "sk-2234-" + bs + "x7f-tail"},
	} {
		if tc.spelling == tc.key {
			t.Fatalf("%s: spelling must differ from the literal key", tc.name)
		}
		path := filepath.Join(t.TempDir(), "override.json")
		overrideJSON, err := json.Marshal(map[string]any{"apiKey": tc.key, "model": "m-1959"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, overrideJSON, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, b := range aiBranches1959() {
			for _, mode := range []string{"text", "json"} {
				t.Run(tc.name+"/"+b.name+"/"+mode, func(t *testing.T) {
					var result map[string]any
					if err := json.Unmarshal([]byte(b.result()), &result); err != nil {
						t.Fatal(err)
					}
					result["reply"] = "echo " + tc.spelling + " safe-tail"
					raw, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					args := append([]string{"--override-file", path}, b.args...)
					if mode == "json" {
						args = append(args, "--json")
					}
					r := runAiTest1959(t, func(string) string { return `{"data":{"testAiServiceConfig":` + string(raw) + `}}` }, args...)
					if r.exit != exitcode.OK || r.stderr != "" {
						t.Fatalf("exit=%d stderr=%q", r.exit, r.stderr)
					}
					assertForwarded1959(t, r, b.op, tc.key)
					if strings.Contains(r.stdout, tc.spelling) || strings.Contains(r.stdout, tc.key) || strings.Contains(r.stdout, "sk-2234") {
						t.Fatalf("an echo of the key's %s reached stdout:\n%s", tc.name, r.stdout)
					}
					if !strings.Contains(r.stdout, "[redacted]") || !strings.Contains(r.stdout, "safe-tail") {
						t.Fatalf("echo was dropped rather than redacted:\n%s", r.stdout)
					}
				})
			}
		}
	}
}

// cli#2234 (BLIND P2, P3 F4): the exact-value tradeoff holds at the command
// level, on both request branches and in both stdout modes, in a knob detail
// and in the effective model as well as the reply. A short key that is a
// substring of a legitimate value masks it; nothing is exempted.
func TestAiConfig2234ShortKeyMasksLegitimateSubstrings(t *testing.T) {
	const key = "m-19" // a substring of the effective model "m-1959"
	path := filepath.Join(t.TempDir(), "override.json")
	overrideJSON, err := json.Marshal(map[string]any{"apiKey": key, "model": "m-1959"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, overrideJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, b := range aiBranches1959() {
		for _, mode := range []string{"text", "json"} {
			t.Run(b.name+"/"+mode, func(t *testing.T) {
				var result map[string]any
				if err := json.Unmarshal([]byte(b.result()), &result); err != nil {
					t.Fatal(err)
				}
				result["model"] = "m-1959"
				result["reply"] = "reply from m-1959"
				knobs, ok := result["knobs"].([]any)
				if !ok || len(knobs) == 0 {
					t.Fatalf("fixture has no knobs: %v", result["knobs"])
				}
				knob, ok := knobs[0].(map[string]any)
				if !ok {
					t.Fatalf("fixture knob is %T, not an object", knobs[0])
				}
				knob["detail"] = "applied to m-1959"
				raw, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				args := append([]string{"--override-file", path}, b.args...)
				if mode == "json" {
					args = append(args, "--json")
				}
				r := runAiTest1959(t, func(string) string { return `{"data":{"testAiServiceConfig":` + string(raw) + `}}` }, args...)
				if r.exit != exitcode.OK || r.stderr != "" {
					t.Fatalf("exit=%d stderr=%q", r.exit, r.stderr)
				}
				assertForwarded1959(t, r, b.op, key)
				if strings.Contains(r.stdout, "m-1959") || strings.Contains(r.stdout, key) {
					t.Fatalf("a legitimate value containing the key was not masked:\n%s", r.stdout)
				}
				if got := strings.Count(r.stdout, "[redacted]59"); got < 3 {
					t.Fatalf("want the model, reply and knob detail each masked as [redacted]59, got %d:\n%s", got, r.stdout)
				}
			})
		}
	}
}
