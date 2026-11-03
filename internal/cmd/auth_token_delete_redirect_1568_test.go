package cmd

// hadron-cli#1568 gate ruling (Xylon, #69494): `auth token delete` sends its
// mutation exactly once even when the endpoint answers 307/308. The default
// redirect behavior of every other command is deliberately NOT changed (revoke
// and the rest still follow), so the seam is operation-scoped.

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAuthTokenDelete1568SendsOnceUnderRedirect(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var dest atomic.Int32
			destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dest.Add(1)
				_, _ = w.Write([]byte(deletion1568("", "")))
			}))
			t.Cleanup(destSrv.Close)
			var src atomic.Int32
			srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"capabilities":[]}`))
					return
				}
				src.Add(1)
				http.Redirect(w, r, destSrv.URL+"/graphql", status)
			}))
			t.Cleanup(srcSrv.Close)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--yes", "--server", srcSrv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatalf("a redirected delete must fail, printed %q", out.String())
			}
			if src.Load() != 1 || dest.Load() != 0 {
				t.Fatalf("want one POST at the source and none at the redirect target, got %d and %d", src.Load(), dest.Load())
			}
			if !strings.Contains(err.Error(), "redirect") {
				t.Errorf("the failure should say a redirect was refused, got %v", err)
			}
		})
	}
}

// The seam is scoped to the delete: revoke keeps following a redirect, as before.
func TestAuthTokenRevoke1568StillFollowsRedirect(t *testing.T) {
	const revoked = `{"data":{"revokeUserApiKey":{"id":"uak_1","label":null,"keyPreview":"p","issuedVia":null,"createdAt":"2026-06-19T00:00:00Z","lastUsedAt":null,"revokedAt":"2026-06-19T02:00:00Z","hiddenAt":null}}}`
	var dest atomic.Int32
	destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dest.Add(1)
		_, _ = w.Write([]byte(revoked))
	}))
	t.Cleanup(destSrv.Close)
	srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		http.Redirect(w, r, destSrv.URL+"/graphql", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srcSrv.Close)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "revoke", "uak_1", "--yes", "--server", srcSrv.URL})
	if err := root.Execute(); err != nil || dest.Load() != 1 {
		t.Fatalf("revoke should keep its default redirect behavior: err=%v dest=%d", err, dest.Load())
	}
}

// A connection dropped before any answer is surfaced after one send.
func TestAuthTokenDelete1568DroppedConnectionSentOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		calls.Add(1)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_ = bufio.NewReader(buf)
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--yes", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("want an error on a dropped connection")
	}
	if calls.Load() != 1 {
		t.Fatalf("a delete is never retried; sent %d times", calls.Load())
	}
}
