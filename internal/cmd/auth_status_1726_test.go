package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	authpkg "github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A supplied credential does not let the client infer the server's answer.
// The two null-authContext cells are indistinguishable before #1726.
func TestAuthStatusAnswers1726(t *testing.T) {
	for _, answer := range []string{"AUTHENTICATED", "REFUSED", "ANONYMOUS"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", answer, jsonMode), func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var req struct {
						OperationName string `json:"operationName"`
						Query         string `json:"query"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					if r.Header.Get("Authorization") != "Bearer fixture-secret-1726" {
						t.Error("credential forwarding changed")
					}
					if req.OperationName != "CheckAuthenticationStatus" || strings.Contains(req.Query, "authContext") {
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
				if calls.Load() != 1 {
					t.Fatalf("calls=%d", calls.Load())
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
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
	if calls.Load() != 1 || strings.Join(strings.Fields(out.String()), "") != `{"status":"ANONYMOUS"}` {
		t.Fatalf("calls=%d output=%s", calls.Load(), out)
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

// Whole-command faults must never manufacture a REFUSED answer, even when
// the envelope uses an auth code or OAuth scope failure before resolution.
func TestAuthStatusFaultWire1726(t *testing.T) {
	for _, tc := range []struct {
		name     string
		httpCode int
		body     string
		code     int
	}{
		{"gateway", 502, `unavailable`, 7},
		{"http401", 401, `{"errors":[{"message":"token expired","extensions":{"code":"UNAUTHENTICATED"}}]}`, 1},
		{"scope", 500, mcpOnlyRefusalBody, 1},
		{"unsupported scope", 403, `{"errors":[{"message":"scope failure","extensions":{"code":"FORBIDDEN","reason":"OAUTH_SCOPE_UNSUPPORTED"}}]}`, 1},
		{"null data", 200, `{"data":null}`, 7},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, asJSON), func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.httpCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				f, out := testFactory(t)
				var stderr strings.Builder
				f.IOStreams.ErrOut = &stderr
				root := NewRootCmd(f)
				args := []string{"auth", "status", "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if exitCodeFor(err) != tc.code || calls.Load() != 1 || out.Len() != 0 {
					t.Fatalf("code=%d calls=%d stdout=%s err=%v", exitCodeFor(err), calls.Load(), out, err)
				}
				if got := renderError(f, err); got != tc.code {
					t.Fatalf("render exit=%d", got)
				}
				for _, forbidden := range []string{"REFUSED", "not accept", "token expired", "fixture-secret"} {
					if strings.Contains(stderr.String(), forbidden) {
						t.Fatalf("fault disclosed verdict/cause: %s", stderr.String())
					}
				}
			})
		}
	}
}

// Field-exact guard on the generated document, not captured variables.
func TestAuthenticationStatusDocument1726(t *testing.T) {
	got := strings.Join(strings.Fields(gen.CheckAuthenticationStatus_Operation), " ")
	if got != "query CheckAuthenticationStatus { authenticationStatus }" {
		t.Fatalf("answer-only operation changed: %s", got)
	}
}

// This is a read-only Store double; no shared config or keychain is touched.
type authStatusStore1726 struct {
	impersonationKey string
	keys             []string
	values           map[string]string
	failOrdinary     bool
	reads            int
}

func (*authStatusStore1726) Name() string { return "fixture" }
func (s *authStatusStore1726) Get(key string) (string, error) {
	s.reads++
	s.keys = append(s.keys, key)
	if s.failOrdinary && key != s.impersonationKey {
		return "", errors.New("fixture keychain read failed")
	}
	value, ok := s.values[key]
	if !ok {
		return "", store.ErrNotFound
	}
	return value, nil
}
func (*authStatusStore1726) Set(string, string) error { return errors.New("fixture is read only") }
func (*authStatusStore1726) Delete(string) error      { return errors.New("fixture is read only") }

func TestAuthStatusCredentialSources1726(t *testing.T) {
	// Only the local selector reads these unverified claims. This fixture does
	// not assert a JWT is accepted by a real provider or has impersonation rights.
	impersonation := "fixture." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":4102444800,"imp":{"sid":"fixture-session","act":"fixture-actor","org":"fixture-org"}}`)) + ".fixture"
	for _, tc := range []struct {
		name, env, stored, imp, want string
		failStore                    bool
		wantReads                    int
		absentLogin, absentImp       bool
	}{
		{"stored login", "", "stored-fixture", "", "stored-fixture", false, 2, false, false},
		{"impersonation precedes login", "", "stored-fixture", impersonation, impersonation, false, 1, false, false},
		{"environment precedes both", "env-fixture", "stored-fixture", impersonation, "env-fixture", false, 0, false, false},
		{"environment does not probe broken store", "env-fixture", "", "", "env-fixture", true, 0, false, false},
		{"absent login and impersonation", "", "", "", "", false, 2, true, true},
		{"empty login and impersonation", "", "", "", "", false, 2, false, false},
		{"empty login absent impersonation", "", "", "", "", false, 2, false, true},
		{"absent login empty impersonation", "", "", "", "", false, 2, true, false},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, asJSON), func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					wantHeader := ""
					if tc.want != "" {
						wantHeader = "Bearer " + tc.want
					}
					if got := r.Header.Get("Authorization"); got != wantHeader {
						t.Errorf("wrong selected credential header: %q", got)
					}
					answer := "AUTHENTICATED"
					if tc.want == "" {
						answer = "ANONYMOUS"
					}
					_, _ = fmt.Fprintf(w, `{"data":{"authenticationStatus":%q}}`, answer)
				}))
				defer srv.Close()
				f, out := testFactory(t)
				t.Setenv("HADRON_TOKEN", tc.env)
				host, impKey := authpkg.Host(srv.URL), authpkg.ImpersonationHostKey(srv.URL)
				values := map[string]string{}
				if !tc.absentLogin {
					values[host] = tc.stored
				}
				if !tc.absentImp {
					values[impKey] = tc.imp
				}
				st := &authStatusStore1726{values: values, impersonationKey: impKey, failOrdinary: tc.failStore}
				f.TokenStoreFn = func() store.Store { return st }
				root := NewRootCmd(f)
				args := []string{"auth", "status", "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				wantAnswer, wantCode := "AUTHENTICATED", 0
				if tc.want == "" {
					wantAnswer, wantCode = "ANONYMOUS", 3
				}
				if err := root.Execute(); exitCodeFor(err) != wantCode {
					t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
				}
				wantKeys := []string{}
				if tc.wantReads > 0 {
					wantKeys = append(wantKeys, impKey)
				}
				if tc.wantReads > 1 {
					wantKeys = append(wantKeys, host)
				}
				if len(st.keys) != len(wantKeys) || (len(wantKeys) > 0 && !reflect.DeepEqual(st.keys, wantKeys)) {
					t.Fatalf("store keys=%q want=%q", st.keys, wantKeys)
				}
				if calls.Load() != 1 || st.reads != tc.wantReads {
					t.Fatalf("requests=%d store reads=%d want=%d", calls.Load(), st.reads, tc.wantReads)
				}
				if asJSON {
					var got map[string]any
					if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
						t.Fatal(err)
					}
					if len(got) != 1 || got["status"] != wantAnswer {
						t.Fatalf("not answer-only: %s", out)
					}
				} else if wantText := map[string]string{"AUTHENTICATED": "credential authenticated\n", "ANONYMOUS": "no credential was presented\n"}[wantAnswer]; out.String() != wantText {
					t.Fatalf("output=%q", out.String())
				}
				for _, secret := range []string{"stored-fixture", "env-fixture", impersonation} {
					if secret != "" && strings.Contains(out.String(), secret) {
						t.Fatal("selected credential leaked")
					}
				}
			})
		}
	}
}

