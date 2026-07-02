package api

import (
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// MapError converts transport and GraphQL errors into CodedErrors so
// the root command can derive the documented exit code. Codes come
// from hadron-server's Apollo resolvers (extensions.code).
func MapError(err error) error {
	if err == nil {
		return nil
	}

	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 401:
			return exitcode.New(exitcode.AuthRequired, err)
		case 403:
			return exitcode.New(exitcode.Error, err)
		case 404:
			return exitcode.New(exitcode.NotFound, err)
		}
		return exitcode.New(exitcode.Error, err)
	}

	var list gqlerror.List
	if errors.As(err, &list) && len(list) > 0 {
		return exitcode.New(codeForExtension(extensionCode(list[0])), err)
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return exitcode.New(codeForExtension(extensionCode(gqlErr)), err)
	}

	return exitcode.New(exitcode.Error, err)
}

// HasErrorCode reports whether err carries a GraphQL error whose
// extensions.code equals code. It inspects the raw genqlient error (call it
// BEFORE MapError wraps the error into a CodedError) so callers can branch on
// a specific server error — e.g. `node import` falling back from updateNode's
// NODE_NOT_FOUND to createNode.
func HasErrorCode(err error, code string) bool {
	var list gqlerror.List
	if errors.As(err, &list) {
		for _, e := range list {
			if extensionCode(e) == code {
				return true
			}
		}
		return false
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return extensionCode(gqlErr) == code
	}
	return false
}

func extensionCode(e *gqlerror.Error) string {
	if e == nil || e.Extensions == nil {
		return ""
	}
	if code, ok := e.Extensions["code"].(string); ok {
		return code
	}
	return ""
}

func codeForExtension(code string) int {
	switch {
	case code == "UNAUTHENTICATED":
		return exitcode.AuthRequired
	case code == "NOT_FOUND" || strings.HasSuffix(code, "_NOT_FOUND"):
		return exitcode.NotFound
	case code == "BAD_USER_INPUT" || code == "GRAPHQL_VALIDATION_FAILED":
		return exitcode.Usage
	case code == "CONFLICT" || strings.HasPrefix(code, "DUPLICATE_"):
		return exitcode.Conflict
	default:
		return exitcode.Error
	}
}
