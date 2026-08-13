package api

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The whole point of #394: a server-formed refusal and a lost request must not
// land on the same exit code, because only one of them is safe to retry blind.
func TestMapErrorSeparatesTransportFromRefusal(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{
			"gateway 502 with a non-JSON body is transport",
			&graphql.HTTPError{StatusCode: 502},
			exitcode.Unavailable,
		},
		{"503 from an edge", &graphql.HTTPError{StatusCode: 503}, exitcode.Unavailable},
		{"504 from an edge", &graphql.HTTPError{StatusCode: 504}, exitcode.Unavailable},
		{
			// Apollo answering 500 with a real errors[] IS an opinion.
			"500 carrying GraphQL errors is a refusal, not transport",
			&graphql.HTTPError{
				StatusCode: 500,
				Response:   graphql.Response{Errors: gqlerror.List{{Message: "boom"}}},
			},
			exitcode.Error,
		},
		{"connection refused", &url.Error{Op: "Post", Err: errors.New("connection refused")}, exitcode.Unavailable},
		{"timeout", context.DeadlineExceeded, exitcode.Unavailable},
		{"net timeout", &net.OpError{Op: "dial", Err: timeoutErr{}}, exitcode.Unavailable},
		// Refusals keep their existing codes — this must not reclassify them.
		{"401 stays auth", &graphql.HTTPError{StatusCode: 401}, exitcode.AuthRequired},
		{"404 stays not-found", &graphql.HTTPError{StatusCode: 404}, exitcode.NotFound},
		{
			"a typed GraphQL refusal is untouched",
			gqlerror.List{{Message: "nope", Extensions: map[string]any{"code": "BAD_USER_INPUT"}}},
			exitcode.Usage,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exitcode.FromError(MapError(tc.err))
			if got != tc.want {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tc.want, MapError(tc.err))
			}
		})
	}
}

// A transport failure must say the write MAY have landed — the caller who
// assumes "error ⇒ nothing happened" is the one who double-applies.
func TestTransportErrorNamesTheIdempotencyRisk(t *testing.T) {
	f, ok := transportStatus(502, []byte("error code: 502"))
	if !ok {
		t.Fatal("502 with a non-JSON body must classify as transport")
	}
	mut := transportError(f, true).Error()
	if !strings.Contains(mut, "may or may not have been applied") {
		t.Errorf("mutation message must warn about idempotency: %s", mut)
	}
	if !strings.Contains(mut, "restarting") {
		t.Errorf("message should name the likely cause: %s", mut)
	}
	read := transportError(f, false).Error()
	if strings.Contains(read, "may or may not have been applied") {
		t.Errorf("a read must not carry the write caveat: %s", read)
	}
}

// A 5xx whose body IS a GraphQL envelope is the API's opinion, so `hadron api`
// must not relabel it as a lost request.
func TestTransportStatusRespectsAGraphQLEnvelope(t *testing.T) {
	if _, ok := transportStatus(500, []byte(`{"errors":[{"message":"boom"}]}`)); ok {
		t.Error("a 5xx carrying errors[] is a refusal, not transport")
	}
	if _, ok := transportStatus(500, []byte(`{"errors":[]}`)); !ok {
		t.Error("an empty errors[] is not an opinion — treat as transport")
	}
	if _, ok := transportStatus(400, []byte("nonsense")); ok {
		t.Error("4xx is never transport — the server formed an opinion")
	}
}

func TestDocumentHasMutation(t *testing.T) {
	cases := []struct {
		doc  string
		want bool
	}{
		{`mutation($r: ID!) { updateAgent(ref: $r) { id } }`, true},
		{`  mutation Foo { x }`, true},
		{"query { a }\n\nmutation { b }", true},
		{`{ agent(ref: "x") { id } }`, false},
		{`query($r: ID!) { agent(ref: $r) { urn } }`, false},
		// The word inside a STRING is not an operation — a query whose
		// argument merely mentions mutations must not trigger the warning.
		{`query { search(q: "mutation") { id } }`, false},
		// ...but a comment mentioning it doesn't either.
		{"# mutation, someday\nquery { a }", false},
		// Name-boundary: a field called `mutations` is not the keyword.
		{`query { mutations { id } }`, false},
	}
	for _, tc := range cases {
		if got := DocumentHasMutation(tc.doc); got != tc.want {
			t.Errorf("DocumentHasMutation(%q) = %v, want %v", tc.doc, got, tc.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
