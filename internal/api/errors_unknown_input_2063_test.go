package api

import (
	"errors"
	"testing"

	"github.com/Khan/genqlient/graphql"
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

func TestIsDefiniteRefusal(t *testing.T) {
	coded := func(c string) *gqlerror.Error {
		return &gqlerror.Error{Message: "m", Extensions: map[string]any{"code": c}}
	}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"coded refusal":  {gqlerror.List{coded("HOST_ASSIGNMENT_NOT_PERMITTED")}, true},
		"two coded":      {gqlerror.List{coded("FORBIDDEN"), coded("BAD_USER_INPUT")}, true},
		"internal":       {gqlerror.List{coded("INTERNAL_SERVER_ERROR")}, false},
		"mixed internal": {gqlerror.List{coded("FORBIDDEN"), coded("INTERNAL_SERVER_ERROR")}, false},
		"no code":        {gqlerror.List{{Message: "m"}}, false},
		"transport":      {errors.New("connection reset"), false},
		"nil":            {nil, false},
		"empty list":     {gqlerror.List{}, false},
	} {
		if got := IsDefiniteRefusal(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestIsDefiniteRefusalAt398(t *testing.T) {
	at := func(code string, path ...string) *gqlerror.Error {
		var p ast.Path
		for _, e := range path {
			p = append(p, ast.PathName(e))
		}
		return &gqlerror.Error{Message: "m", Path: p, Extensions: map[string]any{"code": code}}
	}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"no path":           {gqlerror.List{at("FORBIDDEN")}, true},
		"at the field":      {gqlerror.List{at("BAD_USER_INPUT", "allocateSpecNode")}, true},
		"below the field":   {gqlerror.List{at("FORBIDDEN", "allocateSpecNode", "revision")}, false},
		"mixed below":       {gqlerror.List{at("FORBIDDEN"), at("FORBIDDEN", "allocateSpecNode", "id")}, false},
		"another field":     {gqlerror.List{at("FORBIDDEN", "other")}, false},
		"internal at field": {gqlerror.List{at("INTERNAL_SERVER_ERROR", "allocateSpecNode")}, false},
		"http envelope":     {&graphql.HTTPError{StatusCode: 200, Response: graphql.Response{Errors: gqlerror.List{at("FORBIDDEN", "allocateSpecNode", "id")}}}, false},
	} {
		if got := IsDefiniteRefusalAt(tc.err, "allocateSpecNode"); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
