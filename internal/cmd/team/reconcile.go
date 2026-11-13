package team

import (
	"context"
	"errors"
	"fmt"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/intake"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The reconcile pass (cli#1962, cor:agt:120 D4 "minimal write-back", cor:agt:120:04).
//
// At the start of every `find-untracked --adapter` run the CLI reads, from the
// server, the changes to the App's tasks that carry an external id since its
// cursor, and tells the team's adapter what the tracker should hear:
//
//	REASSIGNED  -> mark-tracked again, for the new assignee
//	RELEASED    -> untrack, with the RELEASED id the feed reports (the task holds none)
//	CANCELLED   -> report, only when the adapter's describe declares it
//	everything else (ASSIGNED, HANDED_BACK, FINISHED) -> nothing in v1
//
// WHERE T LIVES: it is the server's OPAQUE cursor, kept by the CLI in a file
// beside its config, per server + App + adapter (intake.CursorStore). Never a
// client clock. The first run has no cursor and reads from the server's oldest
// change; a second run starts after the last change the first one finished.
// The cursor moves only past changes the adapter has answered (or that this
// adapter is not asked about), so a failed call is retried by the next run.

const (
	reconcilePage     = 200 // the server's cap
	reconcileMaxPages = 25  // per run; the cursor resumes the rest next run
)

// reconcileDTO is the reconcile pass's --json shape, nested in the list and
// claim outputs. Counts are of changes, not tasks.
type reconcileDTO struct {
	// Read is how many changes the pass read this run.
	Read int `json:"read"`
	// Marked, Untracked and Reported are the adapter calls that succeeded.
	Marked    int `json:"marked"`
	Untracked int `json:"untracked"`
	Reported  int `json:"reported"`
	// Skipped are changes whose call the adapter does not answer (describe did
	// not list it) or that v1 does nothing for.
	Skipped int `json:"skipped"`
	// Complete is false when the pass stopped early (an adapter failure, or
	// the per-run page bound); the next run resumes at the stored cursor.
	Complete bool `json:"complete"`
	// Error is why the pass stopped on an adapter failure, else null.
	Error *string `json:"error"`
}

// reconcile runs the pass and returns the summary, then the adapter failure
// (the run reports it at its end; the list and claim still proceed, as they do
// not depend on the write-back), then a fatal error (server, store) that ends
// the run.
func reconcile(ctx context.Context, client graphql.Client, store *intake.CursorStore, key, appRef string, runner *intake.Runner, desc intake.Describe) (dto reconcileDTO, adapterFailure, fatal error) {
	dto = reconcileDTO{Complete: true}
	// One run at a time per feed: the cursor read, the adapter's effects and the
	// cursor write are one critical section (cli#1975, LOCAL P2).
	serr := store.Serialize(ctx, key, func() error {
		dto, adapterFailure, fatal = reconcileLocked(ctx, client, store, key, appRef, runner, desc)
		return nil
	})
	if serr != nil {
		var busy *intake.BusyError
		if errors.As(serr, &busy) {
			return dto, nil, exitcode.Newf(exitcode.Conflict, "%s", busy.Error())
		}
		if ctx.Err() != nil {
			return dto, nil, adapterErr(ctx.Err())
		}
		return dto, nil, exitcode.Newf(exitcode.Error, "the reconcile lock: %v", serr)
	}
	return dto, adapterFailure, fatal
}

func reconcileLocked(ctx context.Context, client graphql.Client, store *intake.CursorStore, key, appRef string, runner *intake.Runner, desc intake.Describe) (dto reconcileDTO, adapterFailure, fatal error) {
	dto = reconcileDTO{Complete: true}
	cursor, err := store.Get(key)
	if err != nil {
		return dto, nil, exitcode.Newf(exitcode.Error, "the reconcile cursor store: %v", err)
	}
	limit := reconcilePage
	for pages := 0; ; pages++ {
		if pages >= reconcileMaxPages {
			dto.Complete = false
			return dto, nil, nil
		}
		sent := cursor
		var after *string
		if cursor != "" {
			after = &cursor
		}
		resp, err := gen.IntakeExternalIdChanges(ctx, client, appRef, after, &limit)
		if err != nil {
			if api.IsGraphQLValidationOnlyFor(err, "agentTaskExternalIdChanges") {
				return dto, nil, exitcode.Newf(exitcode.Usage, "this server cannot list task changes (it predates hadron-server#4084); the reconcile pass cannot run")
			}
			return dto, nil, mapTaskError(err)
		}
		page := resp.AgentTaskExternalIdChanges
		// A non-empty page must move the cursor. One whose resulting cursor is
		// the `after` just sent would be replayed on every page and every run:
		// refuse it before any effect, persist nothing (the list walk refuses a
		// repeated cursor the same way).
		if next := pageCursor(page.NextCursor, page.Changes); sent != "" && len(page.Changes) > 0 && next == sent {
			msg := "the server's change feed did not advance its cursor; nothing was told for that page"
			dto.Error, dto.Complete = &msg, false
			return dto, exitcode.Newf(exitcode.Error, "%s", msg), nil
		}
		for _, ch := range page.Changes {
			if ch == nil { // a null element cannot be told to an adapter
				dto.Skipped++
				continue
			}
			dto.Read++
			if ferr := reconcileOne(ctx, runner, desc, ch.Kind, ch.ExternalId, ch.Task, &dto); ferr != nil {
				// The cursor stays after the last answered change, so the
				// next run retries this one.
				if err := store.Set(key, cursor); err != nil {
					return dto, nil, exitcode.Newf(exitcode.Error, "the reconcile cursor store: %v", err)
				}
				msg := output.SafeLine(ferr.Error(), 300)
				dto.Error, dto.Complete = &msg, false
				return dto, adapterErr(ferr), nil
			}
			cursor = ch.Cursor // past this change only now that it is answered
		}
		cursor = pageCursor(page.NextCursor, page.Changes)
		if cursor != "" {
			if err := store.Set(key, cursor); err != nil {
				return dto, nil, exitcode.Newf(exitcode.Error, "the reconcile cursor store: %v", err)
			}
		}
		if !page.HasMore {
			return dto, nil, nil
		}
		if len(page.Changes) == 0 {
			// More promised, none served: do not claim to be caught up (an
			// empty answer must not read as the end), and do not spin.
			// More promised, none served: loud, not "more next run".
			msg := "the server's change feed promised more changes and served none"
			dto.Error, dto.Complete = &msg, false
			return dto, exitcode.Newf(exitcode.Error, "%s", msg), nil
		}
	}
}

// pageCursor is where the next read starts: the server's nextCursor, else the
// last change's own cursor, else (an empty page) nothing, which callers read as
// "unchanged".
func pageCursor(next *string, changes []*feedChange) string {
	if next != nil && *next != "" {
		return *next
	}
	if len(changes) > 0 && changes[len(changes)-1] != nil {
		return changes[len(changes)-1].Cursor
	}
	return ""
}

type feedChange = gen.IntakeExternalIdChangesAgentTaskExternalIdChangesAgentTaskExternalIdChangePageChangesAgentTaskExternalIdChange

type changeTask = gen.IntakeExternalIdChangesAgentTaskExternalIdChangesAgentTaskExternalIdChangePageChangesAgentTaskExternalIdChangeTaskAgentTask

// reconcileOne maps one change to its adapter call.
func reconcileOne(ctx context.Context, r *intake.Runner, desc intake.Describe, kind gen.AgentTaskExternalIdChangeKind, externalID string, t *changeTask, dto *reconcileDTO) error {
	switch kind {
	case gen.AgentTaskExternalIdChangeKindReassigned:
		// A task now without an assignee names no worker to mark.
		if t == nil || t.Assignee == nil || !desc.Has("mark-tracked") {
			dto.Skipped++
			return nil
		}
		task := intake.Task{ID: t.Id, Handle: t.Handle}
		if t.PortalUrl != nil {
			task.PortalURL = *t.PortalUrl
		}
		// The new worker made no claim, so the model at claim time does not
		// exist: only what the server names is given (an open ruling in
		// cor:agt:120:04).
		var worker intake.Worker
		if t.Assignee != nil {
			worker.ID = t.Assignee.Id
			if t.Assignee.Name != nil {
				worker.Name = *t.Assignee.Name
			}
		}
		if err := r.MarkTracked(ctx, externalID, task, worker); err != nil {
			return err
		}
		dto.Marked++
	case gen.AgentTaskExternalIdChangeKindReleased:
		if !desc.Has("untrack") {
			dto.Skipped++
			return nil
		}
		if err := r.Untrack(ctx, externalID); err != nil {
			return err
		}
		dto.Untracked++
	case gen.AgentTaskExternalIdChangeKindCancelled:
		if t == nil || !desc.Has("report") { // the adapter's choice: only when declared
			dto.Skipped++
			return nil
		}
		task := intake.Task{ID: t.Id, Handle: t.Handle}
		if t.PortalUrl != nil {
			task.PortalURL = *t.PortalUrl
		}
		if err := r.Report(ctx, externalID, task, "CANCELLED"); err != nil {
			return err
		}
		dto.Reported++
	default: // ASSIGNED, HANDED_BACK, FINISHED: nothing in v1 (cor:agt:120 D4)
		dto.Skipped++
	}
	return nil
}

// humanReconcile is the text-mode line, shown only when the pass did or said
// something.
func humanReconcile(d reconcileDTO) string {
	if d.Read == 0 && d.Error == nil && d.Complete {
		return ""
	}
	s := fmt.Sprintf("reconcile: read %d change(s): %d marked, %d untracked, %d reported, %d skipped", d.Read, d.Marked, d.Untracked, d.Reported, d.Skipped)
	if !d.Complete && d.Error == nil {
		s += " (more next run)"
	}
	if d.Error != nil {
		s += "; stopped: " + *d.Error
	}
	return s
}

// reconcileRun is reconcile with the CLI's own cursor store, keyed by this
// server, App and adapter.
func reconcileRun(ctx context.Context, f *cmdutil.Factory, client graphql.Client, appRef string, runner *intake.Runner, desc intake.Describe) (reconcileDTO, error, error) {
	store, err := intake.DefaultCursorStore()
	if err != nil {
		return reconcileDTO{Complete: true}, nil, exitcode.Newf(exitcode.Error, "the reconcile cursor store: %v", err)
	}
	server, err := f.Server()
	if err != nil {
		return reconcileDTO{Complete: true}, nil, err
	}
	return reconcile(ctx, client, store, intake.CursorKey(server, appRef, desc.Name), appRef, runner, desc)
}
