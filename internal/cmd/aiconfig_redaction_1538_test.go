package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAiConfigSubmittedKeyErrors1538(t *testing.T) {
	for _, key := range []string{"sk-test-1538-secret", "key-\"quoted\"-\\slash\nline", "FORBIDDEN"} {
		for _, mode := range []bool{false, true} {
			for _, code := range []string{"BAD_USER_INPUT", "FORBIDDEN", "NOT_FOUND"} {
				t.Run(fmt.Sprintf("%q/json%t/%s", key, mode, code), func(t *testing.T) {
					quoted, _ := json.Marshal(key)
					message := "override rejected apiKey=" + key + "; encoded=" + string(quoted) + "; try a supported input"
					payload, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": code}}}})
					gql, captured := captureGraphQL(t, map[string]string{"TestAiServiceConfig": string(payload)})
					f, out := testFactory(t)
					var stderr bytes.Buffer
					f.IOStreams.ErrOut = &stderr
					path := filepath.Join(t.TempDir(), "override.json")
					input, _ := json.Marshal(map[string]string{"apiKey": key})
					if err := os.WriteFile(path, input, 0600); err != nil {
						t.Fatal(err)
					}
					root := NewRootCmd(f)
					args := []string{"ai-config", "test", "cfg1", "--override-file", path, "--server", gql.URL}
					if mode {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					if err == nil {
						t.Fatal("expected refusal")
					}
					got := renderError(f, err)
					want := map[string]int{"BAD_USER_INPUT": 2, "FORBIDDEN": 8, "NOT_FOUND": 4}[code]
					if got != want {
						t.Fatalf("exit %d want %d", got, want)
					}
					all := out.String() + stderr.String()
					if mode {
						var v struct {
							Error struct {
								Message string `json:"message"`
							}
						}
						if e := json.Unmarshal([]byte(out.String()), &v); e != nil {
							t.Fatal(e)
						}
						all = v.Error.Message
					}
					for _, secret := range []string{key, string(quoted[1 : len(quoted)-1])} {
						if strings.Contains(all, secret) {
							t.Fatal("submitted key surfaced")
						}
					}
					if !strings.Contains(all, "[redacted]") || !strings.Contains(all, "try a supported input") {
						t.Fatalf("safe diagnostic lost: %q", all)
					}
					v := unmarshalVars(t, captured["TestAiServiceConfig"])
					if v["override"].(map[string]any)["apiKey"] != key {
						t.Fatal("submitted key changed on wire")
					}
				})
			}
		}
	}
}

func TestAiConfigCompletedErrorRedacts1538(t *testing.T) {
	const key = "sk-completed-error-secret"
	for _, mode := range []bool{false, true} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			payload := strings.Replace(aiTest1347, "No API key", "rejected "+key, -1)
			gql, _ := captureGraphQL(t, map[string]string{"TestAiServiceConfig": `{"data":{"testAiServiceConfig":` + payload + `}}`})
			f, out := testFactory(t)
			path := filepath.Join(t.TempDir(), "override.json")
			if err := os.WriteFile(path, []byte(`{"apiKey":"`+key+`"}`), 0600); err != nil {
				t.Fatal(err)
			}
			root := NewRootCmd(f)
			args := []string{"ai-config", "test", "cfg1", "--override-file", path, "--server", gql.URL}
			if mode {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), key) || !strings.Contains(out.String(), "[redacted]") {
				t.Fatal("completed test error not redacted")
			}
		})
	}
}
