package api

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

func notLive(ext map[string]any) error {
	e := map[string]any{"code": "SESSION_NOT_LIVE"}
	for k, v := range ext {
		e[k] = v
	}
	return gqlerror.List{{Message: "m", Extensions: e}}
}

func TestSessionNotLiveHelpers3111(t *testing.T) {
	if got := SessionNotLiveNextStep("garbage"); got != "" {
		t.Errorf("next step for an unknown reason: %q", got)
	}
	if WithSessionNotLiveReason(nil, notLive(map[string]any{"reason": "ENDED"})) != nil {
		t.Error("a nil mapped error must stay nil")
	}
	mapped := errors.New("mapped")
	if got := WithSessionNotLiveReason(mapped, notLive(map[string]any{"reason": "NOT_YOURS"})); got != mapped {
		t.Error("an unknown reason must return mapped unchanged")
	}
	if got := SessionNotLiveReason(WithSessionNotLiveReason(mapped, notLive(map[string]any{"reason": "ENDED"}))); got != "ENDED" {
		t.Errorf("reason lost through the wrapper: %q", got)
	}
	// Only a SESSION_NOT_LIVE refusal's reason counts.
	other := gqlerror.List{{Message: "m", Extensions: map[string]any{"code": "FORBIDDEN", "reason": "ENDED"}}}
	if SessionNotLiveReason(other) != "" {
		t.Error("a reason on another code must not count")
	}
	got := AnnotateSessionNotLive("base", func(s string) error { return errors.New(s) }, notLive(map[string]any{"reason": "NOT_OWNER"}))
	if got.Error() != "base (NOT_OWNER: "+SessionNotLiveNextStep("NOT_OWNER")+")" {
		t.Errorf("%q", got)
	}
}
