package team

import (
	"context"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Keep the unfiltered document for old servers. Explicit filters never retry
// without the filter: an apparently complete but wider answer is incorrect.
func sessionPage1337(ctx context.Context, client graphql.Client, repo, worker, customer, typ *string, limit, offset *int) (*gen.TeamSessionsPageResponse, error) {
	if customer == nil && typ == nil {
		return gen.TeamSessionsPage(ctx, client, repo, worker, limit, offset)
	}
	r, err := gen.TeamSessionsPageFiltered(ctx, client, repo, customer, typ, worker, limit, offset)
	if err != nil {
		return nil, err
	}
	if r == nil || r.SessionsPage == nil {
		return nil, fmt.Errorf("server returned no sessions page")
	}
	p := &gen.TeamSessionsPageSessionsPageSessionPage{Total: r.SessionsPage.Total, HasMore: r.SessionsPage.HasMore, Items: []*gen.TeamSessionsPageSessionsPageSessionPageItemsSession{}}
	for _, s := range r.SessionsPage.Items {
		if s != nil {
			p.Items = append(p.Items, &gen.TeamSessionsPageSessionsPageSessionPageItemsSession{TeamSessionFields: s.TeamSessionFields})
		}
	}
	return &gen.TeamSessionsPageResponse{SessionsPage: p}, nil
}
