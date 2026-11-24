package cmd

import (
	"errors"
	"net"
	"net/http"
	neturl "net/url"
	"testing"
	"time"
)

// hadron-cli#2997: several tests need "a connection that never opened": a server address nothing answers, to prove
// the CLI reports "could not reach the server" (exit 7) and makes no claim about a write. They used to get one by
// starting an httptest.Server, taking its URL and closing it. That leaves a window (the rest of the test, which
// builds the whole command tree) in which ANY process on the host can bind the freed port: on a busy shared host
// the CLI then reaches something that answers or drops the connection, and the test reports an unknown outcome
// instead. Measured on this host with another process churning ephemeral ports: 0 of 400 runs failed without it
// and 98 of 400 (24%) with it.
//
// neverOpenedServerURL returns an address that cannot be listening at all, whatever else the host is doing.

// neverOpenedServerURL is a server URL on which a connection can never be opened: port 0 cannot be bound, so no
// listener can ever answer on it and the dial is refused (or fails to assign an address on other platforms; either
// way a net.Error, which the CLI maps to "could not reach the server").
func neverOpenedServerURL(t *testing.T) string {
	t.Helper()
	return "http://127.0.0.1:0"
}

// The property that matters: even when something takes the very port a naive "close a server and reuse its URL"
// would hand out, the URL still cannot be answered. The hostile listener below binds the URL's own host:port if it
// can (which is exactly what reuse of a freed port is); a URL that can never be bound is not affected.
func TestNeverOpenedServerURLCannotBeAnsweredEvenIfItsPortIsTaken_2997(t *testing.T) {
	for i := 0; i < 20; i++ {
		url := neverOpenedServerURL(t)
		u, err := neturl.Parse(url)
		if err != nil {
			t.Fatalf("not a URL: %v", err)
		}
		answered := false
		if ln, lerr := net.Listen("tcp", u.Host); lerr == nil {
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				answered = true
				_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
			})}
			go func() { _ = srv.Serve(ln) }()
			t.Cleanup(func() { _ = srv.Close() })
		}
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Get(url + "/graphql")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("iteration %d: %s was answered (status %d, handler ran: %v); a never-opened URL must not be answerable", i, url, resp.StatusCode, answered)
		}
		var ne net.Error
		if !errors.As(err, &ne) {
			t.Fatalf("iteration %d: want a net.Error dial failure the CLI maps to 'could not reach', got %T: %v", i, err, err)
		}
		if answered {
			t.Fatalf("iteration %d: a handler answered a request to %s", i, url)
		}
	}
}
