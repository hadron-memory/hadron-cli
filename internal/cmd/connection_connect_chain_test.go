package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmd/connection"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// chainSentinels are the secret-bearing values of connectURL, in the forms an
// error graph can hold them: raw, and JSON-escaped (an HTTP error keeps the
// body it was served, in which "&" and "/" may be escaped).
func chainSentinels() []string {
	esc, _ := json.Marshal(connectURL)
	return []string{"SECRETNONCE123456", connectURL, "hadron.example/connect", string(esc[1 : len(esc)-1]), strings.ReplaceAll(connectURL, "&", `&`)}
}

// reachable reports whether any of needles appears in anything v can reach:
// strings, nested structs (unexported fields too), pointers, interfaces,
// slices and maps. It is how "the raw graph is gone" is asserted: not by what
// Error() prints, but by what the value retains.
func reachable(v reflect.Value, needles []string, seen map[uintptr]bool) (string, bool) {
	switch v.Kind() {
	case reflect.Invalid:
	case reflect.String:
		for _, n := range needles {
			if strings.Contains(v.String(), n) {
				return n, true
			}
		}
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return "", false
		}
		if v.Kind() == reflect.Pointer {
			if seen[v.Pointer()] {
				return "", false
			}
			seen[v.Pointer()] = true
		}
		return reachable(v.Elem(), needles, seen)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if n, ok := reachable(v.Field(i), needles, seen); ok {
				return n, ok
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if n, ok := reachable(v.Index(i), needles, seen); ok {
				return n, ok
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if n, ok := reachable(k, needles, seen); ok {
				return n, ok
			}
			if n, ok := reachable(v.MapIndex(k), needles, seen); ok {
				return n, ok
			}
		}
	}
	return "", false
}

// unwrapAll visits err and every error reachable through Unwrap() error and
// Unwrap() []error, so a joined graph is walked as well as a single chain.
func unwrapAll(err error, visit func(error)) {
	if err == nil {
		return
	}
	visit(err)
	switch u := err.(type) {
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			unwrapAll(e, visit)
		}
	case interface{ Unwrap() error }:
		unwrapAll(u.Unwrap(), visit)
	}
}

type chainStep struct {
	status int    // 0 = 200
	body   string // the response body
	drop   bool   // close the connection with no answer
	cancel bool   // cancel the command's context while answering
}

