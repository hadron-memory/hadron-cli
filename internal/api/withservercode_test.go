package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func codedRaw(code, msg string) error {
	return gqlerror.List{{Message: msg, Extensions: map[string]any{"code": code}}}
}

// A nil mapped error stays a true nil whatever code raw carries: a typed
// wrapper around nil would read as a failure.
func TestWithServerCodeNilMappedStaysNil(t *testing.T) {
	if got := WithServerCode(nil, codedRaw("FORBIDDEN", "x")); got != nil {
		t.Fatalf("nil mapped became %#v", got)
	}
}

func TestWithServerCodeWithoutCodeReturnsMappedAsIs(t *testing.T) {
	mapped := exitcode.Newf(exitcode.Error, "mapped")
	if got := WithServerCode(mapped, errors.New("plain")); got != mapped {
		t.Fatalf("identity lost: %#v", got)
	}
}

func TestWithServerCodeKeepsCodeExitAndMappedChainOnly(t *testing.T) {
	const secret = "https://provider.example/authorize?nonce=SENTINELNONCE1"
	mapped := exitcode.Newf(exitcode.Forbidden, "refused")
	for name, raw := range map[string]error{
		"list": codedRaw("FORBIDDEN", secret),
		"http": &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{{Message: secret, Extensions: map[string]any{"code": "FORBIDDEN"}}}}},
	} {
		got := WithServerCode(mapped, raw)
		var coded interface{ ServerCode() string }
		if !errors.As(got, &coded) || coded.ServerCode() != "FORBIDDEN" {
			t.Fatalf("%s: code lost: %v", name, got)
		}
		if exitcode.FromError(got) != exitcode.Forbidden {
			t.Errorf("%s: exit = %d", name, exitcode.FromError(got))
		}
		if got.Error() != "refused" {
			t.Errorf("%s: message = %q", name, got.Error())
		}
		for e := got; e != nil; e = errors.Unwrap(e) {
			if strings.Contains(e.Error(), "SENTINELNONCE1") {
				t.Errorf("%s: raw text reachable through the chain: %q", name, e.Error())
			}
		}
		if errors.Is(got, raw) {
			t.Errorf("%s: raw reachable via errors.Is", name)
		}
	}
}

// First code wins when a response carries several.
func TestWithServerCodeFirstCodeWins(t *testing.T) {
	raw := gqlerror.List{
		{Message: "a", Extensions: map[string]any{"code": "FIRST"}},
		{Message: "b", Extensions: map[string]any{"code": "SECOND"}},
	}
	var coded interface{ ServerCode() string }
	got := WithServerCode(exitcode.Newf(exitcode.Error, "m"), raw)
	if !errors.As(got, &coded) || coded.ServerCode() != "FIRST" {
		t.Fatalf("got %v", got)
	}
}

func TestMapErrorKeepingCodeDelegates(t *testing.T) {
	raw := codedRaw("FORBIDDEN", "nope")
	if got, want := MapErrorKeepingCode(raw), WithServerCode(MapError(raw), raw); got.Error() != want.Error() ||
		exitcode.FromError(got) != exitcode.FromError(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if MapErrorKeepingCode(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
