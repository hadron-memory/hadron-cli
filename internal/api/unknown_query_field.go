package api

import (
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"
)

// IsUnknownQueryField permits a read-only compatibility path only for an exact
// parsed unknown root-field rejection. Prose mentioning a field, mixed errors,
// resolver paths, data-partials and 5xx envelopes never qualify. Callers must
// also reject a populated typed result beside a 200 errors envelope.
func IsUnknownQueryField(err error, field string) bool {
	list := graphQLErrors(err)
	if len(list) == 0 {
		return false
	}
	// gqlerror.List.As itself traverses entries, so reject nulls before
	// asking errors.As for a different envelope type.
	for _, e := range list {
		if e == nil {
			return false
		}
	}
	var h *graphql.HTTPError
	if errors.As(err, &h) && (h.StatusCode != 400 || h.Response.Data != nil) {
		return false
	}
	prefix := `Cannot query field "` + field + `" on type "Query".`
	for _, e := range list {
		if len(e.Path) != 0 || !strings.HasPrefix(e.Message, prefix) {
			return false
		}
		code := extensionCode(e)
		if code != "" && code != "GRAPHQL_VALIDATION_FAILED" {
			return false
		}
	}
	return true
}
