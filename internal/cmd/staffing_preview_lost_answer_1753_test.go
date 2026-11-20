package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1753: an instructed staffing preview is a GraphQL query that writes a
// claim (through a worker session the server records one claim event). A
// connection lost after the server committed it must read like any write with a
// lost answer: outcome unknown, nothing retried, exit 7. The plain preview
// writes nothing, so a lost answer to it keeps the ordinary wording.

// dropFront answers every request through a proxy to backend, except a request
// for the named operation, which it reads and then closes the connection on,
// sending no answer. sent counts those requests.
func dropFront(t *testing.T, backend, op string, sent *atomic.Int32) *httptest.Server {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var q struct {
			OperationName string `json:"operationName"`
		}
		if json.Unmarshal(raw, &q) == nil && q.OperationName == op {
			sent.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front
}

func assertLostAnswerNote1753(t *testing.T, err error, out string, sent *atomic.Int32, wantNote bool) {
	t.Helper()
	if err == nil || out != "" {
		t.Fatalf("want a failure with no stdout, got err=%v out=%q", err, out)
	}
	if got := exitCodeFor(err); got != exitcode.Unavailable {
		t.Errorf("exit = %d, want %d (unavailable): %v", got, exitcode.Unavailable, err)
	}
	if sent.Load() != 1 {
		t.Errorf("the preview was sent %d times, want 1 (no retry)", sent.Load())
	}
	has := strings.Contains(err.Error(), "may or may not have been applied")
	if has != wantNote {
		t.Errorf("note present = %v, want %v: %v", has, wantNote, err)
	}
	if wantNote && !strings.Contains(err.Error(), "verify the current state before retrying") {
		t.Errorf("the note must say to verify before retrying: %v", err)
	}
	if wantNote && strings.Contains(err.Error(), "retry shortly") {
		t.Errorf("an instructed preview must not invite a blind retry: %v", err)
	}
}

func rmLostAnswer1753(t *testing.T, extra ...string) (string, *atomic.Int32, error) {
	t.Helper()
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "Workers", "WorkersRoster":
			return staffJSON
		case "GetWorkerInApp":
			return `{"data":{"worker":` + irisWorkerJSON + `}}`
		}
		return ""
	})
	var sent atomic.Int32
	op := "DeleteWorkerPreview"
	if len(extra) > 0 {
		op = "DeleteWorkerPreviewInstructed"
	}
	front := dropFront(t, gql.URL, op, &sent)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "rm", "Iris", "--app", "acme.com:eng-team", "--server", front.URL, "--dry-run"}, extra...))
	err := root.Execute()
	return out.String(), &sent, err
}

func TestWorkerRmInstructedPreviewLostAnswerIsUnknownOutcome1753(t *testing.T) {
	out, sent, err := rmLostAnswer1753(t, "--instruction", instrTask1592)
	assertLostAnswerNote1753(t, err, out, sent, true)
}

func TestWorkerRmPlainPreviewLostAnswerKeepsOrdinaryWording1753(t *testing.T) {
	out, sent, err := rmLostAnswer1753(t)
	assertLostAnswerNote1753(t, err, out, sent, false)
}

func castLostAnswer1753(t *testing.T, instructed bool) (string, *atomic.Int32, error) {
	t.Helper()
	writeTeamBinding(t)
	responses := responses1594()
	backend, _ := attnServerHook(t, responses, nil)
	var sent atomic.Int32
	op := "CastWorkerPreview"
	if instructed {
		op = "CastWorkerPreviewInstructed"
	}
	front := dropFront(t, backend.URL, op, &sent)
	setTeamBindingServer(t, front.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"team", "worker", "cast", "--app", app1594, "--name", "Uma", "--agent", agent1594, "--no-team", "--dry-run", "--server", front.URL}
	if instructed {
		args = append(args, "--instruction", instrTask1592)
	}
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), &sent, err
}

func TestWorkerCastInstructedPreviewLostAnswerIsUnknownOutcome1753(t *testing.T) {
	out, sent, err := castLostAnswer1753(t, true)
	assertLostAnswerNote1753(t, err, out, sent, true)
}

func TestWorkerCastPlainPreviewLostAnswerKeepsOrdinaryWording1753(t *testing.T) {
	out, sent, err := castLostAnswer1753(t, false)
	assertLostAnswerNote1753(t, err, out, sent, false)
}

// A refusal the server answered is not a lost answer: it keeps its own
// wording and exit code, with no unknown-outcome note.
func TestWorkerRmInstructedPreviewRefusalKeepsItsOwnWording1753(t *testing.T) {
	r := runRm1735(t, `{"data":null,"errors":[{"message":"Not permitted.","extensions":{"code":"FORBIDDEN"}}]}`, "--dry-run", "--instruction", instrTask1592)
	if r.err == nil {
		t.Fatal("want a refusal")
	}
	if strings.Contains(r.err.Error(), "may or may not have been applied") {
		t.Errorf("a server refusal must not read as an unknown outcome: %v", r.err)
	}
	if got := exitCodeFor(r.err); got != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d", got, exitcode.Forbidden)
	}
}
