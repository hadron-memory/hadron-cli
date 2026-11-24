package team

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// hadron-cli#2062 (C7, epic hadron-server#4229): `team session recover` is the
// ONE read that replaces the five a fresh run makes today (session whoami,
// worker get, the state note, mentions since the watermark, task mine).
//
// STRICTLY READ-ONLY. Every request here is made on the command's own context,
// which never carries api.WithSession, so no X-Hadron-Session header is sent:
// the server therefore records no heartbeat and no read state for this call,
// and the chat read is the unattributed form that cannot acknowledge anything.
// Reading never makes a session live (pinned in the command tests).

const (
	// recoverStateNoteLoc is the loc of the per-worker state note in the
	// worker's own working memory, Worker.memoryId (long-lived-agents-on-orca
	// section 4: one note per worker, overwritten). Confirmed by S7's author and
	// the CLI coordinator, 2026-11-15; the boot prompt names the same loc.
	recoverStateNoteLoc = "state"
	// recoverDirectionsHeading is the state note's section holding the
	// human's standing directions, verbatim.
	recoverDirectionsHeading = "standing directions"
	recoverTaskCap           = 200
	recoverMentionsDefault   = 20
)

// recoverDTO is the documented --json shape of `team session recover`
// (agentic-usage.md). Explicit DTOs, never genqlient structs; slices are []
// and never null; a value the server withheld or that does not exist is null.
type recoverDTO struct {
	Worker             recoverWorkerDTO   `json:"worker"`
	Session            recoverSessionDTO  `json:"session"`
	Tasks              []recoverTaskDTO   `json:"tasks"`
	TasksMore          bool               `json:"tasksMore"`
	StateNote          *recoverNoteDTO    `json:"stateNote"`
	StandingDirections *string            `json:"standingDirections"`
	Mentions           recoverMentionsDTO `json:"mentions"`
	// Unavailable lists every section that could not be read, with the
	// reason. A section named here is null/empty because the read failed, NOT
	// because there was nothing: the command also exits 7.
	Unavailable []recoverUnavailableDTO `json:"unavailable"`
}

type recoverWorkerDTO struct {
	ID    string  `json:"id"`
	URN   *string `json:"urn"`
	Name  string  `json:"name"`
	Role  string  `json:"role"`
	AppID string  `json:"appId"`
}

type recoverSessionDTO struct {
	ID        string `json:"id"`
	StartedAt string `json:"startedAt"`
	Server    string `json:"server"`
	// Open is the server's answer for the bound session: true while it has not
	// ended, false once it ended, null when the read was unavailable.
	Open    *bool   `json:"open"`
	EndedAt *string `json:"endedAt"`
}

type recoverTaskDTO struct {
	ID                string  `json:"id"`
	Handle            string  `json:"handle"`
	Status            string  `json:"status"`
	Priority          int     `json:"priority"`
	Title             string  `json:"title"`
	Blocked           bool    `json:"blocked"`
	WaitingOnChildren bool    `json:"waitingOnChildren"`
	ParentTaskID      *string `json:"parentTaskId"`
}

type recoverNoteDTO struct {
	URN       string `json:"urn"`
	UpdatedAt string `json:"updatedAt"`
	Content   string `json:"content"`
}

type recoverMentionsDTO struct {
	// Watermark is the worker's server read cursor on the team channel; null
	// when the server reports none (never a forged 0).
	Watermark *int `json:"watermark"`
	Head      *int `json:"head"`
	// Items are the messages mentioning this worker after the watermark,
	// oldest first, at most the page size; More says the page was full.
	Items []teamChatMessageDTO `json:"items"`
	More  bool                 `json:"more"`
}

type recoverUnavailableDTO struct {
	Section string `json:"section"`
	Reason  string `json:"reason"`
}

