package cmd

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// cli#2144 (Julius's P3 on #2140): a command test must never reach a real
// server. testFactory's client only dials loopback; anything else is refused
// and reported, so a regression (or a red replay) that falls through to the
// default hosted endpoint fails loudly instead of sending a fixture key there.
type loopbackOnlyTransport struct {
	next   http.RoundTripper
	report func(host string)
}

var errNotLoopbackInTest = errors.New("a command test tried to reach a non-loopback server")

func (l loopbackOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		l.report(r.URL.Host)
		return nil, fmt.Errorf("%w: %s", errNotLoopbackInTest, r.URL.Host)
	}
	return l.next.RoundTrip(r)
}

func loopbackOnlyClient(report func(host string)) *http.Client {
	return &http.Client{Transport: loopbackOnlyTransport{next: http.DefaultTransport, report: report}}
}

func TestTestClientNeverLeavesLoopback2144(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	c := loopbackOnlyClient(func(h string) { mu.Lock(); seen = append(seen, h); mu.Unlock() })
	if _, err := c.Get("https://hadronmemory.com/graphql"); !errors.Is(err, errNotLoopbackInTest) {
		t.Fatalf("a hosted URL was not refused: %v", err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "hadronmemory.com") {
		t.Fatalf("not reported: %v", seen)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("a loopback server was refused: %v", err)
	}
	_ = resp.Body.Close()
	if len(seen) != 1 {
		t.Fatalf("loopback reported: %v", seen)
	}
}
