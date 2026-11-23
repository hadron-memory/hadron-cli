package team

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
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

// inboxIdentityOpts3004 shapes one run. answer receives the 1-based index of
// the identity read it answers; nil means no request may be sent.
type inboxIdentityOpts3004 struct {
	appFlag    string
	answer     func(n int, w http.ResponseWriter, r *http.Request)
	bindingRaw string
	noToken    bool
	configErr  error
	args       []string
}

// driveInboxIdentity3004 writes a complete binding (App id "own-app"), points a
// fake server at the identity read, runs `team chat inbox` and returns the
// outcome.
func driveInboxIdentity3004(t *testing.T, o inboxIdentityOpts3004) inboxIdentityRun3004 {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if o.noToken {
		t.Setenv("HADRON_TOKEN", "")
	} else {
		t.Setenv("HADRON_TOKEN", "synthetic-token-3004")
	}
	dir := t.TempDir()
	t.Setenv(GitDirEnv, dir)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1))
		var q struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		if q.OperationName != "TeamAppIdentity" {
			t.Errorf("only the identity read may be sent before the App is confirmed, got %q", q.OperationName)
		}
		if o.answer == nil {
			t.Error("no request was expected")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		o.answer(n, w, r)
	}))
	t.Cleanup(srv.Close)
	f := cmdutil.NewFactory()
	f.ServerFlag = srv.URL
	f.AppFlag = o.appFlag
	if o.configErr != nil {
		f.ConfigFn = func() (*config.Config, error) { return nil, o.configErr }
	}
	f.IOStreams, _, _ = output.Test()
	raw := o.bindingRaw
	if raw == "" {
		b, err := json.Marshal(binding{AppID: "own-app", WorkerID: "self", SessionID: "session", Server: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		raw = string(b)
	}
	if err := os.WriteFile(filepath.Join(dir, bindingFileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newCmdTeamChatInbox(f)
	c.SetArgs(o.args)
	c.SilenceUsage, c.SilenceErrors = true, true
	err := c.Execute()
	return inboxIdentityRun3004{err: err, requests: int(requests.Load())}
}

// leaks3004 lists what no public message may carry: the response body, any
// URL, the token and the loopback host.
var leaks3004 = []string{inboxIdentityLeak3004, "://", "synthetic-token-3004", "127.0.0.1"}

func assertNoLeak3004(t *testing.T, msg string) {
	t.Helper()
	for _, leak := range leaks3004 {
		if strings.Contains(msg, leak) {
			t.Fatalf("the message leaks %q: %q", leak, msg)
		}
	}
}

func gqlCode3004(code string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"` + inboxIdentityLeak3004 + `","extensions":{"code":"` + code + `"}}],"data":null}`))
	}
}

func httpStatus3004(status int) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		http.Error(w, inboxIdentityLeak3004, status)
	}
}

func TestInboxIdentityStageFailuresNameStageAndCause3004(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  func(int, http.ResponseWriter, *http.Request)
		exit    int
		want    []string
		wantNot []string
	}{
		{
			name:   "502",
			answer: httpStatus3004(http.StatusBadGateway),
			exit:   exitcode.Unavailable,
			want:   []string{"could not confirm the App for this worktree", "server unavailable", "this read is safe to retry"},
		},
		{
			name:   "503",
			answer: httpStatus3004(http.StatusServiceUnavailable),
			exit:   exitcode.Unavailable,
			want:   []string{"could not confirm the App for this worktree", "server unavailable", "this read is safe to retry"},
		},
		{
			name:   "INTERNAL_SERVER_ERROR",
			answer: gqlCode3004("INTERNAL_SERVER_ERROR"),
			exit:   exitcode.Error,
			want:   []string{"could not confirm the App for this worktree", "server error"},
		},
		{
			// The one row that was already right: a known reason, named.
			name: "mismatching id",
			answer: func(_ int, w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"app":{"id":"different-app"}}}`))
			},
			exit: exitcode.Conflict,
			want: []string{"INBOX_APP_MISMATCH: inbox App differs from the worker binding"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := driveInboxIdentity3004(t, inboxIdentityOpts3004{appFlag: "hrn:app:test:other", answer: tc.answer})
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
			assertNoLeak3004(t, msg)
			if run.requests != 1 {
				t.Fatalf("exactly the identity read, got %d requests", run.requests)
			}
		})
	}
}

// A malformed --app value is a usage error naming the VALUE's problem, sent
// nowhere, and never blamed on the binding.
func TestInboxIdentityInvalidAppValueIsAUsageErrorNamingTheValue3004(t *testing.T) {
	run := driveInboxIdentity3004(t, inboxIdentityOpts3004{appFlag: "app-id-1"})
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
	// The value is never echoed (cli#2876): a token pasted into --app must not
	// reach the terminal, and a later change must not start printing it.
	if strings.Contains(msg, "app-id-1") {
		t.Fatalf("the refusal must not echo the --app value: %q", msg)
	}
	if run.requests != 0 {
		t.Fatalf("an invalid --app must send nothing, got %d requests", run.requests)
	}
}