// standingDirections returns the body of the "Standing directions" section of
// a state note, verbatim, up to the next heading of the same or a higher
// level. ok is false when the note has no such section.
func standingDirections(note string) (string, bool) {
	lines := strings.Split(note, "\n")
	level, start := 0, -1
	for i, ln := range lines {
		n, title, isHeading := markdownHeading(ln)
		if !isHeading {
			continue
		}
		if start < 0 {
			if strings.EqualFold(title, recoverDirectionsHeading) {
				level, start = n, i+1
			}
			continue
		}
		if n <= level {
			return strings.Trim(strings.Join(lines[start:i], "\n"), "\n"), true
		}
	}
	if start < 0 {
		return "", false
	}
	return strings.Trim(strings.Join(lines[start:], "\n"), "\n"), true
}

func markdownHeading(line string) (level int, title string, ok bool) {
	t := strings.TrimRight(line, " \t\r")
	for level < len(t) && t[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(t) || t[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(strings.TrimRight(t[level:], "# ")), true
}

func newCmdSessionRecover(f *cmdutil.Factory) *cobra.Command {
	var mentionsLimit int
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Print everything a fresh run needs to resume: worker, tasks, state note, mentions",
		Long: `Recover your context in one read-only call. It prints, for this
worktree's bound worker:

  worker     who you are: name, role, URN, App, and the bound session
             (open or ended, as the server says)
  tasks      your PENDING and STARTED tasks (up to 200; tasksMore says more exist)
  state note the worker's state note: the node "` + recoverStateNoteLoc + `" in its own working
             memory; null when there is none
  directions the note's "Standing directions" section, verbatim; null without one
  mentions   team-chat messages that mention you after your server read
             watermark (oldest first, --mentions-limit, default 20)

It REPLACES: session whoami, worker get, the state note read, chat read
--mentions-me --since <watermark>, task mine.

STRICTLY READ-ONLY. It sends no session identity, so it records no
heartbeat and acknowledges nothing: it does not advance your read watermark
(run ` + "`team chat mark-read`" + ` yourself once you have acted on the mentions) and
reading never makes a session live. It needs a worker binding; without one it
exits 2 and names ` + "`team session start`" + `.

A section that cannot be read is listed under "unavailable" with the reason, is
null or empty in --json, and the command exits 7 after printing the rest: an
unreadable section never reads as "nothing to do".`,
		Example: "  hadron team session recover\n  hadron team session recover --json | jq '.tasks[].title'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if mentionsLimit < 1 || mentionsLimit > 200 {
				return exitcode.Newf(exitcode.Usage, "--mentions-limit must be 1 to 200")
			}
			return runSessionRecover(cmd, f, mentionsLimit)
		},
	}
	cmd.Flags().IntVar(&mentionsLimit, "mentions-limit", recoverMentionsDefault, "most mentions to print (1 to 200)")
	return cmd
}

