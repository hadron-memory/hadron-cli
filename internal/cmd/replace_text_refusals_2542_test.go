package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Pre-existing helpers this file relies on, all in package cmd on main:
// testFactory (root_test.go), captureGraphQL (commands_test.go), and
// exitcode.Usage / exitcode.FromError (internal/exitcode).

// replaceRecorder2542 answers SearchReplaceInNodes with a fixed result and
// records each request's dryRun flag, so a test can tell "no request" from
// "a preview, no write".
type replaceRecorder2542 struct {
	mu   sync.Mutex
	dry  []bool
	srv  *httptest.Server
	body string
}

func newReplaceRecorder2542(t *testing.T, result string) *replaceRecorder2542 {
	t.Helper()
	r := &replaceRecorder2542{body: result}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("replace recorder: reading the request body: %v", err)
			http.Error(w, "unreadable request", http.StatusBadRequest)
			return
		}
		var b struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input struct {
					DryRun *bool `json:"dryRun"`
				} `json:"input"`
			} `json:"variables"`
		}
		if err = json.Unmarshal(raw, &b); err != nil {
			t.Errorf("replace recorder: undecodable request body: %v", err)
			http.Error(w, "undecodable request", http.StatusBadRequest)
			return
		}
		// Only the replace operation with an explicit dryRun is a countable
		// request; anything else is a test failure, not a silent "write".
		if b.OperationName != "SearchReplaceInNodes" || b.Variables.Input.DryRun == nil {
			t.Errorf("replace recorder: unexpected request (operation %q, dryRun set: %v): %s",
				b.OperationName, b.Variables.Input.DryRun != nil, raw)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.dry = append(r.dry, *b.Variables.Input.DryRun)
		r.mu.Unlock()
		_, _ = w.Write([]byte(r.body))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// sequence returns the requests in arrival order, as dryRun values.
func (r *replaceRecorder2542) sequence() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.dry...)
}

func (r *replaceRecorder2542) counts() (total, writes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.dry {
		total++
		if !d {
			writes++
		}
	}
	return
}

const replaceThreeNodes2542 = `{"data":{"searchReplaceInNodes":{"nodesScanned":5,"nodesSelected":3,"nodesSkipped":0,"nodesChanged":3,"totalReplacements":3,"dryRun":true,"results":[` +
	`{"nodeId":"a","loc":"a","memoryId":"m","replacements":1,"fields":[{"field":"CONTENT","matches":1}]},` +
	`{"nodeId":"b","loc":"b","memoryId":"m","replacements":1,"fields":[{"field":"CONTENT","matches":1}]},` +
	`{"nodeId":"c","loc":"c","memoryId":"m","replacements":1,"fields":[{"field":"CONTENT","matches":1}]}]}}}`

const replaceNoMatch2542 = `{"data":{"searchReplaceInNodes":{"nodesScanned":5,"nodesSelected":0,"nodesSkipped":0,"nodesChanged":0,"totalReplacements":0,"dryRun":true,"results":[]}}}`

// cli#1051 / cli#2542: the replace text help says a refusal before any write
// exits 2 and changes nothing. Pin that for each of the six refusals: exit 2
// and never a real (non-dry-run) request; the refusals that precede the
// preview send no request at all.
func TestReplaceTextRefusalsExitTwoAndWriteNothing2542(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantRequest bool // --max-nodes refuses only after the read-only preview
		wantMsg     string
	}{
		{"no --yes without a terminal", []string{"a", "b", "-m", "acme.com::kb", "--field", "content"}, false, "refusing to write without --yes"},
		{"more nodes than --max-nodes", []string{"a", "b", "-m", "acme.com::kb", "--field", "content", "--yes", "--max-nodes", "2"}, true, "exceeds --max-nodes=2"},
		{"empty <old>", []string{"", "b", "-m", "acme.com::kb", "--field", "content", "--yes"}, false, "<old> must not be empty"},
		{"missing --field", []string{"a", "b", "-m", "acme.com::kb", "--yes"}, false, "pass at least one --field"},
		{"missing --node and --memory", []string{"a", "b", "--field", "content", "--yes"}, false, "at least one --node or --memory"},
		{"--prefix without --memory", []string{"a", "b", "--node", "hrn:node:acme.com:kb:x", "--prefix", "p", "--field", "content", "--yes"}, false, "--prefix requires --memory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newReplaceRecorder2542(t, replaceThreeNodes2542)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"replace", "text"}, tc.args...), "--server", rec.srv.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("exit = %d, want %d (usage): %v", got, exitcode.Usage, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("message %q lacks %q", err.Error(), tc.wantMsg)
			}
			total, writes := rec.counts()
			if writes != 0 {
				t.Errorf("a refusal must never send a write; %d real request(s)", writes)
			}
			if tc.wantRequest && total == 0 {
				t.Error("expected the read-only preview before this refusal")
			}
			if !tc.wantRequest && total != 0 {
				t.Errorf("this refusal precedes any request; %d sent", total)
			}
		})
	}
}

// The help's no-match claim: the line on stderr, no write, exit 0.
func TestReplaceTextNoMatchWritesNothingAndExitsZero2542(t *testing.T) {
	rec := newReplaceRecorder2542(t, replaceNoMatch2542)
	f, _ := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	root.SetArgs([]string{"replace", "text", "a", "b", "-m", "acme.com::kb", "--field", "content", "--yes", "--server", rec.srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "No matches — nothing to replace.") {
		t.Errorf("stderr lacks the no-match line: %q", stderr.String())
	}
	if total, writes := rec.counts(); total != 1 || writes != 0 {
		t.Errorf("want one preview and no write, got %d requests, %d writes", total, writes)
	}
}

// Positive control for the table above: the same command, with the preview
// showing matches and nothing refusing, DOES send a real (non-dry-run) write,
// so "writes == 0" in the refusal rows can only mean the refusal held.
func TestReplaceTextAppliedRunSendsARealWrite2542(t *testing.T) {
	rec := newReplaceRecorder2542(t, replaceThreeNodes2542)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"replace", "text", "a", "b", "-m", "acme.com::kb", "--field", "content", "--yes", "--server", rec.srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := rec.sequence(); len(got) != 2 || got[0] != true || got[1] != false {
		t.Errorf("want a preview (dryRun true) then one write (dryRun false), got dryRun sequence %v", got)
	}
}
