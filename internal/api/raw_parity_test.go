package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #576: the raw path (`hadron api`, RawGraphQL) and the curated path (MapError)
// must classify a non-200-with-typed-envelope the same way — by the
// extensions.code, not the HTTP status. Before this, raw returned on the 4xx
// (and 5xx-non-transport) branch before reading the envelope, so a 403/404/503
// carrying a code got the generic status mapping on the raw side only.
func TestRawGraphQLPrefersEnvelopeCodeLikeMapError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"403 + WORKER_TAKEN → Conflict", 403, `{"errors":[{"message":"held","extensions":{"code":"WORKER_TAKEN"}}]}`, exitcode.Conflict},
		{"404 + BAD_USER_INPUT → Usage", 404, `{"errors":[{"message":"bad","extensions":{"code":"BAD_USER_INPUT"}}]}`, exitcode.Usage},
		{"503 + NOT_FOUND → NotFound", 503, `{"errors":[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]}`, exitcode.NotFound},
		// no envelope code: the status still decides, on both paths.
		{"403 plain → Error", 403, `not json`, exitcode.Error},
		{"401 plain → AuthRequired", 401, ``, exitcode.AuthRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := RawGraphQL(context.Background(), srv.URL, "", "query Q { __typename }", nil, srv.Client())
			if err == nil {
				t.Fatal("a non-200 must be an error")
			}
			if got := exitcode.FromError(err); got != tc.want {
				t.Errorf("raw path exit = %d, want %d", got, tc.want)
			}
		})
	}
}