func TestAuthStatusStoredCredentialErrorStopsBeforeRequest1726(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(`{"data":{"authenticationStatus":"ANONYMOUS"}}`))
			}))
			defer srv.Close()
			f, out := testFactory(t)
			t.Setenv("HADRON_TOKEN", "")
			var stderr strings.Builder
			f.IOStreams.ErrOut = &stderr
			st := &authStatusStore1726{values: map[string]string{}, impersonationKey: authpkg.ImpersonationHostKey(srv.URL), failOrdinary: true}
			f.TokenStoreFn = func() store.Store { return st }
			root := NewRootCmd(f)
			args := []string{"auth", "status", "--server", srv.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Error || err == nil || !strings.Contains(err.Error(), "fixture keychain read failed") {
				t.Fatalf("read error swallowed: %v", err)
			}
			if calls.Load() != 0 || out.Len() != 0 || st.reads != 2 {
				t.Fatalf("requests=%d stdout=%s store reads=%d", calls.Load(), out, st.reads)
			}
			if code := renderError(f, err); code != exitcode.Error {
				t.Fatalf("render exit=%d", code)
			}
			if strings.Contains(stderr.String(), "ANONYMOUS") || strings.Contains(stderr.String(), "REFUSED") {
				t.Fatalf("manufactured verdict: %s", stderr.String())
			}
		})
	}
}

