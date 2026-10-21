package team

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func checkFailure(err error) error {
	if api.HasErrorCode(err, "CHANNEL_INCARNATION_CHANGED") {
		return exitcode.New(exitcode.Conflict, errCheckIncarnation)
	}
	for _, v := range []struct{ code, text string }{
		{"SESSION_NOT_LIVE", "worker session is no longer live; check the binding before trying again"},
		{"APP_KEY_SESSION_BOUND", "this credential cannot drive the bound worker session"},
		{"FORBIDDEN", "caller is not permitted to check or mark this channel"},
		{"UNAUTHENTICATED", "check requires authentication"},
		{"GRAPHQL_VALIDATION_FAILED", "server does not support the guarded check wire; upgrade the server"},
		{"SEQ_BEYOND_WATERMARK", "sample is beyond the channel watermark; run check again"},
	} {
		if api.HasErrorCode(err, v.code) {
			return exitcode.Newf(exitcode.FromError(api.MapError(err)), "%s: %s", v.code, v.text)
		}
	}
	code := exitcode.FromError(api.MapError(err))
	return exitcode.Newf(code, "team chat check request failed; no upstream details printed")
}

func newCmdTeamChatCheck(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "check", Short: "Write every tagged message to a private file, then mark the sampled head", Args: cobra.NoArgs,
		Long: `Check all messages tagging this worktree's bound worker in its team channel.
Sample the channel, incarnation, cursor C and committed head H once. Drain every
matching message in (C,H], including server-selected @all/team tags. Every page
is fenced and acknowledgement-suppressed. New arrivals after H stay unmarked.

For any messages, write the complete ordered JSON artifact to a private file
(0600 in a new 0700 cache directory) and print its path to stderr BEFORE marking.
Open that file to read the messages. Completion means written and marked, not
that you read the file. The mark also covers UNTAGGED messages between C and H.
An empty tagged range also advances through H. No artifact is removed on success;
remove it yourself after reading. No delivery-batch inbox proofs are acknowledged.

Exit 0: messages written and cursor coverage confirmed. Exit 9: nothing tagged,
cursor coverage confirmed. Exit 10: mark outcome uncertain. Existing errors 1-8
keep their meanings. Read/file/path-output errors before mark send no mutation,
but served own-session reads may already count as checks. No stamp acknowledgement
or roster OK status is asserted: inspect the roster CHECK column separately.

If a mark reply is lost, verify same-incarnation cursor coverage; if unconfirmed,
retry exactly that fenced H mark ONCE, never an incarnation mismatch. There is no
rollback. An uncertain result retains the artifact; rerun check from the current
binding. No old-server, headerless or unguarded fallback is used.

--json emits a bounded result receipt; message bodies are always in the artifact,
never stdout, so tool truncation cannot silently hide messages while marking.
The artifact path/pre-mark notice goes to stderr in both modes. A final receipt
write failure can occur AFTER a confirmed mark; its diagnostic says so.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			identity, err := inboxBoundIdentity(f)(ctx)
			if err != nil {
				return exitcode.Newf(exitcode.FromError(err), "check requires a complete binding on the selected App/server; check team session whoami --check")
			}
			b, path, err := readBindingFile(ctx)
			if err != nil || b == nil {
				return exitcode.Newf(exitcode.Usage, "check requires a readable worker binding")
			}
			lock := flock.New(path + ".chat-check.lock")
			held, err := lock.TryLock()
			if err != nil || !held {
				return exitcode.Newf(exitcode.Conflict, "another chat check holds this worktree lock, or it cannot be locked")
			}
			defer func() { _ = lock.Unlock() }()
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			guard := func() error {
				current, _, e := readBindingFile(ctx)
				if e != nil || current == nil || current.SessionID != identity.SessionID || current.WorkerID != identity.WorkerID || current.AppID != identity.AppID || deliveryServerURL(current.Server) != identity.Server {
					return exitcode.Newf(exitcode.Conflict, "worker binding changed during check; no further request sent")
				}
				return nil
			}
			if err = guard(); err != nil {
				return err
			}
			own := api.WithSession(ctx, identity.SessionID)
			session, err := gen.CheckChatSession(own, client, identity.SessionID)
			if err != nil {
				return checkFailure(err)
			}
			if session.Session == nil || session.Session.Id != identity.SessionID || session.Session.WorkerId == nil || *session.Session.WorkerId != identity.WorkerID || session.Session.AppId == nil || *session.Session.AppId != identity.AppID || session.Session.EndedAt != nil {
				return exitcode.Newf(exitcode.Conflict, "bound session is missing, ended, or bound to a different worker/App")
			}
			if err = guard(); err != nil {
				return err
			}
			channel, err := gen.CheckTeamChannel(own, client, identity.AppID)
			if err != nil {
				return checkFailure(err)
			}
			p := channel.TeamChatMessages
			if p == nil || p.ChannelId == nil || *p.ChannelId == "" || p.ChannelIncarnation == nil {
				return exitcode.Newf(exitcode.NotFound, "team channel identity/incarnation unavailable; nothing marked")
			}
			s := checkSnapshot{ChannelID: *p.ChannelId, Incarnation: *p.ChannelIncarnation}
			cursorRead := func(ctx context.Context) (*gen.CheckChatCursorChannelReadState, error) {
				if e := guard(); e != nil {
					return nil, e
				}
				q, e := gen.CheckChatCursor(api.WithSession(ctx, identity.SessionID), client, s.ChannelID, identity.WorkerID, identity.AppID)
				if e != nil {
					return nil, checkFailure(e)
				}
				r := q.ChannelReadState
				if r == nil || r.ChannelId != s.ChannelID || r.ChannelIncarnation != s.Incarnation {
					return nil, exitcode.New(exitcode.Conflict, errCheckIncarnation)
				}
				return r, nil
			}
			cursor, err := cursorRead(ctx)
			if err != nil {
				return err
			}
			s.Cursor = cursor.LastSeenSeq
			if err = guard(); err != nil {
				return err
			}
			head, err := gen.CheckChatHead(own, client, s.ChannelID, s.Incarnation)
			if err != nil {
				return checkFailure(err)
			}
			h := head.ChannelMessages
			if h == nil || h.ChannelId == nil || *h.ChannelId != s.ChannelID || h.ChannelIncarnation == nil || *h.ChannelIncarnation != s.Incarnation {
				return exitcode.New(exitcode.Conflict, errCheckIncarnation)
			}
			if len(h.Items) > 1 {
				return exitcode.Newf(exitcode.Unavailable, "invalid channel head page; nothing marked")
			}
			for _, m := range h.Items {
				if m == nil || m.Seq < 1 {
					return exitcode.Newf(exitcode.Unavailable, "incomplete channel head page; nothing marked")
				}
				s.Head = m.Seq
			}
			cache, err := os.UserCacheDir()
			if err != nil {
				return exitcode.Newf(exitcode.Error, "private cache directory unavailable; nothing marked")
			}
			cache, err = filepath.Abs(cache)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "invalid private cache directory; nothing marked")
			}
			deps := checkDeps{
				page: func(ctx context.Context, before int) ([]teamChatMessageDTO, error) {
					if e := guard(); e != nil {
						return nil, e
					}
					q, e := gen.CheckChatPage(api.WithSession(ctx, identity.SessionID), client, s.ChannelID, s.Incarnation, identity.WorkerID, s.Cursor, before)
					if e != nil {
						return nil, checkFailure(e)
					}
					page := q.ChannelMessages
					if page == nil || page.ChannelId == nil || *page.ChannelId != s.ChannelID || page.ChannelIncarnation == nil || *page.ChannelIncarnation != s.Incarnation {
						return nil, exitcode.New(exitcode.Conflict, errCheckIncarnation)
					}
					ms := []teamChatMessageDTO{}
					for _, m := range page.Items {
						if m == nil {
							return nil, exitcode.Newf(exitcode.Conflict, "incomplete message page; nothing marked")
						}
						ms = append(ms, teamChatMessageDTOFromFields(m.TeamChatMessageFields))
					}
					return ms, nil
				},
				publish: func(ms []teamChatMessageDTO) (string, error) {
					return publishCheckArtifact(cache, checkArtifact{Version: 1, WorkerID: identity.WorkerID, ChannelID: s.ChannelID, ChannelIncarnation: s.Incarnation, SampledCursor: s.Cursor, SampledHead: s.Head, Messages: ms}, f.IOStreams.ErrOut)
				},
				mark: func(ctx context.Context) (bool, error) {
					if e := guard(); e != nil {
						return false, e
					}
					r, e := gen.CheckChatMark(api.WithSession(ctx, identity.SessionID), client, identity.AppID, identity.SessionID, s.ChannelID, s.Incarnation, s.Head)
					if e != nil {
						if exitcode.FromError(api.MapError(e)) == exitcode.Unavailable {
							return false, errCheckTransport
						}
						return false, checkFailure(e)
					}
					m := r.MarkOwnTeamChatRead
					if m == nil || m.ChannelId != s.ChannelID || m.WorkerId != identity.WorkerID || m.ChannelIncarnation != s.Incarnation {
						return false, exitcode.Newf(checkUncertain, "mark response did not confirm sampled identity/incarnation; outcome uncertain, no retry sent")
					}
					return m.LastSeenSeq >= s.Head, nil
				},
				readback: func(ctx context.Context) (bool, error) {
					r, e := cursorRead(ctx)
					if e != nil {
						return false, e
					}
					return r.CursorIncarnation != nil && *r.CursorIncarnation == s.Incarnation && r.LastSeenSeq >= s.Head, nil
				},
			}
			result, runErr := runChatCheck(ctx, s, deps)
			if runErr != nil {
				result.Error = &checkError{Code: exitcode.FromError(runErr), Message: runErr.Error()}
			}
			// Result is always bounded and contains no message/session text. Emit even
			// on failure so scripts can distinguish pre-mark errors from uncertainty.
			streams := *f.IOStreams
			streams.Out = checkFullWriter{streams.Out}
			outputErr := output.Write(&streams, f.JSON, result, func(w io.Writer) error {
				if result.Error != nil {
					return writeCheckNotice(w, fmt.Sprintf("check failed: %s; mark %s.\n", result.Error.Message, result.MarkStatus))
				}
				return writeCheckNotice(w, fmt.Sprintf("%s: %d tagged messages; sampled #%d..#%d; mark %s (from the worktree binding).\n", result.Outcome, result.MessageCount, s.Cursor, s.Head, result.MarkStatus))
			})
			if outputErr != nil {
				_ = writeCheckNotice(f.IOStreams.ErrOut, fmt.Sprintf("check receipt output failed; mark status %s (no rollback).\n", result.MarkStatus))
				if result.MarkStatus == "uncertain" {
					return exitcode.Silent(checkUncertain)
				}
				return exitcode.Silent(exitcode.Error)
			}
			if runErr != nil {
				return exitcode.Silent(exitcode.FromError(runErr))
			}
			if result.MessageCount == 0 {
				return exitcode.Silent(checkEmpty)
			}
			return nil
		}}
	return c
}
