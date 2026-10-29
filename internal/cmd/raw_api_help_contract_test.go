package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
		{"unreachable", 0, "", 7},
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
				if tc.status == 0 {
					srv.Close()
				}
				f, out := testFactory(t)
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
			})
		}
	}
}
