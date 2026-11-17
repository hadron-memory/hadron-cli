package api

import (
	"testing"

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
