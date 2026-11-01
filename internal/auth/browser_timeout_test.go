package auth

import (
	"context"
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
// so this takes milliseconds, not the real bound.
func TestBrowserLoginTimeoutSaysNothingStored(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	tok, err := BrowserStrategy{}.Login(ctx, LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: func(string) error { return nil }, // the user never finishes
	})
	if tok != nil {
		t.Fatalf("a timed-out login must return no token, got %+v", tok)
	}
	if got := exitcode.FromError(err); got != exitcode.Cancelled {
		t.Fatalf("exit = %d, want Cancelled %d (err: %v)", got, exitcode.Cancelled, err)
	}
	msg := err.Error()
	for _, want := range []string{"did not finish within", "nothing was stored", "hadron auth login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message missing %q: %q", want, msg)
		}
	}
}

// An interrupt (the caller's context cancelled, not timed out) must not be
// reported as a timeout naming a limit that never fired.
func TestBrowserLoginInterruptIsNotReportedAsTimeout(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := BrowserStrategy{}.Login(ctx, LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: func(string) error { return nil },
	})
	if got := exitcode.FromError(err); got != exitcode.Cancelled {
		t.Fatalf("exit = %d, want Cancelled %d (err: %v)", got, exitcode.Cancelled, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "was cancelled; nothing was stored") || strings.Contains(msg, "did not finish within") {
		t.Errorf("interrupt message = %q", msg)
	}
}
