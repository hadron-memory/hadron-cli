package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// cli#2144 (Julius's P3 on #2140): a command test must never reach a real
// server. testFactory's client only CONNECTS to loopback: the check is on the
// address actually dialled, after resolution (Jibril's P2 on #2146), not on
// the URL's host name. Proxies are off, and a redirect goes through the same
// dialer. Anything else is refused and reported, so a regression (or a red
// replay) that falls through to the default hosted endpoint fails loudly
// instead of sending a fixture key there.
var errNotLoopbackInTest = errors.New("a command test tried to reach a non-loopback address")

// loopbackOnlyClient builds a dedicated transport (never the shared
// DefaultTransport). lookup resolves a host name; nil is the system resolver.
func loopbackOnlyClient(report func(dest string), lookup func(ctx context.Context, host string) ([]net.IPAddr, error)) *http.Client {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	refuse := func(dest string) error {
		report(dest)
		return fmt.Errorf("%w: %s", errNotLoopbackInTest, dest)
	}
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		// The last word: the address the socket is about to connect to.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
				return refuse(address)
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy: nil, // no HTTP(S)_PROXY: a proxy would be the real destination
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, refuse(addr)
			}
			ips := []net.IPAddr{{IP: net.ParseIP(host)}}
			if ips[0].IP == nil {
				if ips, err = lookup(ctx, host); err != nil || len(ips) == 0 {
					return nil, refuse(addr)
				}
			}
			for _, ip := range ips {
				if !ip.IP.IsLoopback() {
					return nil, refuse(net.JoinHostPort(ip.IP.String(), port) + " (" + host + ")")
				}
			}
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{Transport: tr}
}

func TestTestClientOnlyConnectsToLoopback2144(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	report := func(d string) { mu.Lock(); seen = append(seen, d); mu.Unlock() }
	reported := func() int { mu.Lock(); defer mu.Unlock(); return len(seen) }
	// localhost that resolves elsewhere is refused before connecting.
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "localhost" {
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
		}
		return net.DefaultResolver.LookupIPAddr(context.Background(), host)
	}
	c := loopbackOnlyClient(report, lookup)
	for _, u := range []string{
		"http://localhost:9/graphql", // resolves (here) to a non-loopback address
		"http://192.0.2.1/graphql",   // an external IPv4 literal
		"http://[2001:db8::1]/graphql",
	} {
		before := reported()
		if _, err := c.Get(u); !errors.Is(err, errNotLoopbackInTest) {
			t.Fatalf("%s was not refused: %v", u, err)
		}
		if reported() != before+1 {
			t.Fatalf("%s was not reported", u)
		}
	}
	ok := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }
	// An IPv4 loopback server is reached.
	srv := httptest.NewServer(http.HandlerFunc(ok))
	defer srv.Close()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("a loopback server was refused: %v", err)
	}
	_ = resp.Body.Close()
	// An IPv6 loopback server is reached, where the host has ::1.
	if l, lerr := net.Listen("tcp", "[::1]:0"); lerr == nil {
		s6 := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(ok)}}
		s6.Start()
		defer s6.Close()
		resp, err := c.Get(s6.URL)
		if err != nil {
			t.Fatalf("an IPv6 loopback server was refused: %v", err)
		}
		_ = resp.Body.Close()
	}
	// A name resolving to ::1 first and 127.0.0.1 second reaches an
	// IPv4-only server: every loopback address is tried, not just the first.
	both := loopbackOnlyClient(report, func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	})
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	before4 := reported()
	resp, err = both.Get("http://fixture.test:" + port + "/")
	if err != nil {
		t.Fatalf("::1 first broke an IPv4-only loopback server: %v", err)
	}
	_ = resp.Body.Close()
	if reported() != before4 {
		t.Fatal("a loopback fallback was reported")
	}
	// A loopback server redirecting outside: the redirect is refused and
	// reported.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.0.2.1/graphql", http.StatusFound)
	}))
	defer redir.Close()
	before := reported()
	if _, err := c.Get(redir.URL); !errors.Is(err, errNotLoopbackInTest) {
		t.Fatalf("a redirect outside was followed: %v", err)
	}
	if reported() != before+1 || !strings.Contains(seen[len(seen)-1], "192.0.2.1") {
		t.Fatalf("the redirect was not reported: %v", seen)
	}
}
