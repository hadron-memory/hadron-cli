package node

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// A real createEdge refusal: the server's message, with the document location
// and field path as the separate fields a GraphQL response actually carries.
// Reading those fields is what makes the reason exact instead of parsed (#566).
func edgeWireErr(msg string) error {
	return gqlerror.List{&gqlerror.Error{
		Message:   msg,
		Locations: []gqlerror.Location{{Line: 3, Column: 5}},
		Path:      ast.Path{ast.PathName("createEdge")},
	}}
}

// TestEdgeRejectReasonReadsTheServerMessage covers the path a real failure
// takes. Every case here renders as `input:3: createEdge <message>` through
// err.Error(), so a reason equal to the bare message is proof the structured
// field was read rather than the rendering parsed.
func TestEdgeRejectReasonReadsTheServerMessage(t *testing.T) {
	cases := []struct{ in, want string }{
		{"operator 'flag' is not in the v1 allowlist", "operator 'flag' is not in the v1 allowlist"},
		// A colon inside the server's own message. The old strip had to be
		// careful here; reading the field cannot get it wrong at all.
		{"field 'x': required", "field 'x': required"},
		// A message that itself begins with "input:" — the shape that defeats
		// any prefix-stripping approach, because the token it strips is
		// indistinguishable from the message's own opening.
		{"input: must be an object", "input: must be an object"},
	}
	for _, c := range cases {
		if got := edgeRejectReason(edgeWireErr(c.in)); got != c.want {
			t.Errorf("edgeRejectReason(wire %q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A multi-error response reports the FIRST refusal: this renders one line per
// edge, and each row is one edge's own failure.
func TestEdgeRejectReasonTakesTheFirstOfSeveral(t *testing.T) {
	err := gqlerror.List{
		{Message: "first error", Locations: []gqlerror.Location{{Line: 3}}},
		{Message: "second error", Locations: []gqlerror.Location{{Line: 4}}},
	}
	if got := edgeRejectReason(err); got != "first error" {
		t.Errorf("edgeRejectReason = %q, want %q", got, "first error")
	}
}

// TestEdgeRejectReason locks the TEXT FALLBACK — the path a non-GraphQL failure
// takes, where there is no structured message to read and parsing the string is
// still the only option. Kept as a table of raw strings deliberately: these are
// errors that never carried a GraphQL envelope.
func TestEdgeRejectReason(t *testing.T) {
	cases := []struct{ in, want string }{
		// the real createEdge allowlist rejection
		{"input:3: createEdge operator 'flag' is not in the v1 allowlist", "createEdge operator 'flag' is not in the v1 allowlist"},
		// inner colon must survive
		{"input:3: field 'x': required", "field 'x': required"},
		// line:col location token
		{"input:3:5: line and col", "line and col"},
		// no line number
		{"input: no line number", "no line number"},
		// no genqlient prefix at all
		{"plain error, no prefix", "plain error, no prefix"},
		// multi-error: first line only
		{"input:3: first error\ninput:4: second error", "first error"},
	}
	for _, c := range cases {
		if got := edgeRejectReason(errors.New(c.in)); got != c.want {
			t.Errorf("edgeRejectReason(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
