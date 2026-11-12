package api

import (
	"errors"
	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"testing"
)

func TestMemoryGrantFieldsUnavailable(t *testing.T) {
	valid := func(field string) *gqlerror.Error {
		return &gqlerror.Error{Message: `Cannot query field "` + field + `" on type "AppMemoryWriteGrant".`, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}
	}
	path := valid("notEffective")
	path.Path = ast.Path{ast.PathName("revokeAppMemoryWriteGrant")}
	wrongType := valid("notEffective")
	wrongType.Message = `Cannot query field "notEffective" on type "Other".`
	wrongCode := valid("notEffective")
	wrongCode.Extensions["code"] = "FORBIDDEN"
	noCode := valid("notEffective")
	noCode.Extensions = nil
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"both", gqlerror.List{valid("notEffective"), valid("notEffectiveReason")}, true},
		{"one", gqlerror.List{valid("notEffectiveReason")}, true},
		{"http400", &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: gqlerror.List{valid("notEffective")}}}, true},
		{"http500", &graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{valid("notEffective")}}}, false},
		{"path", gqlerror.List{path}, false}, {"wrong type", gqlerror.List{wrongType}, false}, {"wrong code", gqlerror.List{wrongCode}, false}, {"no code", gqlerror.List{noCode}, false},
		{"mixed", gqlerror.List{valid("notEffective"), {Message: "denied", Extensions: map[string]any{"code": "FORBIDDEN"}}}, false},
		{"other field", gqlerror.List{valid("other")}, false},
		{"transport", errors.New(`GRAPHQL_VALIDATION_FAILED Cannot query field "notEffective" on type "AppMemoryWriteGrant".`), false},
		{"nil", nil, false}, {"empty", gqlerror.List{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MemoryGrantFieldsUnavailable(tc.err); got != tc.want {
				t.Fatalf("got %t want %t", got, tc.want)
			}
		})
	}
}
