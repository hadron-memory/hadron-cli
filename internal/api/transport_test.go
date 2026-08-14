package api

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// PR #415 review: each of these was a way the classifier got it wrong.
func TestClassifyTransportReviewCases(t *testing.T) {
	// A truncated response body IS a lost answer — headers arrived, so the
	// request certainly reached the server and a mutation may have committed.
	if _, ok := classifyTransport(fmt.Errorf("decoding response: %w", io.ErrUnexpectedEOF)); !ok {
		t.Error("a truncated body must classify as transport (exit 7), not a refusal")
	}
	if code := exitcode.FromError(MapError(io.ErrUnexpectedEOF)); code != exitcode.Unavailable {
		t.Errorf("truncated body exit = %d, want Unavailable", code)
	}
	// OUR OWN redirect refusal is not transport, though net/http wraps it in
	// *url.Error (which satisfies net.Error). The server answered.
	policy := &url.Error{Op: "Get", Err: fmt.Errorf("%w: cleartext", ErrRedirectPolicy)}
	if _, ok := classifyTransport(policy); ok {
		t.Error("a redirect POLICY refusal must not be reported as retryable")
	}
	if code := exitcode.FromError(MapError(policy)); code != exitcode.Error {
		t.Errorf("redirect policy exit = %d, want Error (retrying cannot help)", code)
	}
}

// A gateway that emits JSON must not be able to suppress exit 7 by shipping an
// `errors` array with no real GraphQL error object in it.
func TestEnvelopeRequiresARealErrorObject(t *testing.T) {
	for _, body := range []string{
		`{"errors":[null]}`,
		`{"errors":["upstream unavailable"]}`,
		`{"errors":[{}]}`,
		`{"errors":[]}`,
	} {
		if _, ok := transportStatus(502, []byte(body)); !ok {
			t.Errorf("%s is not the API's opinion — must stay transport", body)
		}
	}
	if _, ok := transportStatus(502, []byte(`{"errors":[{"message":"boom"}]}`)); ok {
		t.Error("a real GraphQL error object IS an opinion")
	}
}

// Block strings may hold unescaped quotes; mis-scanning one can hide a later
// `mutation` token, which is the false-negative direction this must not fail in.
func TestDocumentHasMutationHandlesBlockStrings(t *testing.T) {
	doc := "query A { x(note: \"\"\"he said \"hi\" to me\"\"\") }\n\nmutation B { y }"
	if !DocumentHasMutation(doc) {
		t.Errorf("a block string must not swallow the later mutation: %q", doc)
	}
	if DocumentHasMutation(`query { x(note: """not a mutation""") }`) {
		t.Error("`mutation` inside a block string is not an operation")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
