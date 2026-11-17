package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// guardRecorder1759 stands in for the test loginWithGuard reports to: it keeps
// the Fatalf message instead of ending the goroutine, so the test can look at
// what the guard left behind.
type guardRecorder1759 struct {
	testing.TB
	mu    sync.Mutex
	fatal []string
}

func (g *guardRecorder1759) Helper() {}

func (g *guardRecorder1759) Fatalf(format string, args ...any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fatal = append(g.fatal, fmt.Sprintf(format, args...))
}

// hadron-cli#1759 row 1 (George BLIND P3 on #1632): when the guard fires — the
// killed internal-deadline mutant, simulated here by an hour-long cap — the
// guard fails the test AND stops the login it abandons, so no goroutine keeps a
// request open after the test. Login's own deadline and the caller-cancellation
// path are untouched: the guard only cancels once it has already failed.
func TestLoginGuard1759StopsTheLoginItAbandons(t *testing.T) {
	shortLoginTimeout(t, time.Hour) // nothing inside Login ends this wait
	stopped := make(chan struct{})
	var once sync.Once
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			once.Do(func() { close(stopped) })
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); hang.Close() })

	io, _, _ := output.Test()
	g := &guardRecorder1759{TB: t}
	_, _ = loginWithGuard(g, context.Background(), LoginOptions{
		ServerURL:   hang.URL,
		IO:          io,
		HTTPClient:  &http.Client{},
		OpenBrowser: func(string) error { return nil },
	}, 200*time.Millisecond)

	g.mu.Lock()
	fatal := append([]string{}, g.fatal...)
	g.mu.Unlock()
	if len(fatal) != 1 || !strings.Contains(fatal[0], "did not return within 200ms") {
		t.Fatalf("the guard must fail the test once, naming its bound: %q", fatal)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the guard returned but left the login running: its request was never cancelled")
	}
}
