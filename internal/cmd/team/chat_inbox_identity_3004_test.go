package team

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#3004 — `team chat inbox` must name the stage and the cause when
// its App-identity step fails, instead of "cannot read inbox binding" for a
// binding file that is fine. Each row drives the real command against a fake
// server whose TeamAppIdentity answer is the row's, with the binding's App id
// textually different from the App context (the case that makes the CLI ask
// the server), and pins the message AND the exit class (which a server fault
// must never change from what it is today).

const inboxIdentityLeak3004 = "PRIVATE-BODY-SENTINEL-3004"

type inboxIdentityRun3004 struct {
	err      error
	requests int
}

// driveInboxIdentity3004 writes a complete binding (App id "own-app"), points a
// fake server at identity, runs `team chat inbox` and returns the outcome. A
// nil answer means the identity call must never be sent.
func driveInboxIdentity3004(t *testing.T, appFlag string, answer http.HandlerFunc, bindingRaw string) inboxIdentityRun3004 {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "synthetic-token-3004")
	dir := t.TempDir()
	t.Setenv(GitDirEnv, dir)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var q struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		if q.OperationName != "TeamAppIdentity" {
			t.Errorf("only the identity read may be sent before the App is confirmed, got %q", q.OperationName)
		}
		if answer == nil {
			t.Error("no request was expected")
			return
		}
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	f := cmdutil.NewFactory()
	f.ServerFlag = srv.URL
	f.AppFlag = appFlag
	f.IOStreams, _, _ = output.Test()
	raw := bindingRaw
	if raw == "" {
		b, _ := json.Marshal(binding{AppID: "own-app", WorkerID: "self", SessionID: "session", Server: srv.URL})
		raw = string(b)
	}
	if err := os.WriteFile(filepath.Join(dir, bindingFileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newCmdTeamChatInbox(f)
	c.SetArgs([]string{})
	c.SilenceUsage, c.SilenceErrors = true, true
	err := c.Execute()
	return inboxIdentityRun3004{err: err, requests: requests}
}

func TestInboxIdentityStageFailuresNameStageAndCause3004(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  http.HandlerFunc
		exit    int
		want    []string
		wantNot []string
	}{
		{
			name: "502",
			answer: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, inboxIdentityLeak3004, http.StatusBadGateway)
			},
			exit: exitcode.Unavailable,
			want: []string{"could not confirm the App for this worktree", "server unavailable"},
		},
		{
			name: "503",
			answer: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, inboxIdentityLeak3004, http.StatusServiceUnavailable)
			},
			exit: exitcode.Unavailable,
			want: []string{"could not confirm the App for this worktree", "server unavailable"},
		},
		{
			name: "INTERNAL_SERVER_ERROR",
			answer: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"errors":[{"message":"` + inboxIdentityLeak3004 + `","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`))
			},
			exit: exitcode.Error,
			want: []string{"could not confirm the App for this worktree", "server error"},
		},
		{
			// The one row that was already right: a known reason, named.
			name: "mismatching id",
			answer: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"app":{"id":"different-app"}}}`))
			},
			exit: exitcode.Conflict,
			want: []string{"INBOX_APP_MISMATCH: inbox App differs from the worker binding"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := driveInboxIdentity3004(t, "hrn:app:test:other", tc.answer, "")
			if run.err == nil {
				t.Fatal("the identity failure must fail the command")
			}
			if got := exitcode.FromError(run.err); got != tc.exit {
				t.Fatalf("exit %d, want %d (the class a server fault has today): %v", got, tc.exit, run.err)
			}
			msg := run.err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Fatalf("message must contain %q: %q", w, msg)
				}
			}
			// The binding file is fine, so the binding must not be blamed.
			if strings.Contains(msg, "cannot read inbox binding") {
				t.Fatalf("a failed App-identity step must not be reported as a binding fault: %q", msg)
			}
			// No raw response body, no URL, no token in the public message.
			for _, leak := range []string{inboxIdentityLeak3004, "http://", "https://", "synthetic-token-3004", "127.0.0.1"} {
				if strings.Contains(msg, leak) {
					t.Fatalf("the message leaks %q: %q", leak, msg)
				}
			}
			if run.requests != 1 {
				t.Fatalf("exactly the identity read, got %d requests", run.requests)
			}
		})
	}
}

// A malformed --app value is a usage error naming the VALUE's problem, sent
// nowhere, and never blamed on the binding.
func TestInboxIdentityInvalidAppValueIsAUsageErrorNamingTheValue3004(t *testing.T) {
	run := driveInboxIdentity3004(t, "app-id-1", nil, "")
	if run.err == nil {
		t.Fatal("an invalid --app must fail")
	}
	if got := exitcode.FromError(run.err); got != exitcode.Usage {
		t.Fatalf("exit %d, want usage 2: %v", got, run.err)
	}
	msg := run.err.Error()
	if !strings.Contains(msg, "--app") || strings.Contains(msg, "cannot read inbox binding") {
		t.Fatalf("the refusal must name the --app value's problem, not the binding: %q", msg)
	}
	if run.requests != 0 {
		t.Fatalf("an invalid --app must send nothing, got %d requests", run.requests)
	}
}

// A binding that is genuinely unreadable keeps today's message and class.
func TestInboxIdentityUnreadableBindingKeepsItsMessage3004(t *testing.T) {
	run := driveInboxIdentity3004(t, "hrn:app:test:other", nil, `{"appId":"","workerId":"self"}`)
	if run.err == nil {
		t.Fatal("an unreadable binding must fail")
	}
	if got := exitcode.FromError(run.err); got != exitcode.Usage {
		t.Fatalf("exit %d, want usage 2: %v", got, run.err)
	}
	if !strings.Contains(run.err.Error(), "INBOX_UNBOUND") || strings.Contains(run.err.Error(), "could not confirm the App") {
		t.Fatalf("a genuinely unreadable binding keeps its own reason: %v", run.err)
	}
	if run.requests != 0 {
		t.Fatalf("no request for an unreadable binding, got %d", run.requests)
	}
}
