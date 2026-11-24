package cmd

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"testing"
	"time"
)

// hadron-cli#2997: several tests need "a connection that never opened": a server address nothing answers, to prove
// the CLI reports "could not reach the server" (exit 7) and makes no claim about a write. They used to get one by
// starting an httptest.Server, taking its URL and closing it. That leaves a window (the rest of the test, which
// builds the whole command tree) in which ANY process on the host can bind the freed port; on a busy shared host the
// CLI then reached something that answered or dropped the connection and the test reported an unknown outcome.
// Measured on a busy host with another process churning ephemeral ports, the old tests failed 98 of 400 runs; with
// no such process, 0 of 400. The fixed tests: 0 of 400 under the same churn.

// neverOpenedServerURL is a server URL on which a connection can never be opened: port 0 cannot be bound, so no
// listener can ever answer on it and the dial always fails. What it fails with is platform-dependent (ECONNREFUSED
// on Linux, EADDRNOTAVAIL on macOS and Windows); every such failure is the CLI's "could not reach the server"
// (exit 7) and a never-sent request (internal/api: TestEveryDialFailureIsUnreachableAndNotProcessed_2997).
const neverOpenedServerURL = "http://127.0.0.1:0"

// The property, stated directly: the URL's port is 0, a listener asked to bind that very address can only land on
// a DIFFERENT port (so nothing can ever answer at the URL), and dialing it fails with a dial error and no response.
func TestNeverOpenedServerURLIsAPortNothingCanBind_2997(t *testing.T) {
	u, err := neturl.Parse(neverOpenedServerURL)
	if err != nil {
		t.Fatalf("not a URL: %v", err)
	}
	if u.Port() != "0" {
		t.Fatalf("the URL's port is %q: only port 0 is a port nothing can bind; any other port can be taken by another process", u.Port())
	}
	// Best effort: binding "port 0" asks the OS for ANY free port, which can fail on a host short of ephemeral ports
	// (the same pressure that caused the flake), so a failed bind says nothing about the URL and is not a failure.
	if ln, lerr := net.Listen("tcp", u.Host); lerr == nil {
		defer func() { _ = ln.Close() }()
		if got := ln.Addr().(*net.TCPAddr).Port; got == 0 {
			t.Fatal("a listener bound port 0 itself: the URL could then be answered")
		}
	} else {
		t.Logf("no ephemeral port was free to bind (%v); the port-0 and dial checks still apply", lerr)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(neverOpenedServerURL + "/graphql")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("%s was answered (status %d)", neverOpenedServerURL, resp.StatusCode)
	}
	var ne net.Error
	if !errors.As(err, &ne) {
		t.Fatalf("want a net.Error dial failure, got %T: %v", err, err)
	}
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		t.Fatalf("want a dial failure (the connection never opened), got %v", err)
	}
}

// Why the helper exists, deterministically: a closed server's port can be taken straight over, and whatever took it
// then answers. This is what another process on a busy host did to the old tests; here the test does it itself, so
// the hazard is shown, not assumed. (If something else grabbed the port first, the hazard has shown itself anyway.)
func TestAFreedPortCanBeTakenOverAndAnswered_2997(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("another process took the freed port %s before this test could: the hazard in action (%v)", addr, err)
	}
	taker := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
	})}
	go func() { _ = taker.Serve(ln) }()
	defer func() { _ = taker.Close() }()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://" + addr + "/graphql")
	if err != nil {
		t.Fatalf("a retaken port should answer: %v", err)
	}
	_ = resp.Body.Close()
}
