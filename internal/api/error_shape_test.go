package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestErrorShapeObservation1655(t *testing.T) {
	const valid = `{"message":"no such node","extensions":{"code":"NOT_FOUND"}}`
	for _, status := range []int{200, 400} {
		for _, entries := range []string{valid, `null,` + valid, valid + `,null`, `null`} {
			t.Run(fmt.Sprintf("%d/%s", status, entries), func(t *testing.T) {
				var calls atomic.Int32
				var clean atomic.Bool
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					reply := entries
					if clean.Load() {
						reply = valid
					}
					fmt.Fprint(w, `{"errors":[`+reply+`]}`)
				}))
				defer srv.Close()
				client, err := NewClient(srv.URL, "", srv.Client())
				if err != nil {
					t.Fatal(err)
				}
				invoke := func(ctx context.Context) error {
					var data any
					return client.MakeRequest(ctx, &graphql.Request{OpName: "UnrelatedRead", Query: "query UnrelatedRead { __typename }"}, &graphql.Response{Data: &data})
				}
				baseline := invoke(context.Background())
				ctx, shape := WithGraphQLErrorShape(context.Background())
				observed := invoke(ctx)
				if baseline == nil || observed == nil || baseline.Error() != observed.Error() || exitcode.FromError(MapError(baseline)) != exitcode.FromError(MapError(observed)) {
					t.Fatalf("mapping changed: baseline%v observed%v", baseline, observed)
				}
				if shape.HadNullEntry() != (entries != valid) {
					t.Fatalf("null observation=%v", shape.HadNullEntry())
				}
				// Another observer/context does not receive the first one's state.
				clean.Store(true)
				freshCtx, fresh := WithGraphQLErrorShape(context.Background())
				cleanErr := invoke(freshCtx)
				if cleanErr == nil || exitcode.FromError(MapError(cleanErr)) != exitcode.NotFound {
					t.Fatalf("clean request mapping: %v", cleanErr)
				}
				if shape.HadNullEntry() != (entries != valid) {
					t.Fatal("second request changed first observer")
				}
				if calls.Load() != 3 {
					t.Fatalf("requests=%d want baseline+A+B", calls.Load())
				}
				if fresh.HadNullEntry() {
					t.Fatal("observer leaked between contexts")
				}
			})
		}
	}
}
