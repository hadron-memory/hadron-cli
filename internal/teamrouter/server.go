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

// GraphQLWakeServer is the production WakeServer: dueWakes and
// acknowledgeWakes on a client authenticated with the router's App key.
// The client must never attach a worker session (api.WithSession): an App
// key bound to a session is refused APP_KEY_SESSION_BOUND.
type GraphQLWakeServer struct {
	client graphql.Client
	app    string
}

// NewGraphQLWakeServer reads app's due wakes through client.
func NewGraphQLWakeServer(client graphql.Client, app string) *GraphQLWakeServer {
	return &GraphQLWakeServer{client: client, app: app}
}

// DueWakes reads one page of due wakes for workerRefs.
func (s *GraphQLWakeServer) DueWakes(ctx context.Context, workerRefs []string, after string) (WakePage, error) {
	resp, err := gen.RouterDueWakes(ctx, s.client, s.app, workerRefs, optional(after))
	if err != nil {
		return WakePage{}, err
	}
	p := resp.GetDueWakes()
	if p == nil {
		return WakePage{}, errors.New("malformed result")
	}
	out := WakePage{Status: string(p.GetStatus()), Withheld: p.GetWithheld(), Dormant: []DormantWorker{}, Wakes: []Wake{}}
	if pi := p.GetPageInfo(); pi != nil {
		out.HasNext = pi.GetHasNextPage()
		if c := pi.GetEndCursor(); c != nil {
			out.EndCursor = *c
		}
	}
	for _, d := range p.GetDormant() {
		if d == nil || d.GetWorker() == nil {
			continue
		}
		out.Dormant = append(out.Dormant, DormantWorker{Worker: d.GetWorker().GetId(), Name: d.GetWorker().GetName(), Mentions: d.GetMentions()})
	}
	for _, w := range p.GetWakes() {
		if w == nil || w.GetWorker() == nil {
			continue
		}
		wake := Wake{Worker: w.GetWorker().GetId(), WorkerName: w.GetWorker().GetName(), Unread: w.GetUnread(),
			UnreadMentions: w.GetUnreadMentions(), Proof: w.GetProof()}
		for _, c := range w.GetChannels() {
			if c == nil || c.GetChannel() == nil {
				continue
			}
			wake.Channels = append(wake.Channels, WakeChannel{ID: c.GetChannel().GetId(), Name: c.GetChannel().GetName(),
				FromSeq: c.GetFromSeq(), ThroughSeq: c.GetThroughSeq(), LatestMentionSeq: c.GetLatestMentionSeq()})
		}
		for _, r := range w.GetReasons() {
			if r == nil {
				continue
			}
			reason := WakeReason{Kind: string(r.GetKind())}
			if t := r.GetTicker(); t != nil {
				reason.Ticker = t.GetName()
			}
			if t := r.GetTask(); t != nil {
				reason.Task = &WakeTask{Ref: t.GetRef(), Title: t.GetTitle(), Event: string(t.GetEvent())}
			}
			wake.Reasons = append(wake.Reasons, reason)
		}
		out.Wakes = append(out.Wakes, wake)
	}
	return out, nil
}

// AcknowledgeWakes hands back delivered wakes' proofs.
func (s *GraphQLWakeServer) AcknowledgeWakes(ctx context.Context, proofs []string) (AckResult, error) {
	resp, err := gen.RouterAcknowledgeWakes(ctx, s.client, proofs)
	if err != nil {
		return AckResult{}, err
	}
	r := resp.GetAcknowledgeWakes()
	if r == nil {
		return AckResult{}, errors.New("malformed result")
	}
	return AckResult{Acknowledged: r.GetAcknowledged(), Unchanged: r.GetUnchanged()}, nil
}
