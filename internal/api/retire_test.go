package api

import (
	"net/http"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func retireErr(msg string, path ast.Path, code string) *gqlerror.Error {
	e := &gqlerror.Error{Message: msg, Path: path}
	if code != "" {
		e.Extensions = map[string]any{"code": code}
	}
	return e
}

func httpStatus(status int, errs ...*gqlerror.Error) error {
	return &graphql.HTTPError{StatusCode: status, Response: graphql.Response{Errors: errs}}
}

// hadron-cli#3266 item 1: pins for the two verdicts retireSpec's failure handling
// rests on. Each case names the mutant it kills.
func TestIsRefusedBeforeCommit3266(t *testing.T) {
	field := ast.Path{ast.PathName("retireSpec")}
	deeper := ast.Path{ast.PathName("retireSpec"), ast.PathName("node")}
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"a coded error at the field is a refusal":   {gqlerror.List{retireErr("x", field, "NODE_WRITE_CONFLICT")}, true},
		"a coded error with no path is a refusal":   {gqlerror.List{retireErr("x", nil, "FORBIDDEN")}, true},
		"an uncoded error is unknown":               {gqlerror.List{retireErr("x", field, "")}, false},
		"INTERNAL_SERVER_ERROR is unknown":          {gqlerror.List{retireErr("x", field, "INTERNAL_SERVER_ERROR")}, false},
		"a path below the field is unknown":         {gqlerror.List{retireErr("x", deeper, "NODE_WRITE_CONFLICT")}, false},
		"mixed: one refusal and one uncoded":        {gqlerror.List{retireErr("x", field, "NODE_WRITE_CONFLICT"), retireErr("y", field, "")}, false},
		"mixed: one refusal and one internal fault": {gqlerror.List{retireErr("x", field, "FORBIDDEN"), retireErr("y", field, "INTERNAL_SERVER_ERROR")}, false},
		"mixed: one refusal and one deeper error":   {gqlerror.List{retireErr("x", field, "FORBIDDEN"), retireErr("y", deeper, "FORBIDDEN")}, false},
		"a nil entry is unknown":                    {gqlerror.List{nil}, false},
		"an empty list with HTTP 401 is a refusal":  {httpStatus(401), true},
		"an empty list with HTTP 403 is a refusal":  {httpStatus(403), true},
		"an empty list with HTTP 500 is unknown":    {httpStatus(500), false},
		"an empty list with HTTP 502 is unknown":    {httpStatus(http.StatusBadGateway), false},
		"a non-GraphQL error is unknown":            {http.ErrHandlerTimeout, false},
		"HTTP 403 carrying an uncoded error":        {httpStatus(403, retireErr("x", field, "")), false},
		"HTTP 500 carrying a coded error at field":  {httpStatus(500, retireErr("x", field, "FORBIDDEN")), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsRefusedBeforeCommit(c.err); got != c.want {
				t.Errorf("IsRefusedBeforeCommit = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsRetireSpecUnsupported3266(t *testing.T) {
	const code = "GRAPHQL_VALIDATION_FAILED"
	field := `Cannot query field "retireSpec" on type "Mutation".`
	input := `Unknown type "RetireSpecInput".`
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"the missing field":                          {gqlerror.List{retireErr(field+` Did you mean "x"?`, nil, code)}, true},
		"the missing input type":                     {gqlerror.List{retireErr(input+" Did you mean x?", nil, code)}, true},
		"both, as a server without the door answers": {gqlerror.List{retireErr(field, nil, code), retireErr(input, nil, code)}, true},
		"a validation error that carries a path":     {gqlerror.List{retireErr(field, ast.Path{ast.PathName("retireSpec")}, code)}, false},
		"the right text with another code":           {gqlerror.List{retireErr(field, nil, "NODE_WRITE_CONFLICT")}, false},
		"the right text with no code":                {gqlerror.List{retireErr(field, nil, "")}, false},
		"another validation message":                 {gqlerror.List{retireErr(`Cannot query field "other" on type "Mutation".`, nil, code)}, false},
		"the prefix must start the message":          {gqlerror.List{retireErr("x "+field, nil, code)}, false},
		"the field name must match exactly":          {gqlerror.List{retireErr(`Cannot query field "retireSpecs" on type "Mutation".`, nil, code)}, false},
		"the type must be Mutation":                  {gqlerror.List{retireErr(`Cannot query field "retireSpec" on type "Query".`, nil, code)}, false},
		"one unsupported and one other error":        {gqlerror.List{retireErr(field, nil, code), retireErr("x", nil, "FORBIDDEN")}, false},
		"a nil entry":                                {gqlerror.List{nil}, false},
		"an empty list":                              {gqlerror.List{}, false},
		"a non-GraphQL error":                        {http.ErrHandlerTimeout, false},
		"HTTP 400 carrying the missing field":        {httpStatus(400, retireErr(field, nil, code)), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsRetireSpecUnsupported(c.err); got != c.want {
				t.Errorf("IsRetireSpecUnsupported = %v, want %v", got, c.want)
			}
		})
	}
}
