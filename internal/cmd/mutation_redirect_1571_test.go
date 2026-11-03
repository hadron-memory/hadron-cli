package cmd

// hadron-cli#1571: every mutation is sent exactly once. A 307/308 from the
// endpoint is refused for ANY mutation (decided once, by operation type, in
// the client's redirect policy), not only for the commands that opted in.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMutationRedirect1571SentOnce(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"revoke (auth.graphql)", []string{"auth", "token", "revoke", "uak_1", "--yes"}},
		{"memory rm (memories.graphql)", []string{"memory", "rm", "01a1a07e793d77a8b6424227b3c3602d", "--yes"}},
	}
	for _, tc := range cases {
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			t.Run(tc.name+"/"+http.StatusText(status), func(t *testing.T) {
				var dest atomic.Int32
				destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					dest.Add(1)
					_, _ = w.Write([]byte(`{"data":{}}`))
				}))
				t.Cleanup(destSrv.Close)
				var src atomic.Int32
				srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						_, _ = w.Write([]byte(`{"capabilities":[]}`))
						return
					}
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), "mutation") {
						_, _ = w.Write([]byte(`{"data":{}}`))
						return
					}
					src.Add(1)
					http.Redirect(w, r, destSrv.URL+"/graphql", status)
				}))
				t.Cleanup(srcSrv.Close)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{}, tc.args...), "--server", srcSrv.URL))
				err := root.Execute()
				if err == nil {
					t.Fatalf("a redirected mutation must fail, printed %q", out.String())
				}
				if src.Load() != 1 || dest.Load() != 0 {
					t.Fatalf("want one POST at the source and none at the target, got %d and %d (err=%v)", src.Load(), dest.Load(), err)
				}
				if !strings.Contains(err.Error(), "redirect refused by policy") {
					t.Errorf("want the policy refusal, got %v", err)
				}
			})
		}
	}
}
