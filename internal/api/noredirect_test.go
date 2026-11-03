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

// A call marked WithNoRedirect never reaches the redirect target, for both the
// bearer-token and the anonymous client; an unmarked call keeps following.
func TestWithNoRedirectRefusesReplayOnBothClients(t *testing.T) {
	for _, token := range []string{"tok", ""} {
		for _, marked := range []bool{true, false} {
			var dest, src atomic.Int32
			destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dest.Add(1)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				src.Add(1)
				http.Redirect(w, r, destSrv.URL+"/graphql", http.StatusTemporaryRedirect)
			}))
			c, err := NewClient(srcSrv.URL, token, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if marked {
				ctx = WithNoRedirect(ctx)
			}
			var out struct{}
			// The unmarked leg is a QUERY: since #1571 an unmarked mutation is
			// refused too (mutation_redirect_1571_test.go), a query still follows.
			q := "query X { x }"
			if marked {
				q = "mutation X { x }"
			}
			err = c.MakeRequest(ctx, &graphql.Request{OpName: "X", Query: q}, &graphql.Response{Data: &out})
			if marked {
				if !errors.Is(err, ErrRedirectPolicy) || src.Load() != 1 || dest.Load() != 0 {
					t.Errorf("token=%q marked: err=%v src=%d dest=%d", token, err, src.Load(), dest.Load())
				}
			} else if err != nil || dest.Load() != 1 {
				t.Errorf("token=%q unmarked must follow as before: err=%v dest=%d", token, err, dest.Load())
			}
			srcSrv.Close()
			destSrv.Close()
		}
	}
}
