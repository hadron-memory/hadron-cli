package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #2527: `replace text` whose searchReplaceInNodes answers null or leaves the
// field out must not panic. A dry run (or the preview that every real run
// sends first) writes nothing, so a plain failure is enough. The APPLIED
// request has already been sent, so its outcome is unknown: exit 1 like
// #2493, no success output, and advice to read the affected nodes before
// retrying. Four shapes (null/absent x dry-run/applied) in text and --json,
// plus the preview of a real run answering the same way.
func TestReplaceTextNullResult2527(t *testing.T) {
	shapes := map[string]string{"null": `{"data":{"searchReplaceInNodes":null}}`, "absent": `{"data":{}}`}
	cases := []struct {
		name      string
		args      []string
		nullOn    int      // which request (1-based) gets the null/absent answer
		wantSent  int      // requests that must have been sent
		wantInMsg []string // fragments of the error
	}{
		{"dry-run", []string{"--dry-run"}, 1, 1, []string{"no result", "nothing was written"}},
		{"applied-preview", []string{"--yes"}, 1, 1, []string{"no result", "nothing was written"}},
		{"applied", []string{"--yes"}, 2, 2, []string{"no result", "outcome unknown", "hadron node revision list"}},
	}
	for shape, body := range shapes {
		for _, c := range cases {
			for _, jsonMode := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/json=%v", shape, c.name, jsonMode)
				t.Run(name, func(t *testing.T) {
					n := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n++
						w.Header().Set("Content-Type", "application/json")
						switch {
						case n == c.nullOn:
							_, _ = w.Write([]byte(body))
						case n == 1:
							_, _ = w.Write([]byte(searchReplaceDryJSON))
						default:
							_, _ = w.Write([]byte(searchReplaceRealJSON))
						}
					}))
					defer srv.Close()

					f, out := testFactory(t)
					root := NewRootCmd(f)
					argv := append([]string{"replace", "text", "cat", "dog", "-m", "acme.com::kb", "--field", "content", "--server", srv.URL}, c.args...)
					if jsonMode {
						argv = append(argv, "--json")
					}
					root.SetArgs(argv)
					var err error
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("panic on a %s result: %v", shape, r)
							}
						}()
						err = root.Execute()
					}()
					if n != c.wantSent {
						t.Fatalf("sent %d requests, want %d (never retried)", n, c.wantSent)
					}
					if err == nil {
						t.Fatalf("a missing result must not succeed: stdout=%q", out.String())
					}
					if code := exitCodeFor(err); code != exitcode.Error {
						t.Fatalf("exit %d, want %d: %v", code, exitcode.Error, err)
					}
					for _, want := range c.wantInMsg {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("message lacks %q: %q", want, err.Error())
						}
					}
					if out.String() != "" {
						t.Fatalf("no success output on a failed replace: %q", out.String())
					}
					if jsonMode {
						ef, jout := testFactory(t)
						ef.JSON = true
						if code := renderError(ef, err); code != exitcode.Error {
							t.Fatalf("envelope exit %d", code)
						}
						var env struct {
							Error map[string]any `json:"error"`
						}
						if e := json.Unmarshal([]byte(jout.String()), &env); e != nil || env.Error["code"] != float64(exitcode.Error) {
							t.Fatalf("no structured failure: %v %s", e, jout.String())
						}
					}
				})
			}
		}
	}
}