// A binding that is genuinely unreadable keeps today's message and class.
func TestInboxIdentityUnreadableBindingKeepsItsMessage3004(t *testing.T) {
	run := driveInboxIdentity3004(t, inboxIdentityOpts3004{appFlag: "hrn:app:test:other", bindingRaw: `{"appId":"","workerId":"self"}`})
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

// Server codes that already have a fixed public message keep it when they come
// from the identity read (the stage wrapper must not swallow them).
func TestInboxIdentityKnownServerCodesKeepTheirFixedMessage3004(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
		want string
	}{
		{"UNAUTHENTICATED", exitcode.AuthRequired, "UNAUTHENTICATED: the inbox caller is not authenticated"},
		{"FORBIDDEN", exitcode.Forbidden, "FORBIDDEN: this caller is not permitted"},
		{"APP_NOT_FOUND", exitcode.NotFound, "APP_NOT_FOUND: the inbox App was not found for this caller"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			run := driveInboxIdentity3004(t, inboxIdentityOpts3004{appFlag: "hrn:app:test:other", answer: gqlCode3004(tc.code)})
			if run.err == nil || exitcode.FromError(run.err) != tc.exit {
				t.Fatalf("exit %d, want %d: %v", exitcode.FromError(run.err), tc.exit, run.err)
			}
			if !strings.Contains(run.err.Error(), tc.want) {
				t.Fatalf("want %q: %q", tc.want, run.err.Error())
			}
			assertNoLeak3004(t, run.err.Error())
		})
	}
}

// A failure to SET UP the server call is local: nothing is sent, and the
// message must not claim the server failed to answer.
func TestInboxIdentityClientSetupFailureIsNotAServerFault3004(t *testing.T) {
	run := driveInboxIdentity3004(t, inboxIdentityOpts3004{appFlag: "hrn:app:test:other", noToken: true})
	if run.err == nil {
		t.Fatal("a missing credential must fail")
	}
	if got := exitcode.FromError(run.err); got != exitcode.AuthRequired {
		t.Fatalf("exit %d, want 3: %v", got, run.err)
	}
	msg := run.err.Error()
	// The auth remedy is part of the contract: a missing credential says how to fix it.
	if !strings.Contains(msg, "not authenticated; run hadron auth login or set HADRON_TOKEN") {
		t.Fatalf("a missing credential must carry its remedy: %q", msg)
	}
	if !strings.Contains(msg, "could not confirm the App for this worktree") || !strings.Contains(msg, "could not set up its server call") {
		t.Fatalf("the message must name the client-setup stage: %q", msg)
	}
	if strings.Contains(msg, "server unavailable") || strings.Contains(msg, "server error") || strings.Contains(msg, "cannot read inbox binding") {
		t.Fatalf("a local setup failure must not read as a server fault or a binding fault: %q", msg)
	}
	if run.requests != 0 {
		t.Fatalf("nothing may be sent, got %d requests", run.requests)
	}
}

