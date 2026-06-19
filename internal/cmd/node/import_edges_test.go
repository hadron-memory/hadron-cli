package node

import (
	"errors"
	"testing"
)

// TestEdgeRejectReason locks the location-prefix trimming: only genqlient's
// "input:<line>[:<col>]:" token is stripped — colons inside the server's own
// message are preserved, and a message without the prefix is returned verbatim.
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
