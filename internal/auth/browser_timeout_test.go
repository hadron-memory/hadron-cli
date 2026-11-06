package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#875: first passkey enrolment on a #1897 server needs two inbox round
// trips plus a registration ceremony inside one `auth login`, which 5 minutes
// does not reliably hold. 15 minutes is the CLI's own engineering upper bound,
// not a contract value; it is lowered to the provider's pending-flow lifetime
// if that turns out shorter (Xylon's gate, 6260462573).
func TestLoginTimeoutIsFifteenMinuteUpperBound(t *testing.T) {
	if loginTimeout != 15*time.Minute {
		t.Fatalf("loginTimeout = %v, want 15m (cli#875)", loginTimeout)
	}
}

// A browser step that never comes back (an abandoned passkey page, a server-
// page failure that never redirects) ends Cancelled, and the message says
// nothing was stored and how to retry. The deadline is the test's own context,
// so this takes about a second, not the real bound. The deadline is generous
// against discovery and registration on a local fake (#941 BLIND P3, Jane), and
// the test requires the browser step was reached, so a slow runner that expires
// during setup fails with a plain message instead of a misleading one.
func TestBrowserLoginTimeoutSaysNothingStored(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	opened := false
	tok, err := BrowserStrategy{}.Login(ctx, LoginOptions{
		ServerURL:  as.server.URL,
		IO:         io,
		HTTPClient: as.server.Client(),
		OpenBrowser: func(string) error { // the user never finishes
			opened = true
			return nil
		},
	})
	if !opened {
		t.Fatalf("the deadline fired before the browser step was reached (slow setup?): %v", err)
	}
	if tok != nil {
		t.Fatalf("a timed-out login must return no token, got %+v", tok)
	}
	if got := exitcode.FromError(err); got != exitcode.Cancelled {
		t.Fatalf("exit = %d, want Cancelled %d (err: %v)", got, exitcode.Cancelled, err)
	}
	msg := err.Error()
	for _, want := range []string{"did not finish within", "nothing was stored", "finish that in the browser first", "hadron auth login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message missing %q: %q", want, msg)
		}
	}
	// cli#1624 (#941 §10 P3-2 mutant 3): the caller's earlier deadline is the
	// limit that fired, so the message names it, not the 15-minute cap.
	if strings.Contains(msg, "15 minutes") || !boundNear1s.MatchString(msg) {
		t.Errorf("the message must name the caller's ~1s bound, not the internal cap: %q", msg)
	}
}

var boundNear1s = regexp.MustCompile(`did not finish within (1s|9\d\dms);`)

// shortLoginTimeout sets the internal cap for one test (never parallel).
func shortLoginTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := loginTimeout
	loginTimeout = d
	t.Cleanup(func() { loginTimeout = old })
}

// loginWithGuard runs Login on a goroutine and fails the test if it has not
// returned within guard: an unbounded wait shows up as a failure, not a hang.
func loginWithGuard(t *testing.T, ctx context.Context, opts LoginOptions, guard time.Duration) (*Token, error) {
	t.Helper()
	type result struct {
		tok *Token
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := BrowserStrategy{}.Login(ctx, opts)
		done <- result{tok, err}
	}()
	select {
	case r := <-done:
		return r.tok, r.err
	case <-time.After(guard):
		t.Fatalf("Login did not return within %s: the internal deadline is missing", guard)
		return nil, nil
	}
}

// cli#1624 (#941 §10 P3-2 mutant 2): the CLI's own deadline ends the wait when
// the caller set none. Nobody cancels the caller's context here, so only the
// internal deadline can end it, and it reports a timeout naming that bound,
// not an interrupt.
func TestBrowserLoginInternalDeadlineFiresWithoutCallerDeadline(t *testing.T) {
	shortLoginTimeout(t, 300*time.Millisecond)
	as := newFakeAS(t)
	io, _, _ := output.Test()
	opened := false
	tok, err := loginWithGuard(t, context.Background(), LoginOptions{
		ServerURL:  as.server.URL,
		IO:         io,
		HTTPClient: as.server.Client(),
		OpenBrowser: func(string) error {
			opened = true
			return nil
		},
	}, 10*time.Second)
	if !opened {
		t.Fatalf("the browser step was never reached: %v", err)
	}
	if tok != nil {
		t.Fatalf("a timed-out login must return no token, got %+v", tok)
	}
	if got := exitcode.FromError(err); got != exitcode.Cancelled {
		t.Fatalf("exit = %d, want Cancelled (err: %v)", got, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "did not finish within 300ms") || strings.Contains(msg, "was cancelled") {
		t.Errorf("want a timeout naming the 300ms internal bound, got %q", msg)
	}
}

// The internal deadline covers discovery too, not only the browser wait: a
// server that never answers its metadata request cannot hold the login open.
func TestBrowserLoginInternalDeadlineCoversDiscovery(t *testing.T) {
	shortLoginTimeout(t, 300*time.Millisecond)
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); hang.Close() })
	io, _, _ := output.Test()
	tok, err := loginWithGuard(t, context.Background(), LoginOptions{
		ServerURL:   hang.URL,
		IO:          io,
		HTTPClient:  &http.Client{}, // no client timeout: only the login's deadline can end it
		OpenBrowser: func(string) error { t.Error("the browser must not open before discovery"); return nil },
	}, 10*time.Second)
	if tok != nil || err == nil {
		t.Fatalf("a login whose discovery never answers must fail, got tok=%v err=%v", tok, err)
	}
}

// An interrupt (the caller's context cancelled, not timed out) must not be
// reported as a timeout naming a limit that never fired. The interrupt comes
// from the browser step itself, so it can never land during discovery
// (#941 BLIND P3, Jane).
func TestBrowserLoginInterruptIsNotReportedAsTimeout(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := BrowserStrategy{}.Login(ctx, LoginOptions{
		ServerURL:  as.server.URL,
		IO:         io,
		HTTPClient: as.server.Client(),
		OpenBrowser: func(string) error {
			cancel() // the user interrupts while the browser page is open
			return nil
		},
	})
	if got := exitcode.FromError(err); got != exitcode.Cancelled {
		t.Fatalf("exit = %d, want Cancelled %d (err: %v)", got, exitcode.Cancelled, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "was cancelled; nothing was stored") || strings.Contains(msg, "did not finish within") {
		t.Errorf("interrupt message = %q", msg)
	}
}
