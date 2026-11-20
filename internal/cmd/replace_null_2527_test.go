package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #2527: `replace text` whose searchReplaceInNodes answers null or leaves the
// field out must not panic. A dry run (or the preview that every real run
// sends first) writes nothing, so a plain failure is enough. The APPLIED
// request has already been sent, so its outcome is unknown: exit 1 like
// #2493, no success output, and advice to read the affected nodes before
// retrying. Four shapes (null/absent x dry-run/applied) in text and --json,
// plus the preview of a real run answering the same way. The fake server
// routes on the request's dryRun variable (never a counter), so a request sent
// with the wrong dryRun fails the test instead of silently taking the other
// answer (cli#2634).
func TestReplaceTextNullResult2527(t *testing.T) {
	shapes := map[string]string{"null": `{"data":{"searchReplaceInNodes":null}}`, "absent": `{"data":{}}`}
	cases := []struct {
		name      string
		args      []string
		nullOnDry bool     // the request with this dryRun value gets the null/absent answer
		wantDry   []bool   // the dryRun value of every request that must be sent, in order
		wantInMsg []string // fragments of the error
	}{
		{"dry-run", []string{"--dry-run"}, true, []bool{true}, []string{"no result", "nothing was written"}},
		{"applied-preview", []string{"--yes"}, true, []bool{true}, []string{"no result", "nothing was written"}},
		// The preview (searchReplaceDryJSON) lists findings:flaky-ci: the error names it.
		{"applied", []string{"--yes"}, false, []bool{true, false}, []string{"no result", "outcome is unknown", "hadron node revision list", "the preview listed 1 node(s): findings:flaky-ci"}},
	}
	for shape, body := range shapes {
		for _, c := range cases {
			for _, jsonMode := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/json=%v", shape, c.name, jsonMode)
				t.Run(name, func(t *testing.T) {
					var n atomic.Int32
					var mu sync.Mutex
					var sentDry []bool
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n.Add(1)
						dry := requestDryRun(t, r)
						mu.Lock()
						sentDry = append(sentDry, dry)
						mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						switch {
						case dry == c.nullOnDry:
							_, _ = w.Write([]byte(body))
						case dry:
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
					mu.Lock()
					gotDry := append([]bool(nil), sentDry...)
					mu.Unlock()
					if int(n.Load()) != len(c.wantDry) || fmt.Sprint(gotDry) != fmt.Sprint(c.wantDry) {
						t.Fatalf("dryRun of the requests sent = %v, want %v (in order, never retried)", gotDry, c.wantDry)
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
						// The process-level path: Execute's own renderFailure on the
						// factory the command ran with, not a second factory.
						if code := renderFailure(f, argv, err); code != exitcode.Error {
							t.Fatalf("envelope exit %d", code)
						}
						var env struct {
							Error map[string]any `json:"error"`
						}
						if e := json.Unmarshal([]byte(out.String()), &env); e != nil || env.Error["code"] != float64(exitcode.Error) {
							t.Fatalf("no structured failure: %v %s", e, out.String())
						}
					}
				})
			}
		}
	}
}

// requestDryRun reads the dryRun variable of a searchReplaceInNodes request.
func requestDryRun(t *testing.T, r *http.Request) bool {
	t.Helper()
	var body struct {
		Variables struct {
			Input struct {
				DryRun *bool `json:"dryRun"`
			} `json:"input"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return body.Variables.Input.DryRun != nil && *body.Variables.Input.DryRun
}

// A success envelope whose searchReplaceInNodes is an empty object (every
// field at its zero value) is not null: it decodes to an empty report. Pinned
// as the chosen behaviour (cli#2634): a preview answers with the empty totals
// and exit 0, an applied run is the no-match no-op (stderr note, nothing
// written, a second request is never sent).
func TestReplaceTextEmptyObjectResult2527(t *testing.T) {
	const empty = `{"data":{"searchReplaceInNodes":{}}}`
	for _, c := range []struct {
		name       string
		args       []string
		wantOut    string
		wantStderr string
		wantSent   int
	}{
		{"dry-run", []string{"--dry-run"}, "Replaced 0 occurrence(s) across 0 of 0 node(s) scanned.\n", "", 1},
		{"applied", []string{"--yes"}, "Replaced 0 occurrence(s) across 0 of 0 node(s) scanned.\n", "No matches — nothing to replace.\n", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(empty))
			}))
			defer srv.Close()
			f, out := testFactory(t)
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"replace", "text", "cat", "dog", "-m", "acme.com::kb", "--field", "content", "--server", srv.URL}, c.args...))
			if err := root.Execute(); err != nil {
				t.Fatalf("an empty result object is an empty report, not an error: %v", err)
			}
			if int(n.Load()) != c.wantSent {
				t.Fatalf("sent %d requests, want %d", n.Load(), c.wantSent)
			}
			if out.String() != c.wantOut || stderr.String() != c.wantStderr {
				t.Fatalf("stdout=%q stderr=%q, want %q / %q", out.String(), stderr.String(), c.wantOut, c.wantStderr)
			}
		})
	}
}

// The applied error names at most five of the previewed nodes, then "and M
// more" (cli#2634), in the report's (sorted) order.
func TestReplaceTextNullResultCapsPreviewedNodes2527(t *testing.T) {
	var results []string
	for _, loc := range []string{"g", "f", "e", "d", "c", "b", "a"} {
		results = append(results, `{"nodeId":"n`+loc+`","loc":"`+loc+`","memoryId":"mem1","replacements":1,"fields":[{"field":"content","matches":1}]}`)
	}
	preview := `{"data":{"searchReplaceInNodes":{"nodesScanned":7,"nodesSelected":7,"nodesChanged":7,"totalReplacements":7,"dryRun":true,"results":[` + strings.Join(results, ",") + `]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requestDryRun(t, r) {
			_, _ = w.Write([]byte(preview))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"searchReplaceInNodes":null}}`))
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"replace", "text", "cat", "dog", "-m", "acme.com::kb", "--field", "content", "--yes", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a missing applied result must fail")
	}
	if want := "the preview listed 7 node(s): a, b, c, d, e and 2 more"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error lacks %q: %q", want, err.Error())
	}
}
