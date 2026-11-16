package team

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// serverLacksProfile decides whether the CLI says "this server predates the S1b launcher
// profile" (a definite statement that nothing was written) or lets the real error through.
// Only a PRE-EXECUTION schema refusal of exactly the profile fields may produce it
// (hadron-cli#2176, from #2165's blind review): the helpers are unit-tested alone; this
// pins the combination, including the inputs the review named as unestablished.
func TestServerLacksProfileOnlyForAPreExecutionRefusalOfTheProfileFields2176(t *testing.T) {
	selection := func(field string) *gqlerror.Error {
		return &gqlerror.Error{
			Message:    `Cannot query field "` + field + `" on type "HostWorkerAssignment".`,
			Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
		}
	}
	input := func(field string) *gqlerror.Error {
		return &gqlerror.Error{
			Message:    `Variable "$input" got invalid value {}; Field "` + field + `" is not defined by type "AssignHostWorkerInput".`,
			Extensions: map[string]any{"code": "BAD_USER_INPUT"},
		}
	}
	forbidden := &gqlerror.Error{Message: "not the App's manager", Extensions: map[string]any{"code": "FORBIDDEN"}}
	execution := &gqlerror.Error{
		Message: "assignment failed", Path: ast.Path{ast.PathName("assignHostWorker")},
		Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"},
	}

	yes := map[string]error{
		"selection refused (effort)":      gqlerror.List{selection("effort")},
		"selection refused (both fields)": gqlerror.List{selection("effort"), selection("accountProfile")},
		"unknown input field (effort)":    gqlerror.List{input("effort")},
		"unknown input field (both)":      gqlerror.List{input("accountProfile"), input("effort")},
	}
	for name, err := range yes {
		if !serverLacksProfile(err) {
			t.Errorf("%s: the old-server refusal of the profile fields must be recognised", name)
		}
	}

	no := map[string]error{
		"nil":                            nil,
		"transport":                      errors.New(`dial tcp: Field "effort" is not defined by type "AssignHostWorkerInput"`),
		"empty list":                     gqlerror.List{},
		"forbidden":                      gqlerror.List{forbidden},
		"validation of another field":    gqlerror.List{selection("model")},
		"input refusal of another field": gqlerror.List{input("model")},
		"input field on another type":    gqlerror.List{{Message: `Field "effort" is not defined by type "SomethingElse".`, Extensions: map[string]any{"code": "BAD_USER_INPUT"}}},
		// A server answers in ONE phase: selection validation precedes variable coercion, so
		// one envelope never carries both kinds. If one ever did, the classifier stays
		// conservative (a refusal it cannot attribute is shown as the real error).
		"selection and input refusals in one envelope": gqlerror.List{selection("accountProfile"), input("effort")},
		"profile refusal mixed with other":             gqlerror.List{selection("effort"), selection("model")},
		// A mixed validation + execution envelope: something ran (or may have), so the
		// "nothing was written" sentence would be a claim the CLI cannot make.
		"profile refusal mixed with an execution error": gqlerror.List{input("effort"), execution},
		"profile refusal mixed with forbidden":          gqlerror.List{selection("effort"), forbidden},
		"input refusal carrying a resolver path": gqlerror.List{{
			Message: `Field "effort" is not defined by type "AssignHostWorkerInput".`, Path: ast.Path{ast.PathName("assignHostWorker")},
			Extensions: map[string]any{"code": "BAD_USER_INPUT"},
		}},
	}
	for name, err := range no {
		if serverLacksProfile(err) {
			t.Errorf("%s must NOT be reported as an old server that lacks the profile", name)
		}
	}
}
