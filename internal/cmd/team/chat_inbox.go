package team

import (
	"context"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type inboxIdentity struct{ AppID, WorkerID, SessionID, Server string }
type inboxIdentityReader func(context.Context) (inboxIdentity, error)

type inboxRange struct {
	ChannelID   string `json:"channelId"`
	Incarnation int    `json:"incarnation"`
	FromSeq     int    `json:"fromSeq"`
	ThroughSeq  int    `json:"throughSeq"`
	Seqs        []int  `json:"seqs"`
}
type inboxBatch struct {
	ID           string       `json:"id"`
	Worker       string       `json:"worker"`
	WorkerID     string       `json:"workerId"`
	Text         string       `json:"text"`
	DeliverAfter string       `json:"deliverAfter"`
	BusyDeadline string       `json:"busyDeadline"`
	Interrupt    bool         `json:"interrupt"`
	Overflow     int          `json:"overflow"`
	Ranges       []inboxRange `json:"ranges"`
}
type inboxPageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}
type inboxPage struct {
	Batches  []inboxBatch  `json:"batches"`
	PageInfo inboxPageInfo `json:"pageInfo"`
}
type inboxAck struct {
	Acknowledged int `json:"acknowledged"`
	Unchanged    int `json:"unchanged"`
}

// The landed R57 adapter must authenticate scope.SessionID on both calls via
// api.WithSession, and supply neither host credentials nor attribution fields.
// Through is deliberately absent: this consumer acknowledges complete output.
type inboxTransport interface {
	Read(context.Context, inboxIdentity) (inboxPage, error)
	Ack(context.Context, inboxIdentity, []string) (inboxAck, error)
}

// runInbox is not wired to a production command until the R57 server arm lands.
func runInbox(ctx context.Context, source inboxTransport, identity inboxIdentityReader, streams *output.IOStreams, jsonMode bool, ackOnly []string) error {
	return nil
}