func runSessionRecover(cmd *cobra.Command, f *cmdutil.Factory, mentionsLimit int) error {
	ctx := cmd.Context()
	b, _, err := readBinding(ctx)
	if err != nil {
		return err
	}
	if b == nil || b.SessionID == "" || b.WorkerID == "" || b.AppID == "" {
		return exitcode.Newf(exitcode.Usage, "`session recover` reads the bound worker, and this worktree has no complete worker session binding — `hadron team session start --as <name>` binds it")
	}
	if err := checkBindingServer(f, b); err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	dto := recoverDTO{
		Worker:      recoverWorkerDTO{ID: b.WorkerID, Name: b.WorkerName, Role: b.WorkerRole, AppID: b.AppID},
		Session:     recoverSessionDTO{ID: b.SessionID, StartedAt: b.StartedAt, Server: b.Server},
		Tasks:       []recoverTaskDTO{},
		Mentions:    recoverMentionsDTO{Items: []teamChatMessageDTO{}},
		Unavailable: []recoverUnavailableDTO{},
	}
	fail := func(section string, err error) {
		dto.Unavailable = append(dto.Unavailable, recoverUnavailableDTO{Section: section, Reason: api.MapError(err).Error()})
	}

	// Session: the server's word on whether the bound session is still open.
	// The same header-free read `whoami --check` makes.
	if live, err := checkSessionLiveness(cmd, f, b); err != nil {
		fail("session", err)
	} else {
		open := live.active
		dto.Session.Open, dto.Session.EndedAt = &open, live.endedAt
	}

	// Worker: canonical identity and its working memory.
	memoryID := ""
	if w, err := gen.RecoverWorker(ctx, client, b.WorkerID); err != nil {
		fail("worker", err)
	} else if w == nil || w.Worker == nil {
		fail("worker", exitcode.Newf(exitcode.NotFound, "the server does not return worker %s for this credential", b.WorkerID))
	} else {
		if w.Worker.MemoryId != nil {
			memoryID = *w.Worker.MemoryId
		}
		dto.Worker.URN, dto.Worker.Name = w.Worker.Urn, w.Worker.Name
		if w.Worker.Role != nil {
			dto.Worker.Role = *w.Worker.Role
		}
	}

	// Tasks: `task mine`'s fixed filters.
	worker := b.WorkerID
	if tasks, info, err := taskListPage(ctx, client, b.AppID, taskListQuery{
		assignee: &gen.AgentTaskPartyInput{Worker: &worker},
		status:   []gen.AgentTaskStatus{gen.AgentTaskStatusPending, gen.AgentTaskStatusStarted},
		limit:    recoverTaskCap,
	}); err != nil {
		fail("tasks", err)
	} else {
		for _, t := range tasks {
			dto.Tasks = append(dto.Tasks, recoverTaskDTO{ID: t.ID, Handle: t.Handle, Status: t.Status, Priority: t.Priority,
				Title: t.Title, Blocked: t.Blocked, WaitingOnChildren: t.WaitingOnChildren, ParentTaskID: t.ParentTaskID})
		}
		// The server's answer, not a full-page guess (cli#2734).
		dto.TasksMore = info.HasMore
	}

	// State note and standing directions, from the worker's own working
	// memory. No memoryId (not provisioned, or withheld) reads as no note.
	if memoryID != "" {
		recoverStateNote(ctx, client, memoryID, &dto, fail)
	}

	// Mentions after the watermark, unattributed so nothing is acknowledged.
	state := &chatReadStateDTO{}
	startChatReadMetadata(ctx, f, client, b.AppID, b).wait(f, state)
	dto.Mentions.Watermark, dto.Mentions.Head = state.ReadCursor, state.AllocatedHead
	if state.ReadCursor == nil {
		fail("mentions", exitcode.Newf(exitcode.Unavailable, "the server reports no read watermark for this worker on the team channel, so \"after the watermark\" cannot be answered"))
	} else {
		since, size, me := *state.ReadCursor, mentionsLimit, b.WorkerID
		if page, err := gen.TeamChatReadPage(ctx, client, b.AppID, &since, &me, &size, nil, nil); err != nil {
			fail("mentions", err)
		} else if page == nil || page.TeamChatMessages == nil {
			// A null page is the server saying nothing, not "no messages".
			fail("mentions", exitcode.Newf(exitcode.Unavailable, "the server returned no team-chat page for the mentions read"))
		} else {
			for _, m := range page.TeamChatMessages.Items {
				if m != nil {
					dto.Mentions.Items = append(dto.Mentions.Items, teamChatMessageDTOFromFields(m.TeamChatMessageFields))
				}
			}
			dto.Mentions.More = len(dto.Mentions.Items) >= mentionsLimit
		}
	}

	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderRecover(w, dto) }); err != nil {
		return err
	}
	if len(dto.Unavailable) > 0 {
		names := make([]string, 0, len(dto.Unavailable))
		for _, u := range dto.Unavailable {
			names = append(names, u.Section)
		}
		return exitcode.Newf(exitcode.Unavailable, "recovery incomplete: could not read %s (see \"unavailable\"); an unread section is not an empty one — retry", strings.Join(names, ", "))
	}
	return nil
}

