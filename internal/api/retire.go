package api

import (
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"
)

// IsRetireSpecUnsupported reports a server without the retireSpec door
// (hadron-server#3429): every error is the framework's own validation of the
// mutation field or its input type, so nothing ran.
func IsRetireSpecUnsupported(err error) bool {
	list := graphQLErrors(err)
	if len(list) == 0 {
		return false
	}
	for _, e := range list {
		if e == nil || len(e.Path) > 0 || extensionCode(e) != "GRAPHQL_VALIDATION_FAILED" {
			return false
		}
		if !strings.HasPrefix(e.Message, `Cannot query field "retireSpec" on type "Mutation".`) &&
			!strings.HasPrefix(e.Message, `Unknown type "RetireSpecInput".`) {
			return false
		}
	}
	return true
}

// IsRefusedBeforeCommit reports that the server answered a one-field mutation
// with a refusal: every error carries a code other than the internal fault, and
// names at most the mutation field itself. An error deeper in the selection is
// the server failing to render a result it had already produced, so it, an
// uncoded error and a lost answer all leave the outcome unknown.
func IsRefusedBeforeCommit(err error) bool {
	list := graphQLErrors(err)
	if len(list) == 0 {
		var httpErr *graphql.HTTPError
		return errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403)
	}
	for _, e := range list {
		if e == nil || len(e.Path) > 1 {
			return false
		}
		if c := extensionCode(e); c == "" || c == "INTERNAL_SERVER_ERROR" {
			return false
		}
	}
	return true
}

// WithCause presents one error and keeps another in the chain: the exit code and
// message are presentation's, and cause's server detail stays readable.
func WithCause(presentation, cause error) error {
	return &attestationCause{presentation: presentation, cause: cause}
}

// ErrorExtensions returns the extensions of the first error carrying code, nil
// when there is none.
func ErrorExtensions(err error, code string) map[string]any {
	for _, e := range graphQLErrors(err) {
		if e != nil && extensionCode(e) == code {
			return e.Extensions
		}
	}
	return nil
}
