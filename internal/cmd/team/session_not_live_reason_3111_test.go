package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

var allReasons3111 = []string{"DORMANT", "ENDED", "OTHER_SESSION_LIVE", "LIVENESS_UNKNOWN", "NOT_OWNER"}

type notLiveCase struct {
	name string
	ext  map[string]any
	want string
}

func notLiveCases(reasons []string) []notLiveCase {
	var cs []notLiveCase
	for _, r := range reasons {
		cs = append(cs, notLiveCase{r, map[string]any{"reason": r}, r})
	}
	return append(cs,
		notLiveCase{"none", nil, ""},
		notLiveCase{"unknown", map[string]any{"reason": "SOMETHING_NEW_RAW"}, ""},
		notLiveCase{"nonstring", map[string]any{"reason": 7}, ""})
}

func notLiveGQL(ext map[string]any) error {
	e := map[string]any{"code": "SESSION_NOT_LIVE"}
	for k, v := range ext {
		e[k] = v
	}
	return gqlerror.List{{Message: "PRIVATE-UPSTREAM-SENTINEL worker-name session-id", Extensions: e}}
}

// The text of each refusal on the inbox (three codes) and check (all five)
// surfaces: reason and next step shown; none/unknown print today's message
// unchanged; never a server-supplied string, so no worker or session name.
func TestSessionNotLiveReasonTextInboxAndCheck3111(t *testing.T) {
	for _, surface := range []struct {
		name     string
		fail     func(error) error
		reasons  []string
		base     string
		baseFull string
	}{
		{"inbox", func(e error) error { return inboxFailure(e, "cannot read inbox") }, []string{"OTHER_SESSION_LIVE", "LIVENESS_UNKNOWN", "NOT_OWNER"}, "check or resume the worker session before reading again", "this worker session is no longer live; check or resume the worker session before reading again"},
		{"check", func(e error) error { return checkFailure(e) }, allReasons3111, "check the binding before trying again", "worker session is no longer live; check the binding before trying again"},
	} {
		plain := surface.fail(notLiveGQL(nil)).Error()
		if plain != "SESSION_NOT_LIVE: "+surface.baseFull {
			t.Fatalf("%s: today's message changed: %s", surface.name, plain)
		}
		for _, tc := range notLiveCases(surface.reasons) {
			t.Run(surface.name+"/"+tc.name, func(t *testing.T) {
				err := surface.fail(notLiveGQL(tc.ext))
				got := err.Error()
				if exitcode.FromError(err) != exitcode.FromError(surface.fail(notLiveGQL(nil))) || !strings.Contains(got, "SESSION_NOT_LIVE") {
					t.Fatalf("%v", got)
				}
				for _, bad := range []string{"PRIVATE", "worker-name", "session-id", "SOMETHING_NEW_RAW"} {
					if strings.Contains(got, bad) {
						t.Errorf("%q printed: %s", bad, got)
					}
				}
				if tc.want == "" {
					if got != plain {
						t.Errorf("must equal today's message:\n%s\n%s", got, plain)
					}
					if api.SessionNotLiveReason(err) != "" {
						t.Error("reason must be absent")
					}
					return
				}
				if !strings.Contains(got, tc.want) || !strings.Contains(got, api.SessionNotLiveNextStep(tc.want)) {
					t.Errorf("reason or next step missing: %s", got)
				}
				if api.SessionNotLiveReason(err) != tc.want {
					t.Errorf("reason not carried to --json: %s", got)
				}
			})
		}
	}
}
