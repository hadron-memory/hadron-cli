package node

import (
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Only a parsed older-schema refusal may select the old read. Mixed errors,
// transport failures and populated partials are not capability evidence.
func isUnknownContentValidationProjection(err error) bool {
	if !api.IsGraphQLValidationOnlyFor(err, "authorCheck", "NodeContentValidationAuthorCheck",
		"mintStatus", "NodeMintStatus", "approvalStatus", "NodeApprovalStatus", "NodeApproval",
		"authorship", "contentValidation", "NodeAuthorship", "NodeContentValidationStatus", "revision", "nodeBatch") {
		return false
	}
	var h *graphql.HTTPError
	var list gqlerror.List
	if errors.As(err, &h) {
		if h.StatusCode != 400 || h.Response.Data != nil {
			return false
		}
		list = h.Response.Errors
	} else if !errors.As(err, &list) {
		var single *gqlerror.Error
		if !errors.As(err, &single) {
			return false
		}
		list = gqlerror.List{single}
	}
	for _, e := range list {
		if e == nil || len(e.Path) != 0 {
			return false
		}
	}
	return true
}
