package bootpreview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

func TestExactLegacyBoundary(t *testing.T) {
	const old = `{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	for _, tc := range []struct {
		name, body, op string
		status         int
		want           bool
	}{
		{"empty-errors400", `{"errors":[]}`, "GetWorkerBootPromptServe", 400, false},
		{"200", `{"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 200, true},
		{"checked-suggestion", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\". Did you mean \"bootPromptRef\"?","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "GetWorkerBootPromptServe", 200, true},
		{"null200", `{"data":null,"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 200, true},
		{"null400", `{"data":null,"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 400, true},
		{"empty200", `{"data":{},"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 200, false},
		{"empty400", `{"data":{},"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 400, false},
		{"400", `{"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 400, true},
		{"5xx", `{"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 502, false},
		{"partial200", `{"data":{"worker":null},"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 200, false},
		{"partial400", `{"data":{"worker":null},"errors":[` + old + `]}`, "GetWorkerBootPromptServe", 400, false},
		{"mixed", `{"errors":[` + old + `,{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, "GetWorkerBootPromptServe", 200, false},
		{"auth-spoof", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\".","extensions":{"code":"UNAUTHENTICATED"}}]}`, "GetWorkerBootPromptServe", 200, false},
		{"wrongtype", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "GetWorkerBootPromptServe", 200, false},
		{"wrongop", `{"errors":[` + old + `]}`, "Other", 200, false},
		{"path", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\".","path":["worker"],"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "GetWorkerBootPromptServe", 200, false},
		{"ambiguous", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\". Did you mean something else?","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "GetWorkerBootPromptServe", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c := &ValidationRead{Client: graphql.NewClient(s.URL, s.Client())}
			var data any
			resp := graphql.Response{Data: &data}
			err := c.MakeRequest(context.Background(), &graphql.Request{Query: "query X { worker { bootPromptServe { refusal } } }", OpName: tc.op}, &resp)
			if got := c.MissingField(err, "GetWorkerBootPromptServe", "Worker"); got != tc.want {
				t.Fatalf("fallback=%t err=%v", got, err)
			}
			if tc.op == "Other" && c.MissingField(err, "Other", "Worker") {
				t.Fatal("unscoped fallback helper")
			}
			if calls.Load() != 1 {
				t.Fatal(calls.Load())
			}
		})
	}
	// Connection failure is never a schema capability signal.
	c := &ValidationRead{Client: graphql.NewClient("http://127.0.0.1:1", nil)}
	var data json.RawMessage
	err := c.MakeRequest(context.Background(), &graphql.Request{OpName: "GetWorkerBootPromptServe", Query: "query X{x}"}, &graphql.Response{Data: &data})
	if c.MissingField(err, "GetWorkerBootPromptServe", "Worker") {
		t.Fatal("transport fallback")
	}
}