// recoverStateNote reads the state note from the working memory. The memory
// is addressed by id, which the node read does not take as a prefix, so its URN
// is read first (no slug grammar is assumed anywhere). An absent note is
// normal; any other failure is an unavailable section.
func recoverStateNote(ctx context.Context, client graphql.Client, memoryID string, dto *recoverDTO, fail func(string, error)) {
	mem, err := gen.GetMemory(ctx, client, memoryID)
	if err != nil {
		fail("stateNote", err)
		return
	}
	if mem == nil || mem.Memory == nil || mem.Memory.Urn == "" {
		fail("stateNote", exitcode.Newf(exitcode.NotFound, "the server does not return the worker's working memory %s for this credential", memoryID))
		return
	}
	note, err := gen.GetNode(ctx, client, cmdutil.NodeURN(mem.Memory.Urn, recoverStateNoteLoc))
	switch {
	case err != nil && !isNodeNotFound(err):
		fail("stateNote", err)
	case err == nil && note != nil && note.Node != nil && note.Node.Content != nil:
		dto.StateNote = &recoverNoteDTO{URN: note.Node.Urn, UpdatedAt: note.Node.UpdatedAt, Content: *note.Node.Content}
		if d, ok := standingDirections(*note.Node.Content); ok {
			dto.StandingDirections = &d
		}
	}
}

// isNodeNotFound reports a missing node (an absent state note is normal).
func isNodeNotFound(err error) bool {
	return exitcode.FromError(api.MapError(err)) == exitcode.NotFound
}

func renderRecover(w io.Writer, d recoverDTO) error {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("worker: %s (%s)\n", output.SafeCell(d.Worker.Name), output.SafeCell(d.Worker.Role))
	if d.Worker.URN != nil {
		p("  urn: %s\n", output.SafeCell(*d.Worker.URN))
	}
	state := "unavailable"
	if d.Session.Open != nil {
		state = "open"
		if !*d.Session.Open {
			state = "ENDED"
		}
	}
	p("  session: %s (%s, started %s)\n\n", output.SafeCell(d.Session.ID), state, output.SafeCell(d.Session.StartedAt))

	p("open tasks: %d\n", len(d.Tasks))
	if len(d.Tasks) > 0 {
		t := output.NewTable(w, "HANDLE", "ID", "STATUS", "PRIORITY", "BLOCKED", "TITLE")
		for _, task := range d.Tasks {
			t.Row(output.SafeCell(task.Handle), output.SafeCell(task.ID), output.SafeCell(task.Status), fmt.Sprint(task.Priority), fmt.Sprint(task.Blocked), output.SafeCell(task.Title))
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	if d.TasksMore {
		// The server's hasMore, not a full-page guess (cli#2734): the page can
		// be short and still not be the end.
		p("  (the task page did not end: more tasks may exist)\n")
	}

	p("\nstate note: ")
	if d.StateNote == nil {
		p("none\n")
	} else {
		p("%s (updated %s)\n%s\n", output.SafeCell(d.StateNote.URN), output.SafeCell(d.StateNote.UpdatedAt), d.StateNote.Content)
	}
	if d.StandingDirections != nil {
		p("\nstanding directions (verbatim):\n%s\n", *d.StandingDirections)
	}

	wm := "unavailable"
	if d.Mentions.Watermark != nil {
		wm = fmt.Sprintf("#%d", *d.Mentions.Watermark)
	}
	p("\nmentions after watermark %s: %d\n", wm, len(d.Mentions.Items))
	for _, m := range d.Mentions.Items {
		author := "?"
		if m.AuthorName != nil {
			author = *m.AuthorName
		}
		p("[%d] %s %s: %s\n", m.Seq, output.SafeCell(m.At), output.SafeCell(author), m.Body)
	}
	if d.Mentions.More {
		p("  (a full page: read on with `team chat read --mentions-me --since <last seq> --all`)\n")
	}
	if len(d.Unavailable) > 0 {
		p("\nunavailable:\n")
		for _, u := range d.Unavailable {
			p("  %s: %s\n", u.Section, output.SafeCell(u.Reason))
		}
	}
	return nil
}
