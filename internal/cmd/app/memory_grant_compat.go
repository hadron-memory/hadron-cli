package app

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Only an explicit validation refusal permits the second document. No network,
// runtime, partial-result or auth error is retried. Legacy metadata is unknown.
func legacyGrantFields(g gen.AppMemoryGrantLegacyFields) gen.AppMemoryGrantFields {
	return gen.AppMemoryGrantFields{Id: g.Id, AppId: g.AppId, MemoryUrn: g.MemoryUrn, Prefix: g.Prefix, RegisterNodeUrn: g.RegisterNodeUrn, GrantedBy: g.GrantedBy, CreatedAt: g.CreatedAt, RevokedAt: g.RevokedAt, RevokedBy: g.RevokedBy}
}

func setMemoryGrant(ctx context.Context, c graphql.Client, app, memory, prefix string, citation *string) (*gen.SetAppMemoryWriteGrantResponse, error) {
	r, err := gen.SetAppMemoryWriteGrant(ctx, c, app, memory, prefix, citation)
	if !api.MemoryGrantFieldsUnavailable(err) || (r != nil && r.SetAppMemoryWriteGrant != nil) {
		return r, err
	}
	old, err := gen.SetAppMemoryWriteGrantLegacy(ctx, c, app, memory, prefix, citation)
	if err != nil || old == nil {
		return nil, err
	}
	result := &gen.SetAppMemoryWriteGrantResponse{}
	if old.SetAppMemoryWriteGrant != nil {
		result.SetAppMemoryWriteGrant = &gen.SetAppMemoryWriteGrantSetAppMemoryWriteGrant{AppMemoryGrantFields: legacyGrantFields(old.SetAppMemoryWriteGrant.AppMemoryGrantLegacyFields)}
	}
	return result, nil
}

func memoryGrants(ctx context.Context, c graphql.Client, ref string, revoked *bool, i *gen.ActInstructionInput) (*gen.AppMemoryWriteGrantsResponse, error) {
	r, err := memoryGrantsCurrent(ctx, c, ref, revoked, i)
	if !api.MemoryGrantFieldsUnavailable(err) || (r != nil && r.AppMemoryWriteGrants != nil) {
		return r, err
	}
	var fields []*gen.AppMemoryGrantLegacyFields
	if i == nil {
		old, e := gen.AppMemoryWriteGrantsLegacy(ctx, c, ref, revoked)
		if e != nil || old == nil {
			return nil, e
		}
		if old.AppMemoryWriteGrants == nil {
			return &gen.AppMemoryWriteGrantsResponse{}, nil
		}
		for _, row := range old.AppMemoryWriteGrants {
			if row == nil {
				fields = append(fields, nil)
			} else {
				fields = append(fields, &row.AppMemoryGrantLegacyFields)
			}
		}
	} else {
		old, e := gen.AppMemoryWriteGrantsInstructedLegacy(ctx, c, i, ref, revoked)
		if e != nil || old == nil {
			return nil, e
		}
		if old.AppMemoryWriteGrants == nil {
			return &gen.AppMemoryWriteGrantsResponse{}, nil
		}
		for _, row := range old.AppMemoryWriteGrants {
			if row == nil {
				fields = append(fields, nil)
			} else {
				fields = append(fields, &row.AppMemoryGrantLegacyFields)
			}
		}
	}
	rows := make([]*gen.AppMemoryWriteGrantsRows1595, 0, len(fields))
	for _, g := range fields {
		if g == nil {
			rows = append(rows, nil)
		} else {
			rows = append(rows, &gen.AppMemoryWriteGrantsRows1595{AppMemoryGrantFields: legacyGrantFields(*g)})
		}
	}
	return &gen.AppMemoryWriteGrantsResponse{AppMemoryWriteGrants: rows}, nil
}

func revokeMemoryGrant(ctx context.Context, c graphql.Client, id string, i *gen.ActInstructionInput) (*gen.RevokeAppMemoryWriteGrantResponse, error) {
	r, err := revokeMemoryGrantCurrent(ctx, c, id, i)
	if !api.MemoryGrantFieldsUnavailable(err) || (r != nil && r.RevokeAppMemoryWriteGrant != nil) {
		return r, err
	}
	var g *gen.AppMemoryGrantLegacyFields
	if i == nil {
		old, e := gen.RevokeAppMemoryWriteGrantLegacy(ctx, c, id)
		if e != nil || old == nil {
			return nil, e
		}
		if old.RevokeAppMemoryWriteGrant != nil {
			g = &old.RevokeAppMemoryWriteGrant.AppMemoryGrantLegacyFields
		}
	} else {
		old, e := gen.RevokeAppMemoryWriteGrantInstructedLegacy(ctx, c, id, i)
		if e != nil || old == nil {
			return nil, e
		}
		if old.RevokeAppMemoryWriteGrant != nil {
			g = &old.RevokeAppMemoryWriteGrant.AppMemoryGrantLegacyFields
		}
	}
	result := &gen.RevokeAppMemoryWriteGrantResponse{}
	if g != nil {
		result.RevokeAppMemoryWriteGrant = &gen.RevokeAppMemoryWriteGrantRow1595{AppMemoryGrantFields: legacyGrantFields(*g)}
	}
	return result, nil
}
