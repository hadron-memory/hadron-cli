package api

import (
	"errors"
	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"testing"
)

func TestUnknownQueryField1554(t *testing.T) {
	field := "specBacklinksPage"
	unknown := func() *gqlerror.Error {
		return &gqlerror.Error{Message: `Cannot query field "specBacklinksPage" on type "Query". Did you mean "specBacklinks"?`, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}
	}
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"exact", gqlerror.List{unknown()}, true},
		{"http400", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{unknown()}}}, true},
		{"http500", &graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{unknown()}}}, false},
		{"http partial", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{unknown()}, Data: map[string]any{"specBacklinksPage": nil}}}, false},
		{"plain prose", errors.New(unknown().Message), false},
		{"mixed", gqlerror.List{unknown(), {Message: "denied", Extensions: map[string]any{"code": "FORBIDDEN"}}}, false},
		{"null entry", gqlerror.List{unknown(), nil}, false},
		{"wrong field", gqlerror.List{{Message: `Cannot query field "specBacklinksPageExtra" on type "Query".`, Extensions: unknown().Extensions}}, false},
		{"generic validation", gqlerror.List{{Message: `bad "specBacklinksPage" selection`, Extensions: unknown().Extensions}}, false},
		{"wrong type", gqlerror.List{{Message: `Cannot query field "specBacklinksPage" on type "Mutation".`, Extensions: unknown().Extensions}}, false},
		{"permission prose", gqlerror.List{{Message: unknown().Message, Extensions: map[string]any{"code": "FORBIDDEN"}}}, false},
		{"resolver path", gqlerror.List{{Message: unknown().Message, Extensions: unknown().Extensions, Path: ast.Path{ast.PathName(field)}}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUnknownQueryField(tt.err, field); got != tt.want {
				t.Fatalf("got=%v", got)
			}
		})
	}
}
