package auth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const successHTML = `<!doctype html><meta charset="utf-8"><title>hadron</title>
<body style="font-family:system-ui;display:grid;place-items:center;height:90vh">
<div style="text-align:center"><h1>Signed in</h1>
<p>You can close this tab and return to the terminal.</p></div>`

const errorHTML = `<!doctype html><meta charset="utf-8"><title>hadron</title>
<body style="font-family:system-ui;display:grid;place-items:center;height:90vh">
<div style="text-align:center"><h1>Sign-in failed</h1>
<p>%s</p><p>Return to the terminal for details.</p></div>`

// errInvalidScope marks an invalid_scope error redirect (RFC 6749 §4.1.2.1):
// the server refused the requested OAuth scope. It is a server-compatibility
// failure, not the user declining, so it is not reported as Cancelled.
var errInvalidScope = errors.New("invalid_scope")

type callbackResult struct {
	code string
	err  error
}

// loopbackServer receives the one-shot OAuth redirect on
// 127.0.0.1:<random port>.
type loopbackServer struct {
	listener net.Listener
	server   *http.Server
	results  chan callbackResult
	state    string
}

func startLoopback(state string) (*loopbackServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting loopback listener: %w", err)
	}
	ls := &loopbackServer{
		listener: listener,
		results:  make(chan callbackResult, 1),
		state:    state,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", ls.handle)
	ls.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
	}
	go func() { _ = ls.server.Serve(listener) }()
	return ls, nil
}

// RedirectURI returns the exact redirect URI bound to this run.
func (ls *loopbackServer) RedirectURI() string {
	return fmt.Sprintf("http://%s/callback", ls.listener.Addr().String())
}

func (ls *loopbackServer) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// State first, for error redirects too (RFC 6749 §4.1.2.1 echoes it):
	// otherwise any local process could post a forged error to this port and
	// have the CLI report a denial or a server scope refusal that never
	// happened. hadron-server includes state on every error redirect.
	switch {
	case q.Get("state") != ls.state:
		fmt.Fprintf(w, errorHTML, "state mismatch")
		ls.deliver(callbackResult{err: exitcode.Newf(exitcode.Error, "OAuth state mismatch — possible interception, aborting")})
	case q.Get("error") != "":
		desc := q.Get("error_description")
		if desc == "" {
			desc = q.Get("error")
		}
		fmt.Fprintf(w, errorHTML, html.EscapeString(desc))
		if q.Get("error") == "invalid_scope" {
			ls.deliver(callbackResult{err: exitcode.New(exitcode.Error, fmt.Errorf("%w: %s", errInvalidScope, desc))})
			return
		}
		ls.deliver(callbackResult{err: exitcode.Newf(exitcode.Cancelled, "authorization denied: %s", desc)})
	case q.Get("code") == "":
		fmt.Fprintf(w, errorHTML, "missing authorization code")
		ls.deliver(callbackResult{err: exitcode.Newf(exitcode.Error, "OAuth redirect missing authorization code")})
	default:
		fmt.Fprint(w, successHTML)
		ls.deliver(callbackResult{code: q.Get("code")})
	}
}

func (ls *loopbackServer) deliver(res callbackResult) {
	select {
	case ls.results <- res:
	default: // a result was already delivered; ignore repeats
	}
}

// Wait blocks until the redirect arrives or ctx expires. bound is the limit
// the caller set, named in the timeout message. Either way nothing has been
// stored yet: the token is written only after a successful exchange.
func (ls *loopbackServer) Wait(ctx context.Context, bound time.Duration) (string, error) {
	select {
	case res := <-ls.results:
		return res.code, res.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", exitcode.Newf(exitcode.Cancelled, "the browser sign-in was cancelled; nothing was stored")
		}
		return "", exitcode.Newf(exitcode.Cancelled, "the browser sign-in did not finish within %s; nothing was stored. Rerun `hadron auth login`", formatBound(bound))
	}
}

// formatBound renders a wait limit for a person: "15 minutes" rather than
// "15m0s".
func formatBound(d time.Duration) string {
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	case d >= time.Second:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}

func (ls *loopbackServer) Close() error {
	return ls.server.Close()
}
