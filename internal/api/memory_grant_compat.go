package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Khan/genqlient/graphql"
)

// MemoryGrantFieldsUnavailable accepts only a complete pre-execution unknown-
// field rejection for the #4031 projection. Runtime errors, mixed envelopes,
// field errors with a path, unparsed responses and other HTTP failures must
// never authorize replaying a mutation with an older document.
func MemoryGrantFieldsUnavailable(err error) bool {
	var transport *graphql.HTTPError
	if errors.As(err, &transport) && (transport.StatusCode != http.StatusBadRequest || transport.Response.Data != nil) {
		return false
	}
	list := graphQLErrors(err)
	if len(list) == 0 {
		return false
	}
	for _, e := range list {
		if e == nil || len(e.Path) != 0 || extensionCode(e) != "GRAPHQL_VALIDATION_FAILED" {
			return false
		}
		matched := false
		for _, field := range []string{"notEffective", "notEffectiveReason"} {
			prefix := `Cannot query field "` + field + `" on type "AppMemoryWriteGrant".`
			if e.Message == prefix || strings.HasPrefix(e.Message, prefix+" Did you mean ") {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
