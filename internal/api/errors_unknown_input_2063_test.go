package api

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestIsUnknownInputFieldOnly(t *testing.T) {
	msg := func(f string) string {
		return `Variable "$input" got invalid value {}; Field "` + f + `" is not defined by type "WorkerTerminalReportInput".`
	}
	is := func(err error) bool {
		return IsUnknownInputFieldOnly(err, "WorkerTerminalReportInput", "contextTokens", "contextMeasuredAt")
	}
	ok := gqlerror.List{{Message: msg("contextMeasuredAt")}, {Message: msg("contextTokens"), Extensions: map[string]any{"code": "BAD_USER_INPUT"}}}
	if !is(ok) {
		t.Fatal("the genuine old-server refusal must match")
	}
	for name, err := range map[string]error{
		"nil":            nil,
		"transport":      errors.New("dial tcp: " + msg("contextTokens")),
		"empty":          gqlerror.List{},
		"resolver path":  gqlerror.List{{Message: msg("contextTokens"), Path: ast.Path{ast.PathName("setHostWorkerTerminals")}}},
		"internal code":  gqlerror.List{{Message: msg("contextTokens"), Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"}}},
		"forbidden":      gqlerror.List{{Message: "forbidden", Extensions: map[string]any{"code": "FORBIDDEN"}}},
		"mixed":          gqlerror.List{{Message: msg("contextTokens")}, {Message: "forbidden", Extensions: map[string]any{"code": "FORBIDDEN"}}},
		"other field":    gqlerror.List{{Message: msg("repair")}},
		"other type":     gqlerror.List{{Message: `Field "contextTokens" is not defined by type "SomethingElse".`}},
		"text in resolv": gqlerror.List{{Message: "contextTokens: unknown field in storage result", Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"}}},
	} {
		if is(err) {
			t.Errorf("%s must not be treated as an old-server refusal", name)
		}
	}
}
