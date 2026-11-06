package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
)

// A supplied credential does not let the client infer the server's answer.
// The two null-authContext cells are indistinguishable before #1726.
func TestAuthStatusAnswers1726(t *testing.T) {
	for _, answer := range []string{"AUTHENTICATED", "REFUSED", "ANONYMOUS"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", answer, jsonMode), func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var req struct {
						OperationName string `json:"operationName"`
						Query         string `json:"query"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					if r.Header.Get("Authorization") != "Bearer fixture-secret-1726" {
						t.Error("credential forwarding changed")
					}
					// Keep old AuthContext valid so the baseline failure is the missing
					// ruled output, rather than an unstubbed operation.
					if req.OperationName == "AuthContext" {
						_, _ = w.Write([]byte(`{"data":{"authContext":null}}`))
						return
					}
					if req.OperationName != "AuthenticationStatus" || strings.Contains(req.Query, "authContext") {
						t.Errorf("unexpected query %s", req.Query)
					}
					_, _ = fmt.Fprintf(w, `{"data":{"authenticationStatus":%q}}`, answer)
				}))
				defer srv.Close()
				f, out := testFactory(t)
				t.Setenv("HADRON_TOKEN", "fixture-secret-1726")
				args := []string{"auth", "status", "--server", srv.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				wantCode := 3
				if answer == "AUTHENTICATED" {
					wantCode = 0
				}
				if exitCodeFor(err) != wantCode {
					t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
				}
				if calls != 1 {
					t.Fatalf("calls=%d", calls)
				}
				if jsonMode {
					var got map[string]any
					if e := json.Unmarshal([]byte(out.String()), &got); e != nil {
						t.Fatal(e)
					}
					if len(got) != 1 || got["status"] != answer {
						t.Fatalf("answer-only JSON: %s", out)
					}
				} else {
					want := map[string]string{"AUTHENTICATED": "credential authenticated", "REFUSED": "a credential was presented and the server did not accept it", "ANONYMOUS": "no credential was presented"}[answer]
					if out.String() != want+"\n" {
						t.Fatalf("text=%q want=%q", out.String(), want)
					}
				}
				if strings.Contains(out.String(), "fixture-secret") {
					t.Fatal("credential echoed")
				}
			})
		}
	}
}

func TestAuthStatusAnonymousRequest1726(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("anonymous request carried auth")
		}
		_, _ = w.Write([]byte(`{"data":{"authenticationStatus":"ANONYMOUS"}}`))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	t.Setenv("HADRON_TOKEN", "")
	f.TokenStoreFn = func() store.Store { return memStore{} }
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "status", "--json", "--server", srv.URL})
	if err := root.Execute(); exitCodeFor(err) != 3 {
		t.Fatalf("exit: %v", err)
	}
	if calls != 1 || strings.TrimSpace(out.String()) != `{"status":"ANONYMOUS"}` {
		t.Fatalf("calls=%d output=%s", calls, out)
	}
}

func TestAuthStatusNoAnswer1726(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"missing", `{"data":{}}`, 7}, {"null", `{"data":{"authenticationStatus":null}}`, 7},
		{"unknown", `{"data":{"authenticationStatus":"OTHER"}}`, 7},
		{"skew", `{"errors":[{"message":"Cannot query field authenticationStatus","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, 2},
		{"check failed", `{"errors":[{"message":"private-cause-1726","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req1726"}}]}`, 1},
		{"unauthenticated envelope", `{"errors":[{"message":"not accepted","extensions":{"code":"UNAUTHENTICATED"}}]}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := graphQLAlways(t, 200, tc.body)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"auth", "status", "--json", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.code {
				t.Fatalf("exit=%d want=%d err=%v", exitCodeFor(err), tc.code, err)
			}
			if out.Len() != 0 {
				t.Fatalf("no answer printed a status: %s", out)
			}
			if err == nil || strings.Contains(err.Error(), "not accepted") || strings.Contains(err.Error(), "private-cause") {
				t.Fatalf("fault reads as refused or leaks cause: %v", err)
			}
		})
	}
}
