package api

import (
	"context"
	"encoding/json"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// UpdateNodeDataWithClaim preserves the old document when there is no explicit
// claim. An omitted variable cannot hide an unknown GraphQL argument from an
// older server's validator. Never fall back after an attested write fails.
func UpdateNodeDataWithClaim(ctx context.Context, client graphql.Client, claim *gen.AuthoredWithInput, ref string, data json.RawMessage, reason *string) (*gen.UpdateNodeDataUpdateNodeDataNode, error) {
	if claim == nil {
		r, e := gen.UpdateNodeData(ctx, client, ref, data, reason)
		if e != nil {
			return nil, e
		}
		return r.UpdateNodeData, nil
	}
	r, e := gen.UpdateNodeDataAuthored(ctx, client, claim, ref, data, reason)
	if e != nil {
		return nil, e
	}
	return r.UpdateNodeData, nil
}

// UpdateNodePropertiesWithClaim selects the explicit-only attested document.
func UpdateNodePropertiesWithClaim(ctx context.Context, client graphql.Client, claim *gen.AuthoredWithInput, ref string, properties json.RawMessage, reason *string) (*gen.UpdateNodePropertiesUpdateNodePropertiesNode, error) {
	if claim == nil {
		r, e := gen.UpdateNodeProperties(ctx, client, ref, properties, reason)
		if e != nil {
			return nil, e
		}
		return r.UpdateNodeProperties, nil
	}
	r, e := gen.UpdateNodePropertiesAuthored(ctx, client, claim, ref, properties, reason)
	if e != nil {
		return nil, e
	}
	return r.UpdateNodeProperties, nil
}
