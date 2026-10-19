package api

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// UpdateNodeClearingRoleByKind applies one role-null update, with all other
// explicit edits in the SAME mutation. A nil Role on the ordinary input means
// preserve, so use an empty role only in this local copy for routing the
// resulting ungoverned state; it is never sent on the wire. Server refusal is
// final: do not retry via an ordinary or generic writer.
func UpdateNodeClearingRoleByKind(ctx context.Context, client graphql.Client, input *gen.UpdateNodeInput, cur NodeKindState) (*AuthoredNode, error) {
	routing := *input
	empty := ""
	routing.Role = &empty
	touched := touchedKinds(&routing, cur)
	if err := refuseTouched(touched); err != nil {
		return nil, err
	}
	kind := ""
	if len(touched) == 1 {
		kind = touched[0]
	}
	switch kind {
	case kindTask:
		resp, err := gen.UpdateTaskNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.UpdateTaskNode == nil {
			return nil, errors.New("updateTaskNode returned no node")
		}
		return authoredNodeFrom(resp.UpdateTaskNode), nil
	case kindSpec:
		resp, err := gen.UpdateSpecNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.UpdateSpecNode == nil {
			return nil, errors.New("updateSpecNode returned no node")
		}
		return authoredNodeFrom(resp.UpdateSpecNode), nil
	case kindReview:
		resp, err := gen.UpdateReviewNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.UpdateReviewNode == nil {
			return nil, errors.New("updateReviewNode returned no node")
		}
		return authoredNodeFrom(resp.UpdateReviewNode), nil
	default:
		resp, err := gen.UpdateNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.UpdateNode == nil {
			return nil, errors.New("updateNode returned no node")
		}
		return authoredNodeFrom(resp.UpdateNode), nil
	}
}
