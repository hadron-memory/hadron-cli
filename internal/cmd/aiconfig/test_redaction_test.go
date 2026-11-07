package aiconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestRedactTestError1538Controls(t *testing.T) {
	original := exitcode.New(exitcode.Forbidden, errors.New("permission denied"))
	for _, key := range []string{"", "sk-not-echoed"} {
		if got := redactTestError(original, key); got != original {
			t.Fatal("non-echo error chain changed")
		}
	}
	if got := redactTestError(nil, "secret"); got != nil {
		t.Fatal("nil error changed")
	}
	for _, key := range []string{"redacted", "[", "x"} {
		got := redactTestError(exitcode.Newf(exitcode.Usage, "invalid key %s", key), key)
		if exitcode.FromError(got) != exitcode.Usage {
			t.Fatal("classification changed")
		}
		// The chosen placeholder must not itself reintroduce the submitted value.
		if strings.Contains(got.Error(), key) {
			t.Fatal("short key was not removed")
		}
	}
}

func TestRedactTestError1538DetachesRawChain(t *testing.T) {
	const key = "sk-detach-1538"
	raw := gqlerror.List{&gqlerror.Error{Message: "rejected " + key, Extensions: map[string]any{"code": "BAD_USER_INPUT"}}}
	mapped := api.MapError(raw)
	var before gqlerror.List
	if !errors.As(mapped, &before) {
		t.Fatal("control does not expose raw GraphQL list")
	}
	got := redactTestError(mapped, key)
	var after gqlerror.List
	if errors.As(got, &after) {
		t.Fatal("redacted error retains raw GraphQL list")
	}
	if exitcode.FromError(got) != exitcode.Usage || got.Error() != "rejected [redacted]" {
		t.Fatal("safe diagnostic/classification changed")
	}
}
