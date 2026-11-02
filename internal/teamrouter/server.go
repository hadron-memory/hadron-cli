package teamrouter

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// GraphQLServer is the router's typed reads (internal/api/queries/
// teamrouter.graphql) on the operator's login. Every method is a read. The
// router that polled attention through it is retired (cli#935); RegisterRow
// still serves `team worker onboard`.
type GraphQLServer struct {
	client  graphql.Client
	app     string
	channel string
}

// NewGraphQLServer reads app's state for the team Channel channel (an id).
func NewGraphQLServer(client graphql.Client, app, channel string) *GraphQLServer {
	return &GraphQLServer{client: client, app: app, channel: channel}
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

// RegisterRow is the WINNING attendeeRegister row for the team Channel
// (App over organization; Worker over Agent over everyone).
type RegisterRow struct {
	NamedBy     string `json:"namedBy"`
	ChannelID   string `json:"channel"`
	MentionOnly bool   `json:"mentionOnly"`
	OwnerType   string `json:"ownerType,omitempty"`
}

// CursorResult is a worker's channelReadState (#1826). FieldErrs carries
// GraphQL errors that arrived WITH data: an integer alongside errors is not a
// verdict (#1829 P1-1).
type CursorResult struct {
	LastSeen  *int
	FieldErrs []string
}
