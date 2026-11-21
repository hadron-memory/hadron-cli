package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

const encrypt1650OK = `{"data":{"encryptMemory":{"id":"m1","urn":"hrn:mem:acme.com:kb","name":"KB","isEncrypted":true}}}`
const encrypt1650Secret = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="

type encrypt1650Request struct{ op, body, keys, operation string }
type encrypt1650Fake struct {
	mu                sync.Mutex
	requests          []encrypt1650Request
	principal, answer string
	status            int
	redirect          string
}

func (s *encrypt1650Fake) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, `{"capabilities":[]}`)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var q struct{ OperationName string }
	_ = json.Unmarshal(b, &q)
	s.mu.Lock()
	s.requests = append(s.requests, encrypt1650Request{q.OperationName, string(b), r.Header.Get(api.MemoryKeysHeader), r.Header.Get(api.OperationKeyHeader)})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch q.OperationName {
	case "AuthContext":
		p := s.principal
		if p == "" {
			p = "USER"
		}
		_, _ = fmt.Fprintf(w, `{"data":{"authContext":{"principalType":%q,"appId":null}}}`, p)
	case "GetMemory":
		_, _ = io.WriteString(w, `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:kb","name":"KB","isEncrypted":false}}}`)
	case "EncryptMemory":
		if s.redirect != "" {
			http.Redirect(w, r, s.redirect, http.StatusTemporaryRedirect)
			return
		}
		status := s.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		ans := s.answer
		if ans == "" {
			ans = encrypt1650OK
		}
		ans = strings.ReplaceAll(ans, "SENTINEL", encrypt1650Secret)
		_, _ = io.WriteString(w, ans)
	default:
		t := `{"errors":[{"message":"unexpected operation"}]}`
		_, _ = io.WriteString(w, t)
	}
}
func runEncrypt1650(t *testing.T, s *encrypt1650Fake, extra ...string) (string, string, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(encrypt1650Secret + "\n")
	root := NewRootCmd(f)
	ambient, e := api.WithMemoryKeys(context.Background(), []api.MemoryKey{{Memory: "other", Passphrase: "ambient-unlock"}})
	if e != nil {
		t.Fatal(e)
	}
	root.SetContext(api.WithOperationKey(ambient, "ambient-operation"))
	root.SetArgs(append([]string{"memory", "encrypt", "hrn:mem:acme.com:kb", "--yes", "--server", server.URL}, extra...))
	err := root.Execute()
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}
func (s *encrypt1650Fake) calls() []encrypt1650Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]encrypt1650Request{}, s.requests...)
}
func TestEncryptMemoryDocumentHasNoSecret1650(t *testing.T) {
	for _, word := range []string{"dataKey", "passphrase"} {
		if strings.Contains(gen.EncryptMemory_Operation, word) {
			t.Errorf("document still names %s", word)
		}
	}
}
func TestEncryptMemoryHeadersScoped1650(t *testing.T) {
	for _, tc := range []struct{ name, principal, flag, field string }{{"owner-key", "USER", "--data-key", "dataKey"}, {"owner-passphrase", "USER", "--passphrase-file", "passphrase"}, {"app-key", "APP", "--data-key", "dataKey"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &encrypt1650Fake{principal: tc.principal}
			_, _, err := runEncrypt1650(t, s, tc.flag, "-", "--json")
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, r := range s.calls() {
				if strings.Contains(r.body, encrypt1650Secret) || strings.Contains(r.body, `"dataKey"`) || strings.Contains(r.body, `"passphrase"`) {
					t.Error("secret field/value in GraphQL body")
				}
				if r.op != "EncryptMemory" {
					if r.keys != "" || r.operation != "" {
						t.Errorf("key on %s", r.op)
					}
					continue
				}
				n++
				if tc.principal == "APP" {
					if r.operation != encrypt1650Secret || r.keys != "" {
						t.Error("App must send operation key only")
					}
				} else {
					var entries []map[string]string
					if json.Unmarshal([]byte(r.keys), &entries) != nil || len(entries) != 1 || entries[0]["memory"] != "m1" || entries[0][tc.field] != encrypt1650Secret || len(entries[0]) != 2 || r.operation != "" {
						t.Error("owner must send one target-only key entry")
					}
				}
			}
			if n != 1 {
				t.Fatalf("mutation count=%d", n)
			}
		})
	}
}
func TestEncryptMemoryAppPassphraseRefused1650(t *testing.T) {
	s := &encrypt1650Fake{principal: "APP"}
	_, _, err := runEncrypt1650(t, s, "--passphrase-file", "-")
	if err == nil || exitCodeFor(err) != 2 {
		t.Fatalf("want usage refusal, got %v", err)
	}
	for _, r := range s.calls() {
		if r.op == "EncryptMemory" {
			t.Error("App passphrase sent a mutation")
		}
	}
}
func TestEncryptMemoryPassphraseModeBeforeNetwork1650(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte(encrypt1650Secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	s := &encrypt1650Fake{}
	_, _, err := runEncrypt1650(t, s, "--passphrase-file", p)
	if err == nil || !strings.Contains(err.Error(), "group or others") || len(s.calls()) != 0 {
		t.Fatalf("insecure passphrase file must refuse before requests: err=%v requests=%d", err, len(s.calls()))
	}
}
func TestEncryptMemoryRefusalsNoFallback1650(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{{"older", `{"errors":[{"message":"not encrypted","extensions":{"code":"NOT_ENCRYPTED"}}]}`, "too old"}, {"stripped", `{"errors":[{"message":"Provide exactly one of dataKey or passphrase","extensions":{"code":"BAD_USER_INPUT"}}]}`, "did not reach"}, {"plaintext", strings.Replace(encrypt1650OK, `"isEncrypted":true`, `"isEncrypted":false`, 1), "not confirmed"}, {"null", `{"data":{"encryptMemory":null}}`, "not confirmed"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &encrypt1650Fake{answer: tc.body}
			out, stderr, err := runEncrypt1650(t, s, "--passphrase-file", "-")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "--unlock refused") {
				t.Fatalf("wrong refusal: %v out=%s stderr=%s", err, out, stderr)
			}
			n := 0
			for _, r := range s.calls() {
				if r.op == "EncryptMemory" {
					n++
					if strings.Contains(r.body, encrypt1650Secret) {
						t.Error("fallback put secret in body")
					}
				}
			}
			if n != 1 {
				t.Fatalf("want one mutation, got%d", n)
			}
		})
	}
}
func TestEncryptMemoryEchoNeverLeaks1650(t *testing.T) {
	for _, flag := range []string{"--data-key", "--passphrase-file"} {
		for _, status := range []int{200, 400, 500} {
			t.Run(fmt.Sprintf("%s-%d", flag, status), func(t *testing.T) {
				s := &encrypt1650Fake{status: status, answer: `{"errors":[{"message":"SENTINEL","extensions":{"code":"SENTINEL","detail":"SENTINEL"}}]}`}
				out, stderr, err := runEncrypt1650(t, s, flag, "-", "--json", "--verbose")
				if err == nil {
					t.Fatal("echo error unexpectedly succeeded")
				}
				render := out + stderr + err.Error()
				if strings.Contains(render, encrypt1650Secret) || strings.Contains(strings.ToLower(render), strings.ToLower(api.MemoryKeysHeader)) {
					t.Error("error rendering leaked key/header")
				}
			})
		}
	}
}
func TestEncryptMemoryRedirectDoesNotForward1650(t *testing.T) {
	var mu sync.Mutex
	received := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received++
		mu.Unlock()
		_, _ = io.WriteString(w, encrypt1650OK)
	}))
	defer target.Close()
	s := &encrypt1650Fake{redirect: target.URL}
	_, _, err := runEncrypt1650(t, s, "--data-key", "-")
	mu.Lock()
	n := received
	mu.Unlock()
	if err == nil || n != 0 {
		t.Fatalf("keyed redirect must refuse without second request: err=%v requests=%d", err, n)
	}
}

func TestEncryptMemorySuccessfulEchoWithheld1650(t *testing.T) {
	for _, flag := range []string{"--data-key", "--passphrase-file"} {
		t.Run(flag, func(t *testing.T) {
			s := &encrypt1650Fake{answer: strings.Replace(encrypt1650OK, `"name":"KB"`, `"name":"SENTINEL"`, 1)}
			out, stderr, err := runEncrypt1650(t, s, flag, "-", "--json")
			if err == nil || strings.Contains(out+stderr+err.Error(), encrypt1650Secret) {
				t.Fatal("successful echo must be withheld")
			}
		})
	}
}
