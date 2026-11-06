package bootpreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// ValidationRead captures the original envelope before typed unmarshalling.
// Any partial data, path, mixed error, wrong type or non-validation
// failure excludes the one documented legacy fallback. This is local to the
// two opt-in preview operations, never a shared retry policy.
type ValidationRead struct {
	graphql.Client
	operation     string
	data          bool
	invalidErrors bool
}

func (c *ValidationRead) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.operation = req.OpName
	var raw json.RawMessage
	captured := graphql.Response{Data: &raw}
	observedCtx, shape := api.WithGraphQLErrorShape(ctx)
	err := c.Client.MakeRequest(observedCtx, req, &captured)
	c.invalidErrors = shape.HadNullEntry()
	// Both absent and explicit null carry no data values. Any nonnull
	// JSON shape excludes fallback, even an empty object.
	c.data = len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	resp.Errors = captured.Errors
	resp.Extensions = captured.Extensions
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		return json.Unmarshal(raw, resp.Data)
	}
	return nil
}

func (c *ValidationRead) MissingField(err error, operation, typ string) bool {
	allowed := (operation == "AppAgentEffectiveRoster" && typ == "AppAgent") || (operation == "GetWorkerBootPromptServe" && typ == "Worker")
	if !allowed {
		return false
	}
	if c.operation != operation || c.data || c.invalidErrors {
		return false
	}
	var list gqlerror.List
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode != 400 || httpErr.Response.Data != nil {
			return false
		}
		list = httpErr.Response.Errors
	} else if !errors.As(err, &list) {
		return false
	}
	if len(list) == 0 {
		return false
	}
	plain := `Cannot query field "bootPromptServe" on type "` + typ + `".`
	checked := plain + ` Did you mean "bootPromptRef"?`
	for _, e := range list {
		if e == nil || len(e.Path) > 0 || e.Extensions["code"] != "GRAPHQL_VALIDATION_FAILED" || (e.Message != plain && e.Message != checked) {
			return false
		}
	}
	return true
}
