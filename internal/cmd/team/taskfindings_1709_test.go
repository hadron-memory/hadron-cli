package team

import (
	"errors"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func gqlAt(path ...string) *gqlerror.Error {
	p := ast.Path{}
	for _, s := range path {
		p = append(p, ast.PathName(s))
	}
	return &gqlerror.Error{Message: "m", Path: p}
}

// cli#1709: which GraphQL error lists are the unconfirmed-write class: an error
// attributed to the mutation (its field or beneath it) with no typed refusal
// code, so the write may or may not have committed. Pure classification, so
// the corners are pinned directly.
func TestReadbackFieldFailed1709(t *testing.T) {
	read := gqlAt("disposeAgentTaskFinding", "findingDisposition")
	id := gqlAt("disposeAgentTaskFinding", "id")
	rootGeneric := gqlAt("disposeAgentTaskFinding")
	rootTyped := gqlAt("disposeAgentTaskFinding")
	rootTyped.Extensions = map[string]any{"code": "TASK_NOT_OWNER"}
	rootInternal := gqlAt("disposeAgentTaskFinding")
	rootInternal.Extensions = map[string]any{"code": "INTERNAL_SERVER_ERROR"}
	readTyped := gqlAt("disposeAgentTaskFinding", "findingDisposition")
	readTyped.Extensions = map[string]any{"code": "TASK_NOT_OWNER"}
	foreignTyped := gqlAt("somethingElse")
	foreignTyped.Extensions = map[string]any{"code": "TASK_NOT_OWNER"}
	pathless := &gqlerror.Error{Message: "validation"}
	typedPathless := &gqlerror.Error{Message: "Refused", Extensions: map[string]any{"code": "TASK_NOT_OWNER"}}
	foreign := gqlAt("somethingElse", "x")
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"readback":                                      {gqlerror.List{read}, true},
		"id":                                            {gqlerror.List{id}, true},
		"nested":                                        {gqlerror.List{gqlAt("disposeAgentTaskFinding", "findingDisposition", "reference")}, true},
		"readback plus pathless":                        {gqlerror.List{read, pathless}, true},
		"pathless first":                                {gqlerror.List{pathless, id}, true},
		"readback plus foreign":                         {gqlerror.List{foreign, read}, true},
		"root typed refusal":                            {gqlerror.List{rootTyped}, false},
		"readback plus root typed":                      {gqlerror.List{read, rootTyped}, false},
		"root typed plus readback":                      {gqlerror.List{rootTyped, read}, false},
		"root generic alone":                            {gqlerror.List{rootGeneric}, true},
		"root internal alone":                           {gqlerror.List{rootInternal}, true},
		"root generic plus readback":                    {gqlerror.List{rootGeneric, read}, true},
		"readback plus root generic":                    {gqlerror.List{read, rootInternal}, true},
		"root generic plus typed pathless":              {gqlerror.List{rootGeneric, typedPathless}, true},
		"typed pathless beside readback":                {gqlerror.List{read, typedPathless}, true},
		"typed pathless first":                          {gqlerror.List{typedPathless, id}, true},
		"typed code beneath the write is not a refusal": {gqlerror.List{readTyped}, true},
		"typed code at the readback beside root typed":  {gqlerror.List{readTyped, rootTyped}, false},
		"foreign-root typed code alone":                 {gqlerror.List{foreignTyped}, false},
		"foreign-root typed code beside readback":       {gqlerror.List{foreignTyped, read}, true},
		"pathless only":                                 {gqlerror.List{pathless}, false},
		"foreign only":                                  {gqlerror.List{foreign}, false},
		"empty list":                                    {gqlerror.List{}, false},
		"http 500, empty errors":                        {&graphql.HTTPError{StatusCode: 500}, false},
		"http 500 with readback":                        {&graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{read}}}, true},
		"single error value":                            {read, true},
		"wrapped list":                                  {errors.Join(errors.New("ctx"), gqlerror.List{id}), true},
		"not a graphql error":                           {errors.New("boom"), false},
		"nil error":                                     {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := hasUnconfirmedDisposeError(tc.err); got != tc.want {
				t.Fatalf("hasUnconfirmedDisposeError = %v, want %v", got, tc.want)
			}
		})
	}
}

// writeRefusal picks the write field's typed refusal out of a mixed list, in
// either order, so its code (and exit status) survive; nothing is picked when
// the list has no such refusal.
func TestWriteRefusal1709(t *testing.T) {
	read := gqlAt("disposeAgentTaskFinding", "findingDisposition")
	write := gqlAt("disposeAgentTaskFinding")
	write.Extensions = map[string]any{"code": "TASK_NOT_FINISHED"}
	generic := gqlAt("disposeAgentTaskFinding")
	typed := &gqlerror.Error{Message: "Refused", Extensions: map[string]any{"code": "TASK_NOT_OWNER"}}
	readTyped := gqlAt("disposeAgentTaskFinding", "id")
	readTyped.Extensions = map[string]any{"code": "TASK_NOT_OWNER"}
	foreignTyped := gqlAt("somethingElse")
	foreignTyped.Extensions = map[string]any{"code": "TASK_NOT_OWNER"}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"write then readback":            {gqlerror.List{write, read}, true},
		"readback then write":            {gqlerror.List{read, write}, true},
		"typed pathless alone":           {gqlerror.List{typed}, true},
		"typed pathless beside readback": {gqlerror.List{read, typed}, false},
		"http wrapper kept":              {&graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{read, write}}}, true},
		"readback only":                  {gqlerror.List{read}, false},
		"root generic only":              {gqlerror.List{generic}, false},
		"generic then typed":             {gqlerror.List{generic, write}, true},
		"unrelated code":                 {gqlerror.List{{Message: "x", Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"}}}, false},
		"not graphql":                    {errors.New("boom"), false},
	} {
		t.Run(name, func(t *testing.T) {
			got := writeRefusal(tc.err)
			if (got != nil) != tc.want {
				t.Fatalf("writeRefusal = %v, want picked=%v", got, tc.want)
			}
			if got != nil {
				if list, _ := gqlErrorsOf(got); len(list) != 1 {
					t.Fatalf("must reduce to the single refusal, got %d errors", len(list))
				}
				var h *graphql.HTTPError
				if errors.As(tc.err, &h) {
					var h2 *graphql.HTTPError
					if !errors.As(got, &h2) || h2.StatusCode != h.StatusCode {
						t.Fatal("HTTP status not kept")
					}
				}
			}
		})
	}
}
