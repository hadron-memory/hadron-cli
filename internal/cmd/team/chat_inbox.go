package team

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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

// runInbox preserves complete-output-before-ack for the landed R57 self arm.
func runInbox(ctx context.Context, source inboxTransport, identity inboxIdentityReader, streams *output.IOStreams, jsonMode bool, ackOnly []string) error {
	scope, err := identity(ctx)
	if err != nil {
		return inboxFailure(err, "cannot read inbox binding")
	}
	if scope.AppID == "" || scope.WorkerID == "" || scope.SessionID == "" || scope.Server == "" {
		return inboxLocal("INBOX_UNBOUND")
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return inboxFailure(err, "inbox interrupted")
		}
		current, err := identity(ctx)
		if err != nil {
			return inboxFailure(err, "cannot recheck inbox binding")
		}
		if current != scope {
			return inboxLocal("INBOX_BINDING_CHANGED")
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return inboxFailure(err, "inbox interrupted")
	}
	if ackOnly != nil {
		if err := check(); err != nil {
			return err
		}
		if err := validateInboxProofs(ackOnly); err != nil {
			return err
		}
		result, err := source.Ack(ctx, scope, ackOnly)
		if err != nil {
			return inboxFailure(err, "inbox acknowledgment unconfirmed")
		}
		return writeInboxOutput(streams, jsonMode, result, func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "Acknowledged %d (%d already acknowledged).\n", result.Acknowledged, result.Unchanged)
			return err
		})
	}
	page, err := source.Read(ctx, scope)
	if err != nil {
		return inboxFailure(err, "cannot read inbox")
	}
	ids := make([]string, 0, len(page.Batches))
	if page.Batches == nil {
		page.Batches = []inboxBatch{}
	}
	for i := range page.Batches {
		batch := &page.Batches[i]
		if batch.WorkerID != scope.WorkerID || batch.ID == "" {
			return exitcode.Newf(exitcode.Forbidden, "inbox returned an invalid recipient or proof")
		}
		if batch.Ranges == nil {
			batch.Ranges = []inboxRange{}
		}
		for j := range batch.Ranges {
			if batch.Ranges[j].Seqs == nil {
				batch.Ranges[j].Seqs = []int{}
			}
		}
		ids = append(ids, batch.ID)
	}
	if len(ids) > 0 {
		if err := validateInboxProofs(ids); err != nil {
			return err
		}
	}
	if err := check(); err != nil {
		return err
	}
	if err := writeInboxOutput(streams, jsonMode, page, func(w io.Writer) error {
		if len(page.Batches) == 0 {
			if _, err := fmt.Fprintln(w, "No deliveries waiting in this page."); err != nil {
				return err
			}
		}
		for _, batch := range page.Batches {
			if _, err := fmt.Fprintln(w, batch.Text); err != nil {
				return err
			}
		}
		if page.PageInfo.HasNextPage {
			_, err := fmt.Fprintln(w, "More pages are available; use --json pageInfo.endCursor with --after.")
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if err := check(); err != nil {
		return inboxUnconfirmed(streams.ErrOut, jsonMode, err)
	}
	if _, err := source.Ack(ctx, scope, ids); err != nil {
		return inboxUnconfirmed(streams.ErrOut, jsonMode, err)
	}
	return nil
}

func validateInboxProofs(ids []string) error {
	if len(ids) == 0 || len(ids) > 200 {
		return exitcode.Newf(exitcode.Usage, "acknowledgment requires 1 to 200 batch proofs")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return exitcode.Newf(exitcode.Usage, "acknowledgment contains a duplicate batch proof")
		}
		seen[id] = true
		if id == "" {
			return exitcode.Newf(exitcode.Usage, "acknowledgment contains an empty batch proof")
		}
	}
	return nil
}

func writeInboxOutput(streams *output.IOStreams, jsonMode bool, v any, human func(io.Writer) error) error {
	if err := output.Write(streams, jsonMode, v, human); err != nil {
		return inboxFailure(err, "inbox output failed; acknowledgment was not attempted")
	}
	if flusher, ok := streams.Out.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return inboxFailure(err, "inbox output flush failed; acknowledgment was not attempted")
		}
	}
	return nil
}

// decodeInboxAckFile consumes caller-owned JSON only. The command adapter must
// refuse terminal stdin; proof values never belong on argv or in diagnostics.
func decodeInboxAckFile(r io.Reader) ([]string, error) {
	const limit = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || len(data) > limit {
		return nil, exitcode.Newf(exitcode.Usage, "cannot read acknowledgment input")
	}
	var input struct {
		BatchIDs []string `json:"batchIds"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "acknowledgment input must be an object containing batchIds")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, exitcode.Newf(exitcode.Usage, "acknowledgment input must contain one JSON object")
	}
	if err := validateInboxProofs(input.BatchIDs); err != nil {
		return nil, err
	}
	return input.BatchIDs, nil
}
