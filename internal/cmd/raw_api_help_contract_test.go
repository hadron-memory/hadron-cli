package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// refusingTransport fails every request the way a refused connection does.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

// headerSet renders every request header, sorted, so two requests compare
// exactly rather than on one header name the test happened to think of.
func headerSet(h http.Header) string {
	var lines []string
	for k, vs := range h {
		lines = append(lines, k+": "+strings.Join(vs, ","))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// The raw API ignores --app (its help says so): the request must be the same
// with and without it. Compared on the ACTUAL header set (cli#1220), because
// the old pin looked for one literal header name that no code path sends.
func TestRawAPIAppFlagChangesNoRequestHeader(t *testing.T) {
	run := func(withApp bool) string {
		var got http.Header
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"me":null}}`))
		}))
		defer srv.Close()
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"api", "query { me { id } }", "--server", srv.URL}
		if withApp {
			args = append(args, "--app", autoApp)
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("the server saw no request")
		}
		return headerSet(got)
	}
	without, with := run(false), run(true)
	if without != with {
		t.Errorf("--app changed the request headers:\nwithout:\n%s\nwith:\n%s", without, with)
	}
	if !strings.Contains(without, "Authorization: Bearer ") {
		t.Errorf("the comparison must cover a real request (no bearer seen):\n%s", without)
	}
}

func TestRawAPIHelpContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   int
	}{
		{"data", 200, `{"data":{"me":{"id":"u1"}}}`, 0},
		{"null", 200, `{"data":{"me":null}}`, 0},
		{"errors", 200, `{"data":null,"errors":[{"message":"hidden","extensions":{"code":"FORBIDDEN"}}]}`, 8},
		{"invalid_document", 400, `{"errors":[{"message":"invalid field","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, 2},
		{"typed_http_refusal", 403, `{"errors":[{"message":"hidden","extensions":{"code":"FORBIDDEN"}}]}`, 8},
		{"gateway", 502, `upstream unavailable`, 7},
		{"unreachable", 0, "", 7}, // served by an injected failing transport, not a socket
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonMode), func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if got := r.Header.Get("X-Hadron-App"); got != "" {
						t.Errorf("raw API sent App header: %q", got)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				f, out := testFactory(t)
				if tc.status == 0 {
					// Deterministic: the transport refuses every request, so
					// no listener is opened-then-closed and no port can be
					// re-bound by another process mid-test (cli#1220).
					f.HTTPClient = &http.Client{Transport: refusingTransport{}}
				}
				errOut := &strings.Builder{}
				f.IOStreams.ErrOut = errOut
				root := NewRootCmd(f)
				args := []string{"api", "query { me { id } }", "--server", srv.URL, "--app", autoApp}
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				code := 0
				if err != nil {
					code = renderError(f, err)
				}
				if code != tc.code {
					t.Fatalf("exit=%d want %d: %v", code, tc.code, err)
				}
				if tc.status == 200 {
					var expected bytes.Buffer
					if e := json.Indent(&expected, []byte(tc.body), "", "  "); e != nil {
						t.Fatal(e)
					}
					if out.String() != expected.String()+"\n" || errOut.String() != "" {
						t.Fatalf("envelope/streams changed: stdout=%q stderr=%q", out.String(), errOut.String())
					}
				} else if jsonMode {
					var envelope struct {
						Error struct {
							Code int `json:"code"`
						} `json:"error"`
					}
					if e := json.Unmarshal([]byte(out.String()), &envelope); e != nil || envelope.Error.Code != tc.code || errOut.String() != "" {
						t.Fatalf("JSON error streams: stdout=%q stderr=%q err=%v", out.String(), errOut.String(), e)
					}
				} else if out.String() != "" || errOut.String() == "" {
					t.Fatalf("text error streams: stdout=%q stderr=%q", out.String(), errOut.String())
				}
				if tc.status != 0 && calls != 1 {
					t.Fatalf("requests=%d want 1", calls)
				}
				if tc.status == 0 && calls != 0 {
					t.Fatalf("the unreachable case must not reach the server, requests=%d", calls)
				}
			})
		}
	}
}
