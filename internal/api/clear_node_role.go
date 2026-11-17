package api

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// UpdateNodeClearingRoleByKind applies one role-null update, with all other
// explicit edits in the SAME mutation. A nil Role on the ordinary input means
// preserve, so use an empty role only in this local copy for routing the
// resulting ungoverned state; it is never sent on the wire. Server refusal is
// final: do not retry via an ordinary or generic writer.
func UpdateNodeClearingRoleByKind(ctx context.Context, client graphql.Client, input *gen.UpdateNodeInput, cur NodeKindState) (*AuthoredNode, error) {
	// The refreshed provider input adds an install alternative (#2044). The
	// dedicated clear documents still take node refs, not that alternative.
	// Refuse an unsupported supplied field rather than silently dropping it;
	// existing CLI callers leave it nil and keep their exact operation shape.
	if input.Install != nil {
		return nil, exitcode.Newf(exitcode.Usage, "role clearing with an install target is not supported; use a node reference")
	}
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
		var node *gen.UpdateTaskNodeClearingRoleUpdateTaskNode
		var err error
		if input.AuthoredWith == nil {
			resp, e := gen.UpdateTaskNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateTaskNode
			}
		} else {
			resp, e := gen.UpdateTaskNodeClearingRoleAuthored(ctx, client, input.AuthoredWith, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateTaskNode
			}
		}
		if err != nil {
			return nil, err
		}
		if node == nil {
			return nil, errors.New("updateTaskNode returned no node")
		}
		return authoredNodeFrom(node), nil
	case kindSpec:
		var node *gen.UpdateSpecNodeClearingRoleUpdateSpecNode
		var err error
		if input.AuthoredWith == nil {
			resp, e := gen.UpdateSpecNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateSpecNode
			}
		} else {
			resp, e := gen.UpdateSpecNodeClearingRoleAuthored(ctx, client, input.AuthoredWith, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateSpecNode
			}
		}
		if err != nil {
			return nil, err
		}
		if node == nil {
			return nil, errors.New("updateSpecNode returned no node")
		}
		return authoredNodeFrom(node), nil
	case kindReview:
		var node *gen.UpdateReviewNodeClearingRoleUpdateReviewNode
		var err error
		if input.AuthoredWith == nil {
			resp, e := gen.UpdateReviewNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateReviewNode
			}
		} else {
			resp, e := gen.UpdateReviewNodeClearingRoleAuthored(ctx, client, input.AuthoredWith, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateReviewNode
			}
		}
		if err != nil {
			return nil, err
		}
		if node == nil {
			return nil, errors.New("updateReviewNode returned no node")
		}
		return authoredNodeFrom(node), nil
	default:
		var node *gen.UpdateNodeClearingRoleUpdateNode
		var err error
		if input.AuthoredWith == nil {
			resp, e := gen.UpdateNodeClearingRole(ctx, client, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateNode
			}
		} else {
			resp, e := gen.UpdateNodeClearingRoleAuthored(ctx, client, input.AuthoredWith, input.Abstract, input.AiAgent, input.Alias, input.Content, input.ContentType, input.Data, input.Description, input.Edges, input.ExpectedRevision, input.Id, input.IsRunnable, input.LlmModel, input.Loc, input.MemoryId, input.Name, input.NodeType, input.ObjectType, input.OwnerRepo, input.Properties, input.Reason, input.Seq, input.Tags)
			err = e
			if resp != nil {
				node = resp.UpdateNode
			}
		}
		if err != nil {
			return nil, err
		}
		if node == nil {
			return nil, errors.New("updateNode returned no node")
		}
		return authoredNodeFrom(node), nil
	}
}
