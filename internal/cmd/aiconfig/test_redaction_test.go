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

func TestRedactTestError1538MetadataOnlyEcho(t *testing.T) {
	// The server code is rendered separately; the visible Error() is clean.
	const key = "INTERNAL_SERVER_ERROR"
	original := api.NewInternalFaultError("safe-request-1538", nil)
	if strings.Contains(original.Error(), key) {
		t.Fatal("control is not metadata-only")
	}
	got := redactTestError(original, key)
	fault, ok := api.InternalFaultOf(got)
	if !ok || fault.ServerCode != "[redacted]" || fault.RequestID != "safe-request-1538" {
		t.Fatalf("metadata not sanitized: %#v", fault)
	}
	if got.Error() != original.Error() || exitcode.FromError(got) != exitcode.Error {
		t.Fatal("clean message/classification changed")
	}
}
