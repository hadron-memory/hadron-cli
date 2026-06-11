package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"

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
	ls.server = &http.Server{Handler: mux}
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
	switch {
	case q.Get("error") != "":
		desc := q.Get("error_description")
		if desc == "" {
			desc = q.Get("error")
		}
		fmt.Fprintf(w, errorHTML, desc)
		ls.deliver(callbackResult{err: exitcode.Newf(exitcode.Cancelled, "authorization denied: %s", desc)})
	case q.Get("state") != ls.state:
		fmt.Fprintf(w, errorHTML, "state mismatch")
		ls.deliver(callbackResult{err: exitcode.Newf(exitcode.Error, "OAuth state mismatch — possible interception, aborting")})
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

// Wait blocks until the redirect arrives or ctx expires.
func (ls *loopbackServer) Wait(ctx context.Context) (string, error) {
	select {
	case res := <-ls.results:
		return res.code, res.err
	case <-ctx.Done():
		return "", exitcode.Newf(exitcode.Cancelled, "timed out waiting for browser sign-in")
	}
}

func (ls *loopbackServer) Close() error {
	return ls.server.Close()
}
