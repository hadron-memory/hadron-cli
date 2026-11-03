package api

import (
	"errors"
	"regexp"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// unknownAdvisoryCode is shown instead of an error code that is missing, not a
// string, or not a plain token — never the server's message or extensions.
const unknownAdvisoryCode = "unknown"

var safeAdvisoryCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// RosterGroupAdvisories reports whether err is NOTHING BUT advisory field
// errors on the team roster's per-row model groups (cli#1485 S3, cor:agt:060:12),
// and if so returns the safe error code per affected row index.
//
// It is the one place a GraphQL field error is tolerated, and it is deliberately
// narrow. Every error must be a bare GraphQL error (never a transport or non-200
// failure, which carry no data to trust) whose path is exactly the unaliased
// response path teamRoster.workers[i].modelGroups for a row i that exists
// (0 <= i < rows) and whose membership is actually null (nullAt) — an error on
// any other field, a mixed list, a missing or malformed path, an out-of-range
// index, or an error next to a non-null value all return ok=false, and the
// caller keeps its whole-command failure. The caller must also have a
// successfully decoded, non-null core roster (it supplies rows/nullAt from it).
//
// The returned code is extensions.code only when it is a plain token, else a
// fixed placeholder: raw message text and extensions are never surfaced.
func RosterGroupAdvisories(err error, rows int, nullAt func(i int) bool) (map[int]string, bool) {
	if err == nil {
		return nil, false
	}
	list, ok := bareGraphQLList(err)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make(map[int]string, len(list))
	for _, e := range list {
		if e == nil || len(e.Path) != 4 {
			return nil, false
		}
		root, ok0 := e.Path[0].(ast.PathName)
		workers, ok1 := e.Path[1].(ast.PathName)
		idx, ok2 := e.Path[2].(ast.PathIndex)
		field, ok3 := e.Path[3].(ast.PathName)
		if !ok0 || !ok1 || !ok2 || !ok3 || root != "teamRoster" || workers != "workers" || field != "modelGroups" {
			return nil, false
		}
		i := int(idx)
		if i < 0 || i >= rows || !nullAt(i) {
			return nil, false
		}
		code := extensionCode(e)
		if !safeAdvisoryCode.MatchString(code) {
			code = unknownAdvisoryCode
		}
		out[i] = code
	}
	return out, true
}

// bareGraphQLList returns the GraphQL error list err is made of, and ok only
// when err is that list (possibly wrapped) and nothing else: a non-200 HTTPError
// or any other joined/wrapped failure alongside it is not. It walks the chain by
// type rather than with errors.As, because gqlerror.List.As dereferences every
// entry and panics on a null one (`"errors":[null]`).
func bareGraphQLList(err error) (gqlerror.List, bool) {
	for e := err; e != nil; {
		switch x := e.(type) {
		case gqlerror.List:
			return x, true
		case *graphql.HTTPError:
			return nil, false
		case interface{ Unwrap() []error }:
			return nil, false
		default:
			e = errors.Unwrap(e)
		}
	}
	return nil, false
}