// newChainServer answers each operation from script (the n-th call of an
// operation takes its n-th step, the last repeating); onCancel runs for a step
// that asks for the command's context to be cancelled.
func newChainServer(t *testing.T, script map[string][]chainStep, onCancel func()) *httptest.Server {
	t.Helper()
	calls := map[string]int{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		steps := script[body.OperationName]
		if len(steps) == 0 {
			t.Errorf("unexpected operation %q", body.OperationName)
			return
		}
		mu.Lock()
		i := min(calls[body.OperationName], len(steps)-1)
		calls[body.OperationName]++
		mu.Unlock()
		s := steps[i]
		if s.cancel {
			onCancel()
		}
		if s.drop {
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					_ = c.Close()
				}
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if s.status != 0 {
			w.WriteHeader(s.status)
		}
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runConnectChain runs `connection connect` against a server that answers each
// operation from script (the n-th call of an operation takes its n-th step,
// the last repeating), and returns the command's error.
func runConnectChain(t *testing.T, script map[string][]chainStep) (*connectRun, error) {
	t.Helper()
	r := &connectRun{}
	prev := connection.Hooks
	t.Cleanup(func() { connection.Hooks = prev })
	clock := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	connection.Hooks.Now = func() time.Time { return clock }
	connection.Hooks.Sleep = func(_ context.Context, d time.Duration) error { clock = clock.Add(d); return nil }
	connection.Hooks.Open = func(string) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := newChainServer(t, script, cancel)

	f, out := testFactory(t)
	f.JSON = true
	root := NewRootCmd(f)
	root.SetContext(ctx)
	root.SetArgs([]string{"--server", srv.URL, "connection", "connect", "gmail", "--org", "org_1", "--json"})
	err := root.Execute()
	if err != nil {
		r.code = renderError(f, err)
	}
	r.out = out.String()
	r.errOut = f.IOStreams.ErrOut.(*strings.Builder).String()
	return r, err
}

func gqlErr(msg, ext string) string {
	return `{"errors":[{"message":"` + msg + `","extensions":{` + ext + `}}],"data":null}`
}

var leakMsg = `upstream said ` + connectURL + ` nonce SECRETNONCE123456`

// Every post-link failure leaves a returned error from which no part of the
// link is reachable (single chain or joined), while exit code, serverCode,
// public message and cancellation classification are exactly what they were.
func TestConnectionConnectReturnedErrorHoldsNoSecretGraph(t *testing.T) {
	start := []chainStep{{body: connectStart}}
	me := []chainStep{{body: connectMe}}
	rows := []chainStep{{body: connRows()}}
	type want struct {
		code       int
		serverCode string
		msg        string // substring of the public message
	}
	cases := []struct {
		name   string
		script map[string][]chainStep
		want   want
	}{
		{"authContext: secret in the message", map[string][]chainStep{
			"StartConnection": start,
			"AuthContext":     {{body: gqlErr(leakMsg, `"code":"FORBIDDEN"`)}}}, want{exitcode.Forbidden, "FORBIDDEN", "upstream said <redacted>"}},
		// The outer message is safe; the secret is only in what the error carries.
		{"authContext: safe message, secret in extensions", map[string][]chainStep{
			"StartConnection": start,
			"AuthContext":     {{body: gqlErr("refused", `"code":"FORBIDDEN","detail":"`+connectURL+`"`)}}}, want{exitcode.Forbidden, "FORBIDDEN", "refused"}},
		{"baseline list: safe message, secret in extensions, no code", map[string][]chainStep{
			"StartConnection": start, "AuthContext": me,
			"EmailConnections": {{body: gqlErr("refused", `"detail":"`+connectURL+`"`)}}}, want{exitcode.Error, "", "refused"}},
		{"poll: mapped refusal with a serverCode", map[string][]chainStep{
			"StartConnection": start, "AuthContext": me,
			"EmailConnections": {{body: connRows()}, {body: gqlErr("refused", `"code":"UNAUTHENTICATED","detail":"`+connectURL+`"`)}}}, want{exitcode.AuthRequired, "UNAUTHENTICATED", "refused"}},
		{"poll: secret in a non-200 body", map[string][]chainStep{
			"StartConnection": start, "AuthContext": me,
			"EmailConnections": {{body: connRows()}, {status: 400, body: gqlErr("bad", `"code":"BAD_USER_INPUT","detail":"`+connectURL+`"`)}}}, want{exitcode.Usage, "BAD_USER_INPUT", ""}},
		{"poll: gateway page naming the link", map[string][]chainStep{
			"StartConnection": start, "AuthContext": me,
			"EmailConnections": {{body: connRows()}, {status: 502, body: "<html>bad gateway for " + connectURL + "</html>"}}}, want{exitcode.Unavailable, "", ""}},
		{"poll: connection dropped (non-secret control)", map[string][]chainStep{
			"StartConnection": start, "AuthContext": me,
			"EmailConnections": {{body: connRows()}, {drop: true}}}, want{exitcode.Unavailable, "", "the connection dropped"}},
		{"poll: timeout (non-secret control)", map[string][]chainStep{
			"StartConnection": start, "AuthContext": me,
			"EmailConnections": rows}, want{exitcode.WaitTimeout, "", "not found yet"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := runConnectChain(t, tc.script)
			if err == nil {
				t.Fatal("want a failure")
			}
			if got := exitCodeFor(err); got != tc.want.code || r.code != tc.want.code {
				t.Errorf("exit = %d (rendered %d), want %d; err %v", got, r.code, tc.want.code, err)
			}
			if !strings.Contains(err.Error(), tc.want.msg) {
				t.Errorf("message %q lost %q", err.Error(), tc.want.msg)
			}
			if tc.want.serverCode != "" && !strings.Contains(r.out+r.errOut, `"serverCode": "`+tc.want.serverCode+`"`) {
				t.Errorf("serverCode %s lost: %q", tc.want.serverCode, r.out+r.errOut)
			}
			if tc.want.serverCode == "" && strings.Contains(r.out+r.errOut, "serverCode") {
				t.Errorf("serverCode appeared from nowhere: %q", r.out+r.errOut)
			}
			needles := chainSentinels()
			unwrapAll(err, func(e error) {
				if n, ok := reachable(reflect.ValueOf(e), needles, map[uintptr]bool{}); ok {
					t.Errorf("%T still holds %q", e, n)
				}
				if strings.Contains(fmt.Sprintf("%+v %#v", e, e), "SECRETNONCE") {
					t.Errorf("%T formats the nonce", e)
				}
			})
			for _, n := range needles {
				if strings.Contains(r.out+r.errOut, n) {
					t.Errorf("rendered output holds %q: %q", n, r.out+r.errOut)
				}
			}
		})
	}
}

// A joined error raised after the link is walked through every branch.
func TestConnectionConnectDetachedErrorDropsJoinedBranches(t *testing.T) {
	secret := errors.New("branch holding " + connectURL)
	joined := errors.Join(exitcode.Newf(exitcode.Conflict, "safe outer"), secret)
	got := apiDetached(joined)
	unwrapAll(got, func(e error) {
		if n, ok := reachable(reflect.ValueOf(e), chainSentinels(), map[uintptr]bool{}); ok {
			t.Errorf("%T still holds %q", e, n)
		}
	})
	if exitCodeFor(got) != exitcode.Conflict {
		t.Errorf("exit = %d", exitCodeFor(got))
	}
}

// Cancelling the command mid-poll ends as it did before (a transport failure,
// exit 7, whose mapped error never carried the context error), and nothing raw
// is reachable from it.
func TestConnectionConnectCancelledPollKeepsItsClassAndHoldsNoRawError(t *testing.T) {
	r, err := runConnectChain(t, map[string][]chainStep{
		"StartConnection":  {{body: connectStart}},
		"AuthContext":      {{body: connectMe}},
		"EmailConnections": {{body: connRows()}, {cancel: true, body: connRows()}},
	})
	if err == nil || r.code != exitcode.Unavailable {
		t.Fatalf("exit %d err %v, want 7", r.code, err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("a raw transport error is reachable: %T", ue)
	}
}

// Detached keeps a cancellation or deadline classification only when the raw
// chain carried it, and keeps ErrSilent likewise; it never invents one.
func TestDetachedKeepsOnlyTheClassificationsTheRawChainHad(t *testing.T) {
	id := func(s string) string { return s }
	for name, tc := range map[string]struct {
		raw                       error
		canceled, deadline, quiet bool
	}{
		"canceled":      {fmt.Errorf("poll: %w", context.Canceled), true, false, false},
		"deadline":      {exitcode.New(exitcode.Unavailable, fmt.Errorf("x: %w", context.DeadlineExceeded)), false, true, false},
		"silent":        {exitcode.Silent(exitcode.Conflict), false, false, true},
		"joined cancel": {errors.Join(errors.New("a"), context.Canceled), true, false, false},
		"plain":         {errors.New("boom"), false, false, false},
	} {
		got := api.Detached(tc.raw, id)
		if errors.Is(got, context.Canceled) != tc.canceled || errors.Is(got, context.DeadlineExceeded) != tc.deadline || errors.Is(got, exitcode.ErrSilent) != tc.quiet {
			t.Errorf("%s: classification differs from the raw chain", name)
		}
		if exitCodeFor(got) != exitCodeFor(tc.raw) || got.Error() != tc.raw.Error() {
			t.Errorf("%s: exit/message changed: %d %q", name, exitCodeFor(got), got.Error())
		}
		if errors.Unwrap(got) != nil && name != "silent" {
			if _, coded := got.(*exitcode.CodedError); !coded {
				t.Errorf("%s: unexpected chain %T", name, errors.Unwrap(got))
			}
		}
	}
}

func apiDetached(err error) error {
	return api.Detached(err, func(s string) string { return strings.ReplaceAll(s, connectURL, "<redacted>") })
}

// The real compiled binary, against a disposable synthetic HTTP fixture (no
// provider, no live server): its text and --json sinks carry the public
// message, exit code and serverCode, and never the link.
func TestCompiledConnectErrorSinksStayRedactedAndPublic(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "hadron")
	if out, err := exec.Command("go", "build", "-o", binary, "../../cmd/hadron").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// A stand-in launcher so no real browser can start.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	testFactory(t) // isolated config/token environment for the subprocess

	start := []chainStep{{body: connectStart}}
	me := []chainStep{{body: connectMe}}
	for name, tc := range map[string]struct {
		script map[string][]chainStep
		code   int
		msg    string
		sc     string
	}{
		"safe message over a secret extension": {map[string][]chainStep{"StartConnection": start,
			"AuthContext": {{body: gqlErr("refused", `"code":"FORBIDDEN","detail":"`+connectURL+`"`)}}}, exitcode.Forbidden, "refused", "FORBIDDEN"},
		"secret in the message": {map[string][]chainStep{"StartConnection": start,
			"AuthContext": me, "EmailConnections": {{body: connRows()}, {body: gqlErr(leakMsg, `"code":"UNAUTHENTICATED"`)}}}, exitcode.AuthRequired, "upstream said <redacted>", "UNAUTHENTICATED"},
		"gateway page naming the link": {map[string][]chainStep{"StartConnection": start,
			"AuthContext": me, "EmailConnections": {{body: connRows()}, {status: 502, body: "<html>" + connectURL + "</html>"}}}, exitcode.Unavailable, "HTTP 502", ""},
	} {
		srv := newChainServer(t, tc.script, func() {})
		for _, mode := range []string{"text", "json"} {
			args := []string{"--server", srv.URL, "connection", "connect", "gmail", "--org", "org_1"}
			if mode == "json" {
				args = append(args, "--json")
			} else {
				args = append(args, "--no-browser")
			}
			cmd := exec.Command(binary, args...)
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != tc.code {
				t.Errorf("%s/%s: exit %v, want %d", name, mode, err, tc.code)
			}
			all := stdout.String() + stderr.String()
			errText := stderr.String()
			if mode == "json" {
				errText = all
			}
			if !strings.Contains(errText, tc.msg) {
				t.Errorf("%s/%s: public message %q missing from %q", name, mode, tc.msg, errText)
			}
			if tc.sc != "" && mode == "json" && !strings.Contains(all, `"serverCode": "`+tc.sc+`"`) {
				t.Errorf("%s/%s: serverCode missing from %q", name, mode, all)
			}
			// The link appears once at most, on stdout in text mode, never in an error.
			if got := strings.Count(stderr.String(), "SECRETNONCE"); got != 0 {
				t.Errorf("%s/%s: stderr holds the nonce: %q", name, mode, stderr.String())
			}
			if mode == "json" && strings.Contains(all, "SECRETNONCE") {
				t.Errorf("%s/%s: json output holds the nonce: %q", name, mode, all)
			}
		}
	}
}
