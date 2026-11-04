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
