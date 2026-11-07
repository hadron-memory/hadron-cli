package node

import (
	"errors"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestContentValidationProjectionFallback1706(t *testing.T) {
	missing := func() *gqlerror.Error {
		return &gqlerror.Error{Message: `Cannot query field "authorCheck" on type "NodeContentValidationReport".`, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}
	}
	denied := &gqlerror.Error{Message: missing().Message, Extensions: map[string]any{"code": "FORBIDDEN"}}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"parsed old field", gqlerror.List{missing()}, true},
		{"http400 old field", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{missing()}}}, true},
		{"http500 schema prose", &graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{missing()}}}, false},
		{"http403 schema prose", &graphql.HTTPError{StatusCode: 403, Response: graphql.Response{Errors: gqlerror.List{missing()}}}, false},
		{"http400 populated partial", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Data: map[string]any{"nodeBatch": nil}, Errors: gqlerror.List{missing()}}}, false},
		{"permission with same prose", gqlerror.List{denied}, false},
		{"mixed", gqlerror.List{missing(), denied}, false},
		{"null member", gqlerror.List{missing(), nil}, false},
		{"unrelated field", gqlerror.List{{Message: `Cannot query field "unrelated" on type "Node".`, Extensions: missing().Extensions}}, false},
		{"transport prose", errors.New(missing().Message), false},
		{"old field without code", gqlerror.List{{Message: missing().Message}}, true},
		{"resolver field error", gqlerror.List{{Message: missing().Message, Extensions: missing().Extensions, Path: ast.Path{ast.PathName("nodeBatch")}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnknownContentValidationProjection(tc.err); got != tc.want {
				t.Fatalf("fallback=%t want%t", got, tc.want)
			}
		})
	}
}
