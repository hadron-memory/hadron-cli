package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// statusGraphQLServer answers each GraphQL operation with its own HTTP status
// and body, which the plain captureGraphQL cannot (it always answers 200). An
// operation with no entry fails the test and is answered 200 with a GraphQL
// error. It also serves an empty /capabilities. Introduced for cli#1495 and
// shared by the cli#1495, #1845 and authored-write tests.
func statusGraphQLServer(t *testing.T, resp map[string]statusGraphQLResponse, seen func(op string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen(req.OperationName)
		got, ok := resp[req.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", req.OperationName)
			got.status, got.body = 200, `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(got.status)
		_, _ = w.Write([]byte(got.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// statusGraphQLResponse is one operation's canned answer: the HTTP status and
// the raw body. Callers build it positionally, {status, body}.
type statusGraphQLResponse struct {
	status int
	body   string
}
