package comment

import (
	"context"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type graphqlService struct{ client graphql.Client }

func ConnectGraphQL(f *cmdutil.Factory) Connect {
	return func(context.Context) (Service, error) {
		client, err := f.GraphQLClient()
		if err != nil {
			return nil, err
		}
		return &graphqlService{client: client}, nil
	}
}
func missingResult() error {
	return exitcode.Newf(exitcode.Unavailable, "comment operation returned no result; outcome is unknown — read before retrying a write")
}
func (s *graphqlService) List(ctx context.Context, ref string, states []string, limit, offset int) (Page, error) {
	filter := make([]gen.CommentThreadState, len(states))
	for i, state := range states {
		filter[i] = gen.CommentThreadState(state)
	}
	r, err := gen.CommentThreads(ctx, s.client, ref, filter, limit, offset)
	if err != nil {
		return Page{}, api.MapError(err)
	}
	if r == nil || r.CommentThreads == nil {
		return Page{}, missingResult()
	}
	page := Page{Items: []Thread{}, Total: r.CommentThreads.Total}
	for _, item := range r.CommentThreads.Items {
		if item == nil || item.Root == nil {
			return Page{}, missingResult()
		}
		thread := Thread{Root: fromFields(item.Root.CommentFields), Replies: []Comment{}, ReplyCount: item.ReplyCount}
		for _, reply := range item.Replies {
			if reply == nil {
				return Page{}, missingResult()
			}
			thread.Replies = append(thread.Replies, fromFields(reply.CommentFields))
		}
		page.Items = append(page.Items, thread)
	}
	return page, nil
}
func (s *graphqlService) Get(ctx context.Context, ref string) (*Comment, error) {
	r, err := gen.GetComment(ctx, s.client, ref)
	if err != nil {
		return nil, api.MapError(err)
	}
	if r == nil {
		return nil, missingResult()
	}
	if r.Comment == nil {
		return nil, nil
	}
	c := fromFields(r.Comment.CommentFields)
	return &c, nil
}
func (s *graphqlService) Create(ctx context.Context, ref, body string, quote *string, anchor *int) (Comment, error) {
	r, err := gen.CreateComment(ctx, s.client, ref, body, quote, anchor)
	if err != nil {
		return Comment{}, api.MapError(err)
	}
	if r == nil || r.CreateComment == nil {
		return Comment{}, missingResult()
	}
	return fromFields(r.CreateComment.CommentFields), nil
}
func (s *graphqlService) Reply(ctx context.Context, ref, body string) (Comment, error) {
	r, err := gen.ReplyToComment(ctx, s.client, ref, body)
	if err != nil {
		return Comment{}, api.MapError(err)
	}
	if r == nil || r.ReplyToComment == nil {
		return Comment{}, missingResult()
	}
	return fromFields(r.ReplyToComment.CommentFields), nil
}
func (s *graphqlService) Edit(ctx context.Context, ref string, body, quote *string, expected int) (Comment, error) {
	r, err := gen.EditComment(ctx, s.client, ref, body, quote, expected)
	if err != nil {
		return Comment{}, api.MapError(err)
	}
	if r == nil || r.EditComment == nil {
		return Comment{}, missingResult()
	}
	return fromFields(r.EditComment.CommentFields), nil
}
func (s *graphqlService) Retract(ctx context.Context, ref string, expected int) (Comment, error) {
	r, err := gen.RetractComment(ctx, s.client, ref, expected)
	if err != nil {
		return Comment{}, api.MapError(err)
	}
	if r == nil || r.RetractComment == nil {
		return Comment{}, missingResult()
	}
	return fromFields(r.RetractComment.CommentFields), nil
}
func (s *graphqlService) Resolve(ctx context.Context, ref string, expected int, reopen bool) (Comment, error) {
	if reopen {
		r, err := gen.ReopenCommentThread(ctx, s.client, ref, expected)
		if err != nil {
			return Comment{}, api.MapError(err)
		}
		if r == nil || r.ReopenCommentThread == nil {
			return Comment{}, missingResult()
		}
		return fromFields(r.ReopenCommentThread.CommentFields), nil
	}
	r, err := gen.ResolveCommentThread(ctx, s.client, ref, expected)
	if err != nil {
		return Comment{}, api.MapError(err)
	}
	if r == nil || r.ResolveCommentThread == nil {
		return Comment{}, missingResult()
	}
	return fromFields(r.ResolveCommentThread.CommentFields), nil
}
func actor(a gen.CommentActorFields) *Actor {
	return &Actor{ID: a.Id, Name: a.Name, Handle: a.Handle, URN: a.Urn}
}
func fromFields(c gen.CommentFields) Comment {
	out := Comment{ID: c.Id, URN: c.Urn, PortalURL: c.PortalUrl, ThreadRootID: c.ThreadRootId, IsTopLevel: c.IsTopLevel, AnchorRevision: c.AnchorRevision, AnchorApprovalHash: c.AnchorApprovalHash, AnchorIsCurrent: c.AnchorIsCurrent, Quote: c.Quote, Body: c.Body, Retracted: c.Retracted, RetractedAt: c.RetractedAt, Hidden: c.Hidden, HiddenAt: c.HiddenAt, ResolvedAt: c.ResolvedAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Revision: c.RevSeq}
	if c.State != nil {
		state := string(*c.State)
		out.State = &state
	}
	if c.Target != nil {
		out.Target = Target{ID: c.Target.Id, URN: c.Target.Urn, Name: c.Target.Name, NodeType: c.Target.NodeType, Revision: c.Target.RevSeq}
	}
	if c.Author != nil {
		out.Author.Kind = string(c.Author.Kind)
		if c.Author.Worker != nil {
			out.Author.Worker = actor(c.Author.Worker.CommentActorFields)
		}
		if c.Author.User != nil {
			out.Author.User = actor(c.Author.User.CommentActorFields)
		}
		if c.Author.Agent != nil {
			out.Author.Agent = actor(c.Author.Agent.CommentActorFields)
		}
		if c.Author.App != nil {
			out.Author.App = actor(c.Author.App.CommentActorFields)
		}
	}
	if c.ProvenanceUser != nil {
		out.ProvenanceUser = actor(c.ProvenanceUser.CommentActorFields)
	}
	if c.ResolvedBy != nil {
		out.ResolvedBy = actor(c.ResolvedBy.CommentActorFields)
	}
	return out
}