// The ack-only path reads the identity a SECOND time before acknowledging. A
// failure there says it could not RE-confirm, with the same class, and no
// acknowledgment is attempted.
func TestInboxIdentityRecheckFailureSaysReconfirm3004(t *testing.T) {
	ackFile := filepath.Join(t.TempDir(), "ack.json")
	if err := os.WriteFile(ackFile, []byte(`{"batchIds":["proof-1"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		second func(int, http.ResponseWriter, *http.Request)
		exit   int
		want   string
	}{
		{"502", httpStatus3004(http.StatusBadGateway), exitcode.Unavailable, "could not re-confirm the App for this worktree: server unavailable"},
		{"INTERNAL_SERVER_ERROR", gqlCode3004("INTERNAL_SERVER_ERROR"), exitcode.Error, "could not re-confirm the App for this worktree: server error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := driveInboxIdentity3004(t, inboxIdentityOpts3004{
				appFlag: "hrn:app:test:other",
				args:    []string{"--ack-file", ackFile},
				answer: func(n int, w http.ResponseWriter, r *http.Request) {
					if n == 1 {
						_, _ = w.Write([]byte(`{"data":{"app":{"id":"own-app"}}}`))
						return
					}
					tc.second(n, w, r)
				},
			})
			if run.err == nil || exitcode.FromError(run.err) != tc.exit {
				t.Fatalf("exit %d, want %d: %v", exitcode.FromError(run.err), tc.exit, run.err)
			}
			if !strings.Contains(run.err.Error(), tc.want) {
				t.Fatalf("want %q: %q", tc.want, run.err.Error())
			}
			assertNoLeak3004(t, run.err.Error())
			if run.requests != 2 {
				t.Fatalf("two identity reads and nothing else, got %d requests", run.requests)
			}
		})
	}
}

// An App context that cannot be READ at all (a config fault, not a malformed
// value) is not the CLI's own usage text: it may carry a path or server text,
// so only the stage and the class are printed (BLIND F1).
func TestInboxIdentityUnreadableAppContextPrintsOnlyStageAndClass3004(t *testing.T) {
	run := driveInboxIdentity3004(t, inboxIdentityOpts3004{
		configErr: errors.New("read /secret/path-3004/config.json: " + inboxIdentityLeak3004),
	})
	if run.err == nil {
		t.Fatal("an unreadable App context must fail")
	}
	if got := exitcode.FromError(run.err); got != exitcode.Error {
		t.Fatalf("exit %d, want the cause's class 1: %v", got, run.err)
	}
	msg := run.err.Error()
	// Exact, so a local fault can never pick up server wording (QA P3) and a
	// stray word cannot hide behind a substring match.
	const want = "could not read the App context: a local configuration problem (`hadron config get app` shows what is set)"
	if msg != want {
		t.Fatalf("the message must be exactly %q, got %q", want, msg)
	}
	if strings.Contains(msg, "/secret/path-3004") || strings.Contains(msg, "cannot read inbox binding") {
		t.Fatalf("the message must not echo the cause text or blame the binding: %q", msg)
	}
	assertNoLeak3004(t, msg)
	if run.requests != 0 {
		t.Fatalf("nothing may be sent, got %d requests", run.requests)
	}
}

// The App-context stage prints only fixed CLI wording whatever the cause's own
// text says, even for a usage-coded cause, on both reads (BLIND delta F1 and the
// "every Usage error is CLI-authored" doubt): unit-driven because the real
// factory cannot fail differently on the second read.
func TestInboxIdentityFailureAppContextIsFixedWordingOnBothReads3004(t *testing.T) {
	const own = "/secret/path-3004/config.toml: " + inboxIdentityLeak3004
	for _, tc := range []struct {
		name    string
		cause   error
		recheck bool
		exit    int
		want    string
	}{
		{"usage first read", exitcode.Newf(exitcode.Usage, "%s", own), false, exitcode.Usage, "could not read the App context (--app or the configured App): it is not a valid App reference"},
		{"usage recheck", exitcode.Newf(exitcode.Usage, "%s", own), true, exitcode.Usage, "could not re-read the App context (--app or the configured App): it is not a valid App reference"},
		{"config fault first read", errors.New(own), false, exitcode.Error, "could not read the App context: a local configuration problem"},
		{"config fault recheck", errors.New(own), true, exitcode.Error, "could not re-read the App context: a local configuration problem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := inboxIdentityFailure(&inboxIdentityStageError{stage: inboxStageAppContext, err: tc.cause}, "cannot read inbox binding", tc.recheck)
			if got := exitcode.FromError(err); got != tc.exit {
				t.Fatalf("exit %d, want %d: %v", got, tc.exit, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q: %q", tc.want, err.Error())
			}
			if strings.Contains(err.Error(), "/secret/path-3004") || strings.Contains(err.Error(), inboxIdentityLeak3004) {
				t.Fatalf("the cause's own text must never be printed: %q", err.Error())
			}
		})
	}
}

// A client-setup failure that is not a missing credential (a bad configured
// server URL, say) says so in local wording and never in server wording
// (LOCAL delta F1/F2): unit-driven, since the real factory only fails here for a
// missing credential in this harness.
func TestInboxIdentityClientSetupOtherFailureUsesLocalWording3004(t *testing.T) {
	for _, recheck := range []bool{false, true} {
		err := inboxIdentityFailure(&inboxIdentityStageError{stage: inboxStageClient, err: errors.New("parse /secret/path-3004: " + inboxIdentityLeak3004)}, "cannot read inbox binding", recheck)
		if got := exitcode.FromError(err); got != exitcode.Error {
			t.Fatalf("recheck=%v: exit %d, want 1: %v", recheck, got, err)
		}
		msg := err.Error()
		verb := "could not confirm"
		if recheck {
			verb = "could not re-confirm"
		}
		want := verb + " the App for this worktree: the CLI could not set up its server call (check the server and credential configuration)"
		if msg != want {
			t.Fatalf("recheck=%v: got %q, want %q", recheck, msg, want)
		}
		if strings.Contains(msg, "/secret/path-3004") || strings.Contains(msg, inboxIdentityLeak3004) || strings.Contains(msg, "server unavailable") || strings.Contains(msg, "server error") {
			t.Fatalf("recheck=%v: local setup failure printed cause text or server wording: %q", recheck, msg)
		}
	}
}
