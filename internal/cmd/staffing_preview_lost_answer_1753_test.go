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

// loseAnswerAfterBackend proxies every request to backend. For the named
// operation it lets the backend run and answer (so the server has recorded
// whatever it records), then throws that answer away and ends the connection:
// "closed" sends nothing back, "truncated" sends the status line and headers
// and part of the body. sent counts the requests for the operation and
// executed counts the ones the backend answered with data and no GraphQL
// error before the answer was lost.
func loseAnswerAfterBackend(t *testing.T, backend, op, mode string, sent, executed *atomic.Int32) *httptest.Server {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
			http.Error(w, "read", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var q struct {
			OperationName string `json:"operationName"`
		}
		if json.Unmarshal(raw, &q) != nil || q.OperationName != op {
			proxy.ServeHTTP(w, r)
			return
		}
		sent.Add(1)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, r)
		var answered struct {
			Data   json.RawMessage   `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if rec.Code == http.StatusOK && json.Unmarshal(rec.Body.Bytes(), &answered) == nil && len(answered.Data) > 0 && string(answered.Data) != "null" && len(answered.Errors) == 0 {
			executed.Add(1)
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if mode == "truncated" {
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n{\"data\":"))
		}
		_ = conn.Close()
	}))
	t.Cleanup(front.Close)
	return front
}

// claims counts the claim events the fake backend recorded: the instructed
// preview records one when it runs, the plain preview none.
func assertLostAnswerNote1753(t *testing.T, err error, out string, sent, executed, claims *atomic.Int32, wantNote bool) {
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
	if executed.Load() != 1 {
		t.Errorf("the backend ran the preview %d times before its answer was lost, want 1", executed.Load())
	}
	wantClaims := int32(0)
	if wantNote {
		wantClaims = 1
	}
	if claims.Load() != wantClaims {
		t.Errorf("the backend recorded %d claim(s) before the answer was lost, want %d", claims.Load(), wantClaims)
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
	if !wantNote && !strings.Contains(err.Error(), "retry shortly") {
		t.Errorf("a plain preview keeps the ordinary transport guidance: %v", err)
	}
}

// lostRun is one preview whose answer was lost after the backend ran it.
type lostRun struct {
	out                    string
	sent, executed, claims *atomic.Int32
	err                    error
}

func rmLostAnswer1753(t *testing.T, mode string, extra ...string) lostRun {
	t.Helper()
	var sent, executed, claims atomic.Int32
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "Workers", "WorkersRoster":
			return staffJSON
		case "GetWorkerInApp":
			return `{"data":{"worker":` + irisWorkerJSON + `}}`
		case "DeleteWorkerPreview", "DeleteWorkerPreviewInstructed":
			// The instructed preview records its claim event as it runs.
			if op == "DeleteWorkerPreviewInstructed" {
				claims.Add(1)
			}
			return previewOK1735
		}
		return ""
	})
	op := "DeleteWorkerPreview"
	if len(extra) > 0 {
		op = "DeleteWorkerPreviewInstructed"
	}
	front := loseAnswerAfterBackend(t, gql.URL, op, mode, &sent, &executed)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "rm", "Iris", "--app", "acme.com:eng-team", "--server", front.URL, "--dry-run"}, extra...))
	err := root.Execute()
	return lostRun{out.String(), &sent, &executed, &claims, err}
}

func castLostAnswer1753(t *testing.T, mode string, instructed bool) lostRun {
	t.Helper()
	writeTeamBinding(t)
	responses := responses1594()
	var sent, executed, claims atomic.Int32
	backend, _ := attnServerHook(t, responses, func(op string) {
		if op == "CastWorkerPreviewInstructed" {
			claims.Add(1)
		}
	})
	op := "CastWorkerPreview"
	if instructed {
		op = "CastWorkerPreviewInstructed"
	}
	front := loseAnswerAfterBackend(t, backend.URL, op, mode, &sent, &executed)
	setTeamBindingServer(t, front.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"team", "worker", "cast", "--app", app1594, "--name", "Uma", "--agent", agent1594, "--no-team", "--dry-run", "--server", front.URL}
	if instructed {
		args = append(args, "--instruction", instrTask1592)
	}
	root.SetArgs(args)
	err := root.Execute()
	return lostRun{out.String(), &sent, &executed, &claims, err}
}

func TestWorkerRmInstructedPreviewLostAnswerIsUnknownOutcome1753(t *testing.T) {
	for _, mode := range []string{"closed", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			r := rmLostAnswer1753(t, mode, "--instruction", instrTask1592)
			assertLostAnswerNote1753(t, r.err, r.out, r.sent, r.executed, r.claims, true)
		})
	}
}

func TestWorkerRmPlainPreviewLostAnswerKeepsOrdinaryWording1753(t *testing.T) {
	r := rmLostAnswer1753(t, "closed")
	assertLostAnswerNote1753(t, r.err, r.out, r.sent, r.executed, r.claims, false)
}

func TestWorkerCastInstructedPreviewLostAnswerIsUnknownOutcome1753(t *testing.T) {
	for _, mode := range []string{"closed", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			r := castLostAnswer1753(t, mode, true)
			assertLostAnswerNote1753(t, r.err, r.out, r.sent, r.executed, r.claims, true)
		})
	}
}

func TestWorkerCastPlainPreviewLostAnswerKeepsOrdinaryWording1753(t *testing.T) {
	r := castLostAnswer1753(t, "closed", false)
	assertLostAnswerNote1753(t, r.err, r.out, r.sent, r.executed, r.claims, false)
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
	if !strings.Contains(r.err.Error(), "Not permitted.") {
		t.Errorf("the server's own message must be kept: %v", r.err)
	}
	if got := exitCodeFor(r.err); got != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d", got, exitcode.Forbidden)
	}
}
