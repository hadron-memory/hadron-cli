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
		Long: `Collect the messages in your team chat that tag your bound worker, write them
to a private file, and mark the chat read. Use it to catch up on what is
addressed to you without reading the whole chat. It reads the team chat of the
App your worktree is bound to (see ` + "`" + `hadron team chat --help` + "`" + `).

What it does, in order:
  1. Notes the chat's newest seq once (call it the head). Messages that arrive
     after that are left unmarked for the next check.
  2. Starts from your stored read position when that was recorded for the
     same incarnation of the chat (its reset generation). When it was not, it
     scans from the beginning and may show messages you have seen before.
  3. Collects every message up to the head that tags you, including @all and
     @team tags, and writes them, in order, as JSON to a private file (mode
     0600, in a new 0700 cache directory). The file path is printed to stderr
     BEFORE anything is marked. Open that file to read the messages; the
     message text is never printed to stdout, so output truncation cannot
     hide one.
  4. Marks the chat read through the head. This also covers messages in that
     range that did not tag you. "Done" means the file is written and the mark
     landed, not that you read the file. An empty result still advances the
     position to the head.

The file is not deleted: remove it yourself once you have read it. The delivery
batches ` + "`" + `team chat inbox` + "`" + ` shows are not acknowledged by this command, and the
roster's CHECK column is not changed or asserted; look at the roster for that.

Exit codes: 0 messages written and the mark confirmed; 9 nothing tagged you and
the mark is confirmed (not an error, so handle it in scripts that stop on
errors); 10 the mark may or may not have landed. On 10, the file (if any) is
kept. Verify that your read position in the same incarnation reached the head
(the read-state line of ` + "`team chat read`" + ` shows it); only if it did not, retry
that one mark at the same head ONCE, and never when the incarnation differs. Do
not retry in a loop. An old-server fallback is not
used. If the server keeps answering with a position from an older chat, check
can keep returning 10 until a ` + "`" + `team chat mark-read` + "`" + ` moves it. Errors 1 to 8
mean what they do everywhere. A failure before the mark sends no change, though
the read may still count as a check.

--json prints a short receipt (never the messages, which are in the file). If
writing that receipt fails after the mark landed, the error says so.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			identity, err := inboxBoundIdentity(f)(ctx)
			if err != nil {
				return exitcode.Newf(exitcode.FromError(err), "check requires a complete binding on the selected App/server; run hadron team session whoami --check")
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
			s.CursorIncarnation = cursor.CursorIncarnation
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
					q, e := gen.CheckChatPage(api.WithSession(ctx, identity.SessionID), client, s.ChannelID, s.Incarnation, identity.WorkerID, checkScanFloor(s), before)
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
					return publishCheckArtifact(cache, checkArtifact{Version: 1, WorkerID: identity.WorkerID, ChannelID: s.ChannelID, ChannelIncarnation: s.Incarnation, SampledCursor: s.Cursor, ScanCursor: checkScanFloor(s), CursorIncarnation: s.CursorIncarnation, SampledHead: s.Head, Messages: ms}, f.IOStreams.ErrOut)
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
					if m.CursorIncarnation == nil || *m.CursorIncarnation != s.Incarnation {
						return false, errCheckCursorLabel
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
			outputErr := output.Write(&streams, f.JSON, result, func(w io.Writer) error { return renderCheckReceipt(w, result) })
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

func renderCheckReceipt(w io.Writer, r checkResult) error {
	if r.Error != nil {
		status := "No mark was attempted."
		switch r.MarkStatus {
		case "refused":
			status = "The mark was refused."
		case "uncertain":
			status = "The mark outcome is uncertain."
		case "confirmed":
			status = "Cursor coverage was confirmed."
		}
		rescue := ""
		if r.ArtifactPath != nil {
			rescue = fmt.Sprintf(" Messages remain at %q.", *r.ArtifactPath)
		}
		if r.MarkStatus == "uncertain" {
			return writeCheckNotice(w, fmt.Sprintf("Check incomplete: %s.%s\n", r.Error.Message, rescue))
		}
		return writeCheckNotice(w, fmt.Sprintf("Check failed: %s. %s%s\n", r.Error.Message, status, rescue))
	}
	if r.MessageCount == 0 {
		return writeCheckNotice(w, fmt.Sprintf("No tagged messages; marked through #%d (scanned above #%d; stored cursor #%d, from the worktree binding).\n", r.SampledHead, r.ScanCursor, r.SampledCursor))
	}
	word := "messages"
	if r.MessageCount == 1 {
		word = "message"
	}
	artifact := ""
	if r.ArtifactPath != nil {
		artifact = fmt.Sprintf(" to %q", *r.ArtifactPath)
	}
	return writeCheckNotice(w, fmt.Sprintf("Wrote %d tagged %s%s and marked through #%d (scanned above #%d; stored cursor #%d, from the worktree binding).\n", r.MessageCount, word, artifact, r.SampledHead, r.ScanCursor, r.SampledCursor))
}
