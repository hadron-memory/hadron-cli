package cmd

// hadron-cli#1568 gate ruling (Xylon, #69494): `auth token delete` sends its
// mutation exactly once even when the endpoint answers 307/308. The default
// redirect behavior of every other command was left alone then; hadron-cli#1571
// made refusing a redirect the default for EVERY mutation (see
// mutation_redirect_1571_test.go, which supersedes the old "revoke still
// follows" pin).

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
