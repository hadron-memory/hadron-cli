package team

import (
	"errors"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func gqlAt(path ...any) *gqlerror.Error {
	p := ast.Path{}
	for _, s := range path {
		p = append(p, ast.PathName(s.(string)))
	}
	return &gqlerror.Error{Message: "m", Path: p}
}

// cli#1709: which GraphQL error lists mean "the write committed, its receipt
// failed". Pure classification, so the corners are pinned directly.
func TestReadbackFieldFailed1709(t *testing.T) {
	read := gqlAt("disposeAgentTaskFinding", "findingDisposition")
	id := gqlAt("disposeAgentTaskFinding", "id")
	write := gqlAt("disposeAgentTaskFinding")
	pathless := &gqlerror.Error{Message: "validation"}
	foreign := gqlAt("somethingElse", "x")
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"readback":               {gqlerror.List{read}, true},
		"id":                     {gqlerror.List{id}, true},
		"nested":                 {gqlerror.List{gqlAt("disposeAgentTaskFinding", "findingDisposition", "reference")}, true},
		"readback plus pathless": {gqlerror.List{read, pathless}, true},
		"pathless first":         {gqlerror.List{pathless, id}, true},
		"readback plus foreign":  {gqlerror.List{foreign, read}, true},
		"write field refused":    {gqlerror.List{write}, false},
		"readback plus write":    {gqlerror.List{read, write}, false},
		"pathless only":          {gqlerror.List{pathless}, false},
		"foreign only":           {gqlerror.List{foreign}, false},
		"empty list":             {gqlerror.List{}, false},
		"http 500, empty errors": {&graphql.HTTPError{StatusCode: 500}, false},
		"http 500 with readback": {&graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{read}}}, true},
		"single error value":     {read, true},
		"wrapped list":           {errors.Join(errors.New("ctx"), gqlerror.List{id}), true},
		"not a graphql error":    {errors.New("boom"), false},
		"nil error":              {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := readbackFieldFailed(tc.err); got != tc.want {
				t.Fatalf("readbackFieldFailed = %v, want %v", got, tc.want)
			}
		})
	}
}
