package team

import (
	"context"
	"net/url"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// A self consumer uses the ordinary caller credential with the exact bound
// session on BOTH calls; the hostServer intentionally remains session-free.
type sessionInbox struct {
	factory *cmdutil.Factory
	after   string
}

func (s *sessionInbox) Read(ctx context.Context, scope inboxIdentity) (inboxPage, error) {
	c, err := s.factory.GraphQLClient()
	if err != nil {
		return inboxPage{}, err
	}
	r, err := gen.SessionInbox(api.WithSession(ctx, scope.SessionID), c, scope.AppID, []string{scope.WorkerID}, optStr(s.after))
	if err != nil {
		return inboxPage{}, err
	}
	if r == nil || r.DeliveryBatches == nil || r.DeliveryBatches.PageInfo == nil {
		return inboxPage{}, exitcode.Newf(exitcode.Unavailable, "inbox returned no page")
	}
	p := r.DeliveryBatches
	out := inboxPage{Batches: []inboxBatch{}, PageInfo: inboxPageInfo{HasNextPage: p.PageInfo.HasNextPage, EndCursor: p.PageInfo.EndCursor}}
	if out.PageInfo.HasNextPage && (out.PageInfo.EndCursor == nil || *out.PageInfo.EndCursor == "" || *out.PageInfo.EndCursor == s.after) {
		return inboxPage{}, exitcode.Newf(exitcode.Unavailable, "inbox returned no new cursor")
	}
	for _, b := range p.Batches {
		if b == nil || b.Worker == nil {
			return inboxPage{}, exitcode.Newf(exitcode.Unavailable, "inbox returned an invalid batch")
		}
		v := inboxBatch{ID: b.Id, Worker: b.Worker.Name, WorkerID: b.Worker.Id, Text: b.Text, DeliverAfter: b.DeliverAfter, BusyDeadline: b.BusyDeadline, Interrupt: b.Interrupt, Overflow: b.Overflow, Ranges: []inboxRange{}}
		for _, r := range b.Ranges {
			if r == nil || r.Channel == nil {
				return inboxPage{}, exitcode.Newf(exitcode.Unavailable, "inbox returned an invalid range")
			}
			v.Ranges = append(v.Ranges, inboxRange{ChannelID: r.Channel.Id, Incarnation: r.Incarnation, FromSeq: r.FromSeq, ThroughSeq: r.ThroughSeq, Seqs: append([]int{}, r.Seqs...)})
		}
		out.Batches = append(out.Batches, v)
	}
	return out, nil
}
func (s *sessionInbox) Ack(ctx context.Context, scope inboxIdentity, ids []string) (inboxAck, error) {
	c, err := s.factory.GraphQLClient()
	if err != nil {
		return inboxAck{}, err
	}
	r, err := gen.AcknowledgeDeliveryBatches(api.WithSession(ctx, scope.SessionID), c, ids, nil)
	if err != nil {
		return inboxAck{}, err
	}
	if r == nil || r.AcknowledgeDeliveryBatches == nil {
		return inboxAck{}, exitcode.Newf(exitcode.Unavailable, "inbox returned no acknowledgment")
	}
	a := r.AcknowledgeDeliveryBatches
	if a.Acknowledged < 0 || a.Unchanged < 0 || a.Acknowledged+a.Unchanged != len(ids) {
		return inboxAck{}, exitcode.Newf(exitcode.Unavailable, "inbox returned invalid acknowledgment counts")
	}
	return inboxAck{Acknowledged: a.Acknowledged, Unchanged: a.Unchanged}, nil
}

// Canonical deployment comparison excludes credentials, query and fragment;
// those are not alternative spellings of the binding's server.
func deliveryServerURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "https" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	} else {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
func inboxBoundIdentity(f *cmdutil.Factory) inboxIdentityReader {
	return func(ctx context.Context) (inboxIdentity, error) {
		b, _, err := readBindingFile(ctx)
		if err != nil || b == nil || b.AppID == "" || b.WorkerID == "" || b.SessionID == "" || b.Server == "" {
			return inboxIdentity{}, exitcode.Newf(exitcode.Usage, "inbox requires this worktree's complete worker session binding")
		}
		server, err := f.Server()
		if err != nil {
			return inboxIdentity{}, err
		}
		canonical := deliveryServerURL(server)
		if canonical == "" || canonical != deliveryServerURL(b.Server) {
			return inboxIdentity{}, exitcode.Newf(exitcode.Conflict, "inbox server differs from the worker binding")
		}
		app, err := f.App()
		if err != nil {
			return inboxIdentity{}, err
		}
		if app != "" && app != b.AppID {
			c, err := f.GraphQLClient()
			if err != nil {
				return inboxIdentity{}, err
			}
			r, err := gen.TeamAppIdentity(ctx, c, app)
			if err != nil {
				return inboxIdentity{}, err
			}
			if r == nil || r.App == nil || r.App.Id != b.AppID {
				return inboxIdentity{}, exitcode.Newf(exitcode.Conflict, "inbox App differs from the worker binding")
			}
		}
		return inboxIdentity{AppID: b.AppID, WorkerID: b.WorkerID, SessionID: b.SessionID, Server: canonical}, nil
	}
}
func newCmdTeamChatInbox(f *cmdutil.Factory) *cobra.Command {
	s := &sessionInbox{factory: f}
	c := newCmdTeamChatInboxWithTransport(f, s, inboxBoundIdentity(f))
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("after") && strings.TrimSpace(s.after) == "" {
			return exitcode.Newf(exitcode.Usage, "--after requires a nonempty endCursor")
		}
		return run(cmd, args)
	}
	c.Flags().StringVar(&s.after, "after", "", "Read the next own-worker page using the returned endCursor")
	c.MarkFlagsMutuallyExclusive("after", "ack-file")
	return c
}