type countedRefusingTransport1754 struct{ calls atomic.Int32 }

func (tr *countedRefusingTransport1754) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls.Add(1)
	return (refusingTransport{}).RoundTrip(req)
}

func TestAuthStatusTransportFailureIsNoAnswer1726(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			f, out := testFactory(t)
			var serverCalls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				serverCalls.Add(1)
				_, _ = w.Write([]byte(`{"data":{"authenticationStatus":"AUTHENTICATED"}}`))
			}))
			defer srv.Close()
			tr := &countedRefusingTransport1754{}
			f.HTTPClient = &http.Client{Transport: tr}
			var stderr strings.Builder
			f.IOStreams.ErrOut = &stderr
			root := NewRootCmd(f)
			args := []string{"auth", "status", "--server", srv.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			err := root.Execute()
			if tr.calls.Load() != 1 || serverCalls.Load() != 0 {
				t.Fatalf("injected transport calls=%d reachable server calls=%d", tr.calls.Load(), serverCalls.Load())
			}
			if exitCodeFor(err) != exitcode.Unavailable || out.Len() != 0 {
				t.Fatalf("code=%d stdout=%s err=%v", exitCodeFor(err), out, err)
			}
			if code := renderError(f, err); code != exitcode.Unavailable {
				t.Fatalf("render exit=%d", code)
			}
			if strings.Contains(stderr.String(), "REFUSED") || strings.Contains(stderr.String(), "not accept") {
				t.Fatalf("fault became refusal: %s", stderr.String())
			}
		})
	}
}

func TestAuthImpersonationHelpUsesWhoamiForIdentity1726(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "impersonate", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	command, _, err := root.Find([]string{"auth", "impersonate"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "hadron auth status") ||
		!strings.Contains(command.Long, `hadron auth whoami
shows "IMPERSONATED (read-only)"`) ||
		!strings.Contains(command.Example, "\n  hadron auth whoami\n") {
		t.Fatalf("impersonation pointer/example drift: %s", out)
	}
}

// Follow the pointer, rather than assuming whoami uses status's old words.
// This response fixture proves rendering, not actual impersonation rights.
func TestAuthWhoamiImpersonationPointerTarget1726(t *testing.T) {
	srv := fakeGraphQL(t, map[string]string{"AuthContext": `{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,"user":{"id":"fixture-user","name":"Fixture target","email":null,"handle":null,"githubUsername":null,"roles":[]},"apiKey":null,"impersonation":{"sessionId":"fixture-session","organizationId":"fixture-org","expiresAt":"2100-01-01T00:00:00Z","readOnly":true,"actorUser":{"id":"fixture-actor","handle":"actor-handle","name":"Fixture actor"}}}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "whoami", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Fixture target — IMPERSONATED (read-only), org fixture-org, as Fixture actor, expires 2100-01-01T00:00:00Z\n"; got != want {
		t.Fatalf("pointer target text=%q want=%q", got, want)
	}
}

// The fixture must distinguish an exact impersonation key from a suffix lookalike.
func TestAuthStatusStoreDiscriminator1754(t *testing.T) {
	server := "https://fixture.example"
	st := &authStatusStore1726{values: map[string]string{}, failOrdinary: true}
	exact := authpkg.ImpersonationHostKey(server)
	// Bind the selector without depending on a future key's textual format.
	st.impersonationKey = exact
	if _, err := st.Get(exact); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("exact key: %v", err)
	}
	if _, err := st.Get("unrelated#impersonation"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lookalike did not take ordinary failure: %v", err)
	}
	if !reflect.DeepEqual(st.keys, []string{exact, "unrelated#impersonation"}) {
		t.Fatalf("keys=%q", st.keys)
	}
}

func TestAuthStatusStoreEmptyAndAbsent1754(t *testing.T) {
	for _, tc := range []struct {
		name        string
		values      map[string]string
		wantMissing bool
	}{
		{"absent", map[string]string{}, true},
		{"present empty", map[string]string{"fixture-host": ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &authStatusStore1726{values: tc.values}
			value, err := st.Get("fixture-host")
			if value != "" || errors.Is(err, store.ErrNotFound) != tc.wantMissing || (!tc.wantMissing && err != nil) {
				t.Fatalf("value=%q err=%v missing=%t", value, err, tc.wantMissing)
			}
		})
	}
}
