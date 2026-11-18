package api

import (
	"strconv"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestSupersededEvidenceIsOneDefinitivePair1845(t *testing.T) {
	valid := func() *gqlerror.Error {
		return &gqlerror.Error{Message: "Session superseded.", Extensions: map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "C", "startedAt": "2026-11-12T16:37:52Z"}}
	}
	mixed := gqlerror.List{valid(), {Message: "fault", Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"}}}
	if definite, id, stamp := SupersededSessionEvidence(mixed); definite || id != "" || stamp != "" {
		t.Fatalf("mixed error became definitive: %v %q %q", definite, id, stamp)
	}
	// Details on separate errors are never joined into invented evidence.
	partial := valid()
	delete(partial.Extensions, "startedAt")
	later := valid()
	delete(later.Extensions, "sessionId")
	if definite, id, stamp := SupersededSessionEvidence(gqlerror.List{partial, later}); !definite || id != "" || stamp != "" {
		t.Fatalf("partial errors combined: %v %q %q", definite, id, stamp)
	}
	for _, stamp := range []string{"", "not-time", "2026-02-30T12:00:00Z", "2026-11-12", "2026-11-12T16:37:52Z\n"} {
		e := valid()
		e.Extensions["startedAt"] = stamp
		if definite, id, ts := SupersededSessionEvidence(e); !definite || id != "" || ts != "" {
			t.Fatalf("invalid time projected: %q -> %v %q %q", stamp, definite, id, ts)
		}
	}
}

// Error.As is permitted to implement its own matching. This adversarial
// wrapper demonstrates disagreement between the two extractions without
// changing OnlyErrorCodes globally. It is not a production-transport repro.
type disappearingSupersededError1845 struct{ reads int }

func (e *disappearingSupersededError1845) Error() string { return "changing GraphQL error list" }
func (e *disappearingSupersededError1845) As(target any) bool {
	if list, ok := target.(*gqlerror.List); ok {
		e.reads++
		*list = nil
		if e.reads == 1 {
			*list = gqlerror.List{&gqlerror.Error{Extensions: map[string]any{"code": "SESSION_SUPERSEDED"}}}
		}
		return true
	}
	return false
}

func TestSupersededEmptyListGuard1845(t *testing.T) {
	for _, err := range []error{nil, gqlerror.List{}, &disappearingSupersededError1845{}} {
		t.Run("empty-or-disappearing", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("empty-list guard panicked: %v", r)
				}
			}()
			definite, id, stamp := SupersededSessionEvidence(err)
			if definite || id != "" || stamp != "" {
				t.Fatalf("empty list should use normal error path: %v %q %q", definite, id, stamp)
			}
		})
	}
}

// The first refusal owns the whole pair; later errors cannot replace it or
// supply evidence it omitted (cli#2251). Controls added for cli#2311 item 4
// hold cor:agt:020:03's CLI clause (#2243): an id is shown only with a
// well-formed time and the reverse, and evidence that is not the definitive
// refusal never lends a pair.
func TestSupersededPairOrdering2251(t *testing.T) {
	pair := func(id, stamp string) *gqlerror.Error {
		return &gqlerror.Error{Message: "Session superseded.", Extensions: map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": id, "startedAt": stamp}}
	}
	codeOnly := func() *gqlerror.Error {
		return &gqlerror.Error{Extensions: map[string]any{"code": "SESSION_SUPERSEDED"}}
	}
	idOnly := &gqlerror.Error{Extensions: map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "A"}}
	const firstTime = "2026-11-12T16:37:52Z"
	const secondTime = "2026-11-13T10:00:00Z"
	for _, tc := range []struct {
		name       string
		list       gqlerror.List
		definitive bool
		id, stamp  string
	}{
		{"A-then-B", gqlerror.List{pair("A", firstTime), pair("B", secondTime)}, true, "A", firstTime},
		{"B-then-A", gqlerror.List{pair("B", secondTime), pair("A", firstTime)}, true, "B", secondTime},
		{"code-only-then-pair", gqlerror.List{codeOnly(), pair("B", secondTime)}, true, "", ""},
		// The pair is the first error's, whole: a trailing code-only refusal
		// neither erases it nor changes it.
		{"pair-then-code-only", gqlerror.List{pair("A", firstTime), codeOnly()}, true, "A", firstTime},
		// A pair is id AND time. An empty id is discarded even beside a good time
		// (the CLI must not show a time with no session to name), mirroring the
		// empty-time rows in TestSupersededEvidenceIsOneDefinitivePair1845.
		{"empty-id", gqlerror.List{pair("", firstTime)}, true, "", ""},
		{"id-without-time", gqlerror.List{idOnly}, true, "", ""},
		// Another code anywhere in the list means this is not the definitive
		// refusal, so no pair is borrowed from the superseded entry.
		{"superseded-then-other-code", gqlerror.List{pair("A", firstTime), {Extensions: map[string]any{"code": "FORBIDDEN"}}}, false, "", ""},
		{"other-code-then-superseded", gqlerror.List{{Extensions: map[string]any{"code": "FORBIDDEN"}}, pair("A", firstTime)}, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definitive, id, stamp := SupersededSessionEvidence(tc.list)
			if definitive != tc.definitive || id != tc.id || stamp != tc.stamp {
				t.Fatalf("got %v %q %q, want %v %q %q", definitive, id, stamp, tc.definitive, tc.id, tc.stamp)
			}
		})
	}
}

// TestSupersededGatewayInclusionAndExclusion2251: the same typed envelope is
// definitive on a 4xx (HTTPError extraction works, so an implementation that
// ignores every HTTPError fails) and never on a gateway 5xx (the code riding a
// fault is not the refusal). The helper's boundary is status < 500: 502-504 are
// the gateway examples, 500 a plain origin fault, and the 200 row is a
// deliberately synthetic HTTPError (a real 200 is never an HTTPError) that
// exercises the boundary from below. 409 is the real refusal status.
func TestSupersededGatewayInclusionAndExclusion2251(t *testing.T) {
	for _, tc := range []struct {
		status int
		why    string
	}{
		{200, "synthetic: a real 200 is not an HTTPError"},
		{409, "the real refusal status"},
		{500, "plain origin fault"},
		{502, "gateway"},
		{503, "gateway"},
		{504, "gateway"},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			err := &graphql.HTTPError{StatusCode: tc.status, Response: graphql.Response{Errors: gqlerror.List{&gqlerror.Error{Extensions: map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "A", "startedAt": "2026-11-12T16:37:52Z"}}}}}
			definitive, id, stamp := SupersededSessionEvidence(err)
			if tc.status < 500 {
				if !definitive || id != "A" || stamp != "2026-11-12T16:37:52Z" {
					t.Errorf("HTTP %d (%s) lost definitive evidence: %v %q %q", tc.status, tc.why, definitive, id, stamp)
				}
			} else if definitive || id != "" || stamp != "" {
				t.Errorf("HTTP %d (%s) became definitive: %v %q %q", tc.status, tc.why, definitive, id, stamp)
			}
		})
	}
}
