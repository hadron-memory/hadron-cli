package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#1485 S3 — the roster's one tolerated field error, at the unit level.

func advErr(path ast.Path, code any) *gqlerror.Error {
	e := &gqlerror.Error{Message: "raw message", Path: path}
	if code != nil {
		e.Extensions = map[string]any{"code": code}
	}
	return e
}

func groupsPath(i int) ast.Path {
	return ast.Path{ast.PathName("teamRoster"), ast.PathName("workers"), ast.PathIndex(i), ast.PathName("modelGroups")}
}

func TestRosterGroupAdvisories(t *testing.T) {
	allNull := func(int) bool { return true }
	got, ok := RosterGroupAdvisories(gqlerror.List{advErr(groupsPath(0), "A_CODE"), advErr(groupsPath(2), nil)}, 3, allNull)
	if !ok || got[0] != "A_CODE" || got[2] != "unknown" || len(got) != 2 {
		t.Errorf("got %v ok=%v", got, ok)
	}
	// A wrapped list is still the list (the client may wrap it).
	if _, ok := RosterGroupAdvisories(fmt.Errorf("x: %w", gqlerror.List{advErr(groupsPath(0), "A")}), 1, allNull); !ok {
		t.Error("a wrapped bare list must be tolerated")
	}

	bad := map[string]struct {
		err  error
		rows int
		null func(int) bool
	}{
		"nil":                {nil, 1, allNull},
		"empty list":         {gqlerror.List{}, 1, allNull},
		"nil entry":          {gqlerror.List{nil}, 1, allNull},
		"not a gql error":    {errors.New("boom"), 1, allNull},
		"out of range":       {gqlerror.List{advErr(groupsPath(1), "A")}, 1, allNull},
		"negative":           {gqlerror.List{advErr(groupsPath(-1), "A")}, 1, allNull},
		"value not null":     {gqlerror.List{advErr(groupsPath(0), "A")}, 1, func(int) bool { return false }},
		"one bad of two":     {gqlerror.List{advErr(groupsPath(0), "A"), advErr(ast.Path{ast.PathName("teamRoster"), ast.PathName("asOf")}, "B")}, 1, allNull},
		"string index":       {gqlerror.List{advErr(ast.Path{ast.PathName("teamRoster"), ast.PathName("workers"), ast.PathName("0"), ast.PathName("modelGroups")}, "A")}, 1, allNull},
		"index in name slot": {gqlerror.List{advErr(ast.Path{ast.PathIndex(0), ast.PathName("workers"), ast.PathIndex(0), ast.PathName("modelGroups")}, "A")}, 1, allNull},
		"no path":            {gqlerror.List{advErr(nil, "A")}, 1, allNull},
		// A non-200 response is a transport/server failure, never advisory, even
		// when its body parses to exactly the tolerated envelope.
		"http error": {&graphql.HTTPError{StatusCode: 502, Response: graphql.Response{Errors: gqlerror.List{advErr(groupsPath(0), "A")}}}, 1, allNull},
		"joined http error": {errors.Join(gqlerror.List{advErr(groupsPath(0), "A")},
			&graphql.HTTPError{StatusCode: 500, Response: graphql.Response{Errors: gqlerror.List{advErr(groupsPath(0), "A")}}}), 1, allNull},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			if got, ok := RosterGroupAdvisories(c.err, c.rows, c.null); ok {
				t.Errorf("must not be tolerated, got %v", got)
			}
		})
	}
}
