package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// #1571: the policy is by OPERATION TYPE, on both clients, for 307 and 308: a
// mutation is never replayed, a query still follows.
func TestMutationRedirectRefusedByOperationType(t *testing.T) {
	for _, token := range []string{"tok", ""} {
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			for _, q := range []struct {
				name, query string
				refuse      bool
			}{
				{"mutation", "mutation X { x }", true},
				{"mutation after comment", "# c\n  mutation X { x }", true},
				{"query", "query X { x }", false},
				{"anonymous query", "{ x }", false},
			} {
				var dest, src atomic.Int32
				destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					dest.Add(1)
					_, _ = w.Write([]byte(`{"data":{}}`))
				}))
				srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					src.Add(1)
					http.Redirect(w, r, destSrv.URL+"/graphql", status)
				}))
				c, err := NewClient(srcSrv.URL, token, nil)
				if err != nil {
					t.Fatal(err)
				}
				var out struct{}
				err = c.MakeRequest(context.Background(), &graphql.Request{OpName: "X", Query: q.query}, &graphql.Response{Data: &out})
				if q.refuse {
					if !errors.Is(err, ErrRedirectPolicy) || src.Load() != 1 || dest.Load() != 0 {
						t.Errorf("token=%q %d %s: err=%v src=%d dest=%d", token, status, q.name, err, src.Load(), dest.Load())
					}
				} else if err != nil || dest.Load() != 1 {
					t.Errorf("token=%q %d %s must follow: err=%v dest=%d", token, status, q.name, err, dest.Load())
				}
				srcSrv.Close()
				destSrv.Close()
			}
		}
	}
}
