package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAiConfigSubmittedKeyErrors1538(t *testing.T) {
	for _, key := range []string{"sk-test-1538-secret", "key-\"quoted\"-\\slash\nline", "FORBIDDEN"} {
		for _, mode := range []bool{false, true} {
			for _, cell := range []struct {
				code   string
				status int
			}{{"BAD_USER_INPUT", 200}, {"FORBIDDEN", 200}, {"NOT_FOUND", 200}, {"BAD_USER_INPUT", 400}, {"FORBIDDEN", 400}, {"NOT_FOUND", 400}} {
				code := cell.code
				t.Run(fmt.Sprintf("%q/json%t/%s/http%d", key, mode, code, cell.status), func(t *testing.T) {
					quoted, _ := json.Marshal(key)
					message := "override rejected apiKey=" + key + "; encoded=" + string(quoted) + "; try a supported input"
					payload, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": code}}}})
					var wire map[string]json.RawMessage
					calls := 0
					gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var request struct {
							OperationName string                     `json:"operationName"`
							Variables     map[string]json.RawMessage `json:"variables"`
						}
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						if request.OperationName != "TestAiServiceConfig" {
							t.Error("unexpected operation")
						}
						wire = request.Variables
						calls++
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(cell.status)
						_, _ = w.Write(payload)
					}))
					defer gql.Close()
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
						var envelope map[string]any
						if e := json.Unmarshal([]byte(out.String()), &envelope); e != nil {
							t.Fatal(e)
						}
						failure, ok := envelope["error"].(map[string]any)
						if !ok || len(failure) != 2 || failure["code"] != float64(want) {
							t.Fatal("unexpected error envelope shape")
						}
						// Check all raw streams AND every decoded envelope value, not
						// only message. error.code is numeric, even for key=FORBIDDEN.
						all += fmt.Sprint(envelope)
					}
					for _, secret := range []string{key, string(quoted[1 : len(quoted)-1])} {
						if strings.Contains(all, secret) {
							t.Fatal("submitted key surfaced")
						}
					}
					if !strings.Contains(all, "[redacted]") || !strings.Contains(all, "try a supported input") {
						t.Fatalf("safe diagnostic lost: %q", all)
					}
					if calls != 1 {
						t.Fatalf("requests %d want1", calls)
					}
					wireBytes, _ := json.Marshal(wire)
					v := unmarshalVars(t, wireBytes)
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
			payload := strings.ReplaceAll(aiTest1347, "No API key", "rejected "+key)
			payload = strings.ReplaceAll(payload, "NO_API_KEY", key)
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

// Root metadata is preserved when clean; secret-bearing request IDs are removed
// from the unsafe chain while safe structured metadata and classification survive.
func TestAiConfigErrorMetadata1538(t *testing.T) {
	const key = "sk-metadata-1538-secret"
	for _, requestID := range []string{"safe-request-1538", key} {
		t.Run(requestID, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "hidden server details " + key, "extensions": map[string]any{"code": "INTERNAL_SERVER_ERROR", "requestId": requestID}}}})
			gql, _ := captureGraphQL(t, map[string]string{"TestAiServiceConfig": string(payload)})
			f, out := testFactory(t)
			var stderr bytes.Buffer
			f.IOStreams.ErrOut = &stderr
			path := filepath.Join(t.TempDir(), "override.json")
			if err := os.WriteFile(path, []byte(`{"apiKey":"`+key+`"}`), 0600); err != nil {
				t.Fatal(err)
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"ai-config", "test", "cfg1", "--json", "--override-file", path, "--server", gql.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected failure")
			}
			if got := renderError(f, err); got != 1 {
				t.Fatalf("exit %d", got)
			}
			var envelope struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error["code"] != float64(1) {
				t.Fatal("numeric code changed")
			}
			if strings.Contains(out.String()+stderr.String()+fmt.Sprint(envelope.Error), key) {
				t.Fatal("metadata leaked submitted key")
			}
			if requestID == key {
				if len(envelope.Error) != 4 || envelope.Error["requestId"] != "[redacted]" || envelope.Error["serverCode"] != "INTERNAL_SERVER_ERROR" || !strings.Contains(envelope.Error["message"].(string), "[redacted]") {
					t.Fatal("secret-bearing metadata not sanitized")
				}
			} else if len(envelope.Error) != 4 || envelope.Error["serverCode"] != "INTERNAL_SERVER_ERROR" || envelope.Error["requestId"] != requestID {
				t.Fatal("clean metadata changed")
			}
		})
	}
}
