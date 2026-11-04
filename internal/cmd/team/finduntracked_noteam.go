package team

// `find-untracked --no-team` (hadron-cli#1622; hadron-server#3422, cor:agt:120:03).
// The server's noTeam filter keeps the items carrying none of the integration's
// team labels. It rides SEPARATE request documents (the cli#1596 pattern): the
// three documents of finduntracked.go stay byte for byte what they were, so a
// server without the argument keeps validating a call that does not use it.
// Exactly one request is sent, never retried, never rewritten without the
// filter: a widened list or claim is a different set of items.

import (
	"context"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	untrackedListField  = "Query.untrackedTaskItems"
	untrackedClaimField = "Mutation.claimUntrackedTaskItems"
)

// noTeamUnsupported turns the ONE schema refusal that means "this server has no
// noTeam argument" into a plain statement. Only an unknown noTeam on the
// field this request named qualifies, with no other error beside it; any other
// failure, a validation one included, keeps the ordinary mapping, so a server
// that also lacks assignee/owner is reported as today, not relabelled.
func noTeamUnsupported(err error, field string) error {
	if api.IsUnknownGraphQLArgument(err, "noTeam", field) {
		return exitcode.Newf(exitcode.Error, "this server does not support --no-team (the noTeam argument needs hadron-server#3422); nothing was changed, and the CLI does not retry without the filter")
	}
	return api.WithServerCode(mapTaskError(err), err)
}

// runFindUntrackedNoTeam sends the no-team document that matches the flags.
func runFindUntrackedNoTeam(ctx context.Context, f *cmdutil.Factory, client graphql.Client, appRef, integration, repo string,
	ready *gen.ReadyFilter, skip, limit *int, assignToMe bool, assignee, owner *gen.AgentTaskPartyInput) error {
	switch {
	case assignToMe && (assignee != nil || owner != nil):
		resp, err := gen.ClaimUntrackedTaskItemsForPartiesNoTeam(ctx, client, appRef, integration, true, optStr(repo), ready, limit, assignee, owner)
		if err != nil {
			return noTeamUnsupported(err, untrackedClaimField)
		}
		r := resp.ClaimUntrackedTaskItems
		if r == nil {
			return writeClaimResult(f, nil)
		}
		tasks := make([]*gen.AgentTaskFields, 0, len(r.Tasks))
		for _, t := range r.Tasks {
			if t != nil {
				tasks = append(tasks, &t.AgentTaskFields)
			}
		}
		return writeClaimResult(f, &claimOutcome{tasks, r.Complete, r.IncompleteReason, r.RefusalCode})
	case assignToMe:
		resp, err := gen.ClaimUntrackedTaskItemsNoTeam(ctx, client, appRef, integration, true, optStr(repo), ready, limit)
		if err != nil {
			return noTeamUnsupported(err, untrackedClaimField)
		}
		r := resp.ClaimUntrackedTaskItems
		if r == nil {
			return writeClaimResult(f, nil)
		}
		tasks := make([]*gen.AgentTaskFields, 0, len(r.Tasks))
		for _, t := range r.Tasks {
			if t != nil {
				tasks = append(tasks, &t.AgentTaskFields)
			}
		}
		return writeClaimResult(f, &claimOutcome{tasks, r.Complete, r.IncompleteReason, r.RefusalCode})
	}
	resp, err := gen.UntrackedTaskItemsNoTeam(ctx, client, appRef, integration, true, optStr(repo), ready, skip, limit)
	if err != nil {
		return noTeamUnsupported(err, untrackedListField)
	}
	p := resp.UntrackedTaskItems
	if p == nil {
		return writeUntrackedPage(f, nil)
	}
	dto := &untrackedPageDTO{Items: []untrackedItemDTO{}, Total: p.Total, Complete: p.Complete, IncompleteReason: p.IncompleteReason}
	for _, it := range p.Items {
		if it == nil {
			continue
		}
		dto.Items = append(dto.Items, untrackedItemDTO{ExternalID: it.ExternalId, Repository: it.Repository, Number: it.Number,
			Title: it.Title, URL: it.Url, Labels: append([]string{}, it.Labels...), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
			LinkedPullRequestMerged: it.LinkedPullRequestMerged})
	}
	return writeUntrackedPage(f, dto)
}
