package teamrouter

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// GraphQLServer is the production Server: the router's own typed reads
// (internal/api/queries/teamrouter.graphql) on the operator's login. Every
// method is a read; none takes or returns the adopted token except
// AttentionPage, which passes it to the server and never logs it.
type GraphQLServer struct {
	client  graphql.Client
	app     string
	channel string
}

// NewGraphQLServer reads app's attention for the team Channel channel (an id).
func NewGraphQLServer(client graphql.Client, app, channel string) *GraphQLServer {
	return &GraphQLServer{client: client, app: app, channel: channel}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// AttentionPage reads one page of the paged attention scan.
func (s *GraphQLServer) AttentionPage(ctx context.Context, req AttentionRequest) (AttentionPage, error) {
	resp, err := gen.RouterAttentionPage(ctx, s.client, s.app, optional(req.Since), optional(req.Page))
	if err != nil {
		return AttentionPage{}, err
	}
	p := resp.GetTeamAttentionPage()
	if p == nil {
		return AttentionPage{}, errors.New("malformed result")
	}
	out := AttentionPage{ScanComplete: p.GetScanComplete(), Items: []AttentionItem{}}
	if v := p.GetNextPage(); v != nil {
		out.NextPage = *v
	}
	if v := p.GetAdoptableSince(); v != nil {
		out.AdoptableSince = *v
	}
	for _, it := range p.GetItems() {
		if it == nil {
			continue
		}
		item := AttentionItem{Worker: it.GetWorker(), WorkerName: it.GetWorkerName(), Channel: it.GetChannel(),
			Unread: it.GetUnread(), UnreadMentions: it.GetUnreadMentions(), FirstUnreadSeq: it.GetFirstUnreadSeq(),
			LastSeq: it.GetLastSeq()}
		if v := it.GetLastRelevantSeq(); v != nil {
			item.LastRelevantSeq = *v
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// RegisterRow is the worker's winning register row on the team Channel, or
// the zero row (which never counts as mention-only) when it has none there.
func (s *GraphQLServer) RegisterRow(ctx context.Context, workerID string) (RegisterRow, error) {
	resp, err := gen.RouterRegister(ctx, s.client, workerID, s.app)
	if err != nil {
		return RegisterRow{}, err
	}
	for _, row := range resp.GetAttendeeRegister() {
		if row == nil || row.GetChannel() == nil || row.GetChannel().GetId() != s.channel {
			continue
		}
		out := RegisterRow{NamedBy: string(row.GetNamedBy()), ChannelID: row.GetChannel().GetId()}
		if e := row.GetEntry(); e != nil {
			out.MentionOnly = e.GetMentionOnly()
		}
		return out, nil
	}
	return RegisterRow{}, nil
}

// ReadCursor is the worker's read cursor on the team Channel. Any GraphQL
// error — including errors that arrive WITH data — is an error here, so the
// router reads it as unknown (#1829 P1-1).
func (s *GraphQLServer) ReadCursor(ctx context.Context, workerID string) (CursorResult, error) {
	resp, err := gen.RouterReadCursor(ctx, s.client, workerID, s.app, s.channel)
	if err != nil {
		return CursorResult{}, err
	}
	rs := resp.GetChannelReadState()
	if rs == nil {
		return CursorResult{}, nil
	}
	v := rs.GetLastSeenSeq()
	return CursorResult{LastSeen: &v}, nil
}

// ChannelHead is the Channel's watermark.
func (s *GraphQLServer) ChannelHead(ctx context.Context, channelRef string) (int, error) {
	resp, err := gen.RouterChannelHead(ctx, s.client, channelRef)
	if err != nil {
		return 0, err
	}
	if resp.GetChannel() == nil {
		return 0, errors.New("channel not found")
	}
	return resp.GetChannel().GetLastSeq(), nil
}
