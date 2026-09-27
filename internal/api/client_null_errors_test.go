package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestClientSanitizesNullGraphQLErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		errors string
		want   int
		valid  bool
	}{
		{"200 all null", http.StatusOK, `[null]`, exitcode.Error, false},
		{"400 all null", http.StatusBadRequest, `[null]`, exitcode.Error, false},
		// A 5xx body with no real GraphQL error is classified as a gateway
		// failure by bearerDoer before genqlient decodes it.
		{"500 all null", http.StatusInternalServerError, `[null]`, exitcode.Unavailable, false},
		{"200 mixed", http.StatusOK, `[null,{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, exitcode.NotFound, true},
		{"400 mixed", http.StatusBadRequest, `[null,{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, exitcode.NotFound, true},
		{"500 mixed", http.StatusInternalServerError, `[null,{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, exitcode.NotFound, true},
		{"200 ordinary", http.StatusOK, `[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, exitcode.NotFound, true},
		{"400 ordinary", http.StatusBadRequest, `[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, exitcode.NotFound, true},
		{"500 ordinary", http.StatusInternalServerError, `[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, exitcode.NotFound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"errors":` + tc.errors + `}`))
			}))
			defer srv.Close()

			client, err := NewClient(srv.URL, "", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			var data struct{}
			resp := &graphql.Response{Data: &data}
			err = client.MakeRequest(context.Background(), &graphql.Request{Query: "query Q { __typename }", OpName: "Q"}, resp)
			if err == nil {
				t.Fatal("a response with only null errors must not become success")
			}
			// gqlparser's List.Is/As panic on a nil entry. Exercise those and
			// Error through the real client boundary, before MapError wraps it.
			_ = err.Error()
			_ = errors.Is(err, errors.New("probe"))
			var httpErr *graphql.HTTPError
			if tc.status != http.StatusOK && tc.valid {
				if !errors.As(err, &httpErr) {
					t.Fatalf("non-200 must keep HTTP status: %T: %v", err, err)
				}
			}
			list := resp.Errors
			if httpErr != nil {
				list = httpErr.Response.Errors
			}
			var gqlErr *gqlerror.Error
			if tc.valid {
				if len(list) != 1 || list[0] == nil || !errors.As(list, &gqlErr) || gqlErr.Message != "no such node" {
					t.Fatalf("valid refusal lost: %#v", list)
				}
			} else {
				wantMessage := "malformed GraphQL response"
				if tc.status == http.StatusInternalServerError {
					wantMessage = "gateway"
				}
				if len(list) != 0 || !strings.Contains(err.Error(), wantMessage) {
					t.Fatalf("all-null list should fail truthfully, got list=%#v err=%v", list, err)
				}
			}
			if got := exitcode.FromError(MapError(err)); got != tc.want {
				t.Fatalf("exit = %d, want %d: %v", got, tc.want, err)
			}
		})
	}
}
