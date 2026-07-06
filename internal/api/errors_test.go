package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func gqlErr(code string) error {
	return gqlerror.List{{Message: "boom", Extensions: map[string]any{"code": code}}}
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitcode.OK},
		{"unauthenticated", gqlErr("UNAUTHENTICATED"), exitcode.AuthRequired},
		{"not found", gqlErr("NOT_FOUND"), exitcode.NotFound},
		{"node not found", gqlErr("NODE_NOT_FOUND"), exitcode.NotFound},
		{"bad input", gqlErr("BAD_USER_INPUT"), exitcode.Usage},
		{"validation", gqlErr("GRAPHQL_VALIDATION_FAILED"), exitcode.Usage},
		{"duplicate", gqlErr("DUPLICATE_APP_AGENT"), exitcode.Conflict},
		{"forbidden", gqlErr("FORBIDDEN"), exitcode.Error},
		{"no extension", gqlerror.List{{Message: "boom"}}, exitcode.Error},
		{"plain", errors.New("network down"), exitcode.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitcode.FromError(MapError(tt.err))
			if got != tt.want {
				t.Errorf("MapError() exit code = %d, want %d", got, tt.want)
			}
		})
	}
}

// A curated command hitting a schema-validation failure (the CLI and server
// disagree on the schema) gets one actionable, direction-NEUTRAL line — not the
// raw envelope — and includes the server's message (#136).
func TestMapErrorSchemaSkewMessage(t *testing.T) {
	err := MapError(gqlErr("GRAPHQL_VALIDATION_FAILED"))
	if err == nil || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("validation code should map to a version-skew hint, got %v", err)
	}
	// The hint must not push a CLI upgrade as the only remedy — a newer CLI vs an
	// older self-hosted server needs the server updated instead.
	if !strings.Contains(err.Error(), "self-hosted") {
		t.Errorf("skew hint should be direction-neutral (mention the server side), got %v", err)
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage", got)
	}

	// Older servers may omit the code and only send the message.
	listErr := gqlerror.List{{Message: `Cannot query field "myMemories" on type "Query"`}}
	e := MapError(listErr)
	if e == nil || !strings.Contains(e.Error(), "out of sync") {
		t.Errorf(`"Cannot query field" should map to a version-skew hint, got %v`, e)
	}
	if !strings.Contains(e.Error(), "myMemories") {
		t.Errorf("skew hint should surface the server message, got %v", e)
	}
}

// A 400 HTTPError carrying a validation error is treated as skew too.
func TestMapErrorSchemaSkewFromHTTP400(t *testing.T) {
	he := &graphql.HTTPError{
		StatusCode: 400,
		Response:   graphql.Response{Errors: gqlerror.List{{Message: `Cannot query field "x"`}}},
	}
	e := MapError(he)
	if e == nil || !strings.Contains(e.Error(), "out of sync") {
		t.Fatalf("a 400 validation error should map to a version-skew hint, got %v", e)
	}
}

// A normal BAD_USER_INPUT must NOT be reframed as a version-skew error.
func TestMapErrorNonSkewUnchanged(t *testing.T) {
	e := MapError(gqlErr("BAD_USER_INPUT"))
	if strings.Contains(e.Error(), "out of sync") {
		t.Errorf("BAD_USER_INPUT should not be reframed as schema skew, got %v", e)
	}
	if got := exitcode.FromError(e); got != exitcode.Usage {
		t.Errorf("BAD_USER_INPUT should stay Usage, got %d", got)
	}
}

// The raw-body message fallback must honor backslash escapes: an embedded \"
// must not truncate the extracted message (Gemini #146 review).
func TestFirstGraphQLMessageUnescapes(t *testing.T) {
	raw := errors.New(`returned error 400: {"errors":[{"message":"Cannot query field \"myMemories\" on type \"Query\""}]}`)
	got := firstGraphQLMessage(raw)
	want := `Cannot query field "myMemories" on type "Query"`
	if got != want {
		t.Errorf("firstGraphQLMessage() = %q, want %q", got, want)
	}
}
