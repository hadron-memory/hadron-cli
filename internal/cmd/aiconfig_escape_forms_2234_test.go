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
	bs := string(rune(92))
	for _, tc := range []struct{ name, key, spelling string }{
		{"json-only spelling", "sk-2234-<tag>&", "sk-2234-" + bs + "u003ctag" + bs + "u003e" + bs + "u0026"},
		{"go-only spelling", "sk-2234-\x7f-tail", "sk-2234-" + bs + "x7f-tail"},
	} {
		if tc.spelling == tc.key {
			t.Fatalf("%s: spelling must differ from the literal key", tc.name)
		}
		path := filepath.Join(t.TempDir(), "override.json")
		overrideJSON, _ := json.Marshal(map[string]any{"apiKey": tc.key, "model": "m-1959"})
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
					raw, _ := json.Marshal(result)
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
