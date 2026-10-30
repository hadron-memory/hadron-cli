package team

// `team task list --team [--tree] [--stale-over <d>]` — #1044 slice 1: a
// coordinator's work in flight. The team is the coordinator plus every worker
// whose roster reportsTo chain reaches them, within the one App read. The
// rows are that App's tasks a team member owns, is assigned or created, plus every
// descendant of those (a child's owner is its parent's assignee, so a seat
// task's own children can sit wholly outside the team). One read gate: the
// existing agentTasks read, paged to the end, joined to the roster
// client-side. The creator counts (slice 2); cross-App rows are not read
// (hadron-server#2370).

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// teamFlagBound is --team's value when it is given bare: the bound worker.
const teamFlagBound = "@bound"

// teamTaskPageLimit bounds the whole-App read: 500 pages at the 200 cap is a
// hundred thousand tasks, past which the view refuses rather than spin.
const teamTaskPageLimit = 500

// teamListFlagConflicts are the plain list's server filters and paging. The
// team view reads the whole App and selects by the roster join, so each of
// these would either narrow the read below the recursion or page a view that
// is not a page; refused rather than half-honoured.
var teamListFlagConflicts = []string{"assignee", "owner", "parent", "tag", "queued", "roots-only", "limit", "offset",
	"tag-any", "tag-all", "source", "overdue",
	"created-after", "created-before", "started-after", "started-before", "finished-after", "finished-before"}

// teamTaskDTO is one row of the team view: the plain task plus how it joined.
// OwnerTeam / AssigneeTeam: "team" (a worker in the requested team), "outside"
// (a worker on this App's roster outside it), "unknown" (a worker the roster
// does not list — retired), "user" (a person), null for no party. Match:
// "party" (a team member owns, is assigned or created it) or "descendant" (under such
// a task). AgeSeconds counts from AgeFrom: updatedAt, else startedAt, else
// createdAt.
type teamTaskDTO struct {
	taskDTO
	OwnerTeam    *string `json:"ownerTeam"`
	AssigneeTeam *string `json:"assigneeTeam"`
	Match        string  `json:"match"`
	AgeSeconds   int64   `json:"ageSeconds"`
	AgeFrom      string  `json:"ageFrom"`
}

type teamTaskListDTO struct {
	AppID             string         `json:"appId"`
	Coordinator       taskPartyDTO   `json:"coordinator"`
	CoordinatorSource string         `json:"coordinatorSource"`
	Members           []taskPartyDTO `json:"members"`
	StaleOverSeconds  *int64         `json:"staleOverSeconds"`
	Tasks             []teamTaskDTO  `json:"tasks"`
}

type teamTaskQuery struct {
	team      string
	text      *string
	status    map[string]bool
	staleOver time.Duration
	tree      bool
}

// checkTeamListFlags validates the team view's flags before any read.
func checkTeamListFlags(cmd *cobra.Command, args []string, team, staleOver string) (time.Duration, error) {
	if !cmd.Flags().Changed("team") {
		for _, name := range []string{"tree", "stale-over"} {
			if cmd.Flags().Changed(name) {
				return 0, exitcode.Newf(exitcode.Usage, "--%s works on the team view: add --team (the bound worker's team) or --team=worker:<ref>", name)
			}
		}
		return 0, nil
	}
	if len(args) > 0 {
		return 0, exitcode.Newf(exitcode.Usage, "unexpected argument %q: --team takes its worker attached, as --team=worker:<ref>", args[0])
	}
	for _, name := range teamListFlagConflicts {
		if cmd.Flags().Changed(name) {
			return 0, exitcode.Newf(exitcode.Usage, "--%s does not combine with --team: the team view reads the whole App and selects by the roster (combine --status, --text, --tree, --stale-over)", name)
		}
	}
	if team != teamFlagBound {
		p, err := parseTaskPartyFlag("--team", team)
		if err != nil {
			return 0, err
		}
		if p.Worker == nil {
			return 0, exitcode.Newf(exitcode.Usage, "--team names a worker (worker:<id, URN or name>), got %q", team)
		}
	}
	if !cmd.Flags().Changed("stale-over") {
		return 0, nil
	}
	d, err := time.ParseDuration(staleOver)
	if err != nil || d <= 0 {
		return 0, exitcode.Newf(exitcode.Usage, "--stale-over takes a positive duration such as 30m or 1h, got %q", staleOver)
	}
	return d, nil
}

// rosterWorker is the roster's view of one worker, for the team join.
type rosterWorker struct {
	party     taskPartyDTO
	reportsTo string
}

// resolveTeam finds the coordinator on the roster and walks reportsTo down
// from them. Members come back coordinator first, then breadth-first in
// roster order; a reportsTo cycle cannot loop the walk.
func resolveTeam(workers []rosterWorker, coordinatorRef string) (taskPartyDTO, []taskPartyDTO, bool) {
	var coordinator *rosterWorker
	for i := range workers {
		w := &workers[i]
		if w.party.ID == coordinatorRef ||
			(w.party.Urn != nil && *w.party.Urn == coordinatorRef) ||
			(w.party.Name != nil && strings.EqualFold(*w.party.Name, coordinatorRef)) {
			coordinator = w
			break
		}
	}
	if coordinator == nil {
		return taskPartyDTO{}, nil, false
	}
	members := []taskPartyDTO{coordinator.party}
	seen := map[string]bool{coordinator.party.ID: true}
	for queue := []string{coordinator.party.ID}; len(queue) > 0; queue = queue[1:] {
		for _, w := range workers {
			if w.reportsTo == queue[0] && !seen[w.party.ID] {
				seen[w.party.ID] = true
				members = append(members, w.party)
				queue = append(queue, w.party.ID)
			}
		}
	}
	return coordinator.party, members, true
}

// readAllTasks pages the App's task read to the end. A page that adds no new
// task is a server ignoring the offset: refused, never silently repeated.
func readAllTasks(ctx context.Context, f *cmdutil.Factory, appRef string, text *string) ([]taskDTO, error) {
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, err
	}
	tasks := []taskDTO{}
	seen := map[string]bool{}
	limit := teamEventPageCap
	for page := 0; ; page++ {
		if page >= teamTaskPageLimit {
			return nil, exitcode.Newf(exitcode.Error, "the App's task read did not end after %d pages of %d; nothing shown", page, limit)
		}
		offset := page * limit
		rows, count, err := taskListPage(ctx, client, appRef, taskListQuery{limit: limit, offset: offset, text: text})
		if err != nil {
			return nil, err
		}
		added := 0
		for _, t := range rows {
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			added++
			tasks = append(tasks, t)
		}
		if count < limit {
			return tasks, nil
		}
		if added == 0 {
			return nil, exitcode.Newf(exitcode.Error, "the App's task read did not advance at offset %d (a full page of tasks already read); nothing shown", offset)
		}
	}
}

// taskAge is the task record's idle time: from updatedAt, else startedAt,
// else createdAt.
func taskAge(t taskDTO, now time.Time) (time.Duration, string, error) {
	at, from := t.CreatedAt, "createdAt"
	if t.UpdatedAt != nil {
		at, from = *t.UpdatedAt, "updatedAt"
	} else if t.StartedAt != nil {
		at, from = *t.StartedAt, "startedAt"
	}
	parsed, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return 0, "", exitcode.Newf(exitcode.Error, "task %s has an unreadable %s %q", t.ID, from, at)
	}
	return now.Sub(parsed), from, nil
}

func listTeamTasks(ctx context.Context, f *cmdutil.Factory, scope appScope, b *binding, q teamTaskQuery) error {
	// checkTeamListFlags has already refused anything but worker:<ref>.
	_, coordinatorRef, _ := strings.Cut(q.team, ":")
	source := "from --team"
	if q.team == teamFlagBound {
		if b == nil || b.WorkerID == "" {
			return exitcode.Newf(exitcode.Usage, "a bare --team means the bound worker's team, and this worktree has no worker session — name one with --team=worker:<ref>")
		}
		coordinatorRef, source = b.WorkerID, "the bound worker"
	}
	appLabel := lazyAppLabel(ctx, f, scope)
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	roster, err := gen.TeamReportingLines(ctx, client, scope.Ref)
	if err != nil {
		return mapTaskError(err)
	}
	workers := []rosterWorker{}
	for _, w := range roster.TeamRoster.Workers {
		if w == nil || w.Worker == nil {
			continue
		}
		rw := rosterWorker{party: *partyDTO(w.Worker)}
		if w.ReportsTo != nil {
			rw.reportsTo = w.ReportsTo.Id
		}
		workers = append(workers, rw)
	}
	coordinator, members, ok := resolveTeam(workers, coordinatorRef)
	if !ok {
		return exitcode.Newf(exitcode.NotFound, "worker %s is not on the roster of %s (the roster lists active workers; a retired worker leads no team here)", coordinatorRef, appLabel())
	}
	// Keep the ancestry read unfiltered. Ask the server for matching IDs in a
	// separate paged read before scanning ancestry, so invalid text fails fast.
	// Intersect only after the full descendant join.
	var textMatches map[string]bool
	if q.text != nil {
		filtered, err := readAllTasks(ctx, f, scope.Ref, q.text)
		if err != nil {
			return err
		}
		textMatches = map[string]bool{}
		for _, task := range filtered {
			textMatches[task.ID] = true
		}
	}
	all, err := readAllTasks(ctx, f, scope.Ref, nil)
	if err != nil {
		return err
	}
	alarms, err := openAlarmsByTask(ctx, client, f.IOStreams.ErrOut, roster.TeamRoster.AppId)
	if err != nil {
		return err
	}

	inTeam := map[string]bool{}
	for _, m := range members {
		inTeam[m.ID] = true
	}
	onRoster := map[string]bool{}
	for _, w := range workers {
		onRoster[w.party.ID] = true
	}
	label := func(p *taskPartyDTO) *string {
		if p == nil {
			return nil
		}
		l := "user"
		if p.Kind == string(gen.AgentTaskPartyKindWorker) {
			switch {
			case inTeam[p.ID]:
				l = "team"
			case onRoster[p.ID]:
				l = "outside"
			default:
				l = "unknown"
			}
		}
		return &l
	}

	// Select: a team worker who owns, is assigned or created a task, then
	// every descendant of a selected task (#1044 slice 2: the creator counts;
	// createdBy is a person for a task they made themselves, never a team
	// worker, so the worker kind is part of the test).
	match := map[string]string{}
	children := map[string][]string{}
	for _, t := range all {
		if t.ParentTaskID != nil {
			children[*t.ParentTaskID] = append(children[*t.ParentTaskID], t.ID)
		}
		isTeamWorker := func(p *taskPartyDTO) bool {
			return p != nil && p.Kind == string(gen.AgentTaskPartyKindWorker) && inTeam[p.ID]
		}
		if isTeamWorker(t.Owner) || isTeamWorker(t.Assignee) || isTeamWorker(t.CreatedBy) {
			match[t.ID] = "party"
		}
	}
	queue := []string{}
	for id := range match {
		queue = append(queue, id)
	}
	for ; len(queue) > 0; queue = queue[1:] {
		for _, child := range children[queue[0]] {
			if _, ok := match[child]; !ok {
				match[child] = "descendant"
				queue = append(queue, child)
			}
		}
	}

	now := time.Now()
	dto := teamTaskListDTO{
		AppID:             roster.TeamRoster.AppId,
		Coordinator:       coordinator,
		CoordinatorSource: source,
		Members:           members,
		Tasks:             []teamTaskDTO{},
	}
	if q.staleOver > 0 {
		s := int64(q.staleOver / time.Second)
		dto.StaleOverSeconds = &s
	}
	for _, t := range all {
		how, ok := match[t.ID]
		if !ok || (textMatches != nil && !textMatches[t.ID]) {
			continue
		}
		if len(q.status) > 0 && !q.status[t.Status] {
			continue
		}
		age, from, err := taskAge(t, now)
		if err != nil {
			return err
		}
		open := t.Status == "STARTED" || t.Status == "PENDING"
		if q.staleOver > 0 && (!open || age <= q.staleOver) {
			continue
		}
		if n, ok := alarms[t.ID]; ok {
			open := n
			t.OpenAlarms = &open
		}
		dto.Tasks = append(dto.Tasks, teamTaskDTO{
			taskDTO:      t,
			OwnerTeam:    label(t.Owner),
			AssigneeTeam: label(t.Assignee),
			Match:        how,
			AgeSeconds:   int64(age / time.Second),
			AgeFrom:      from,
		})
	}
	if q.staleOver > 0 {
		_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "note: age is the time since the task record changed (updatedAt, else startedAt, else createdAt); records lag real progress such as PR pushes and receipts")
	}

	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		// Both scope lines unconditionally and first (#470): the App and the
		// team are each resolved from a fallback, and an empty view must still
		// say what it read.
		if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "team: %s (%s) — %d worker%s\n", partyCell(&dto.Coordinator), source, len(members), plural(len(members))); err != nil {
			return err
		}
		if len(dto.Tasks) == 0 {
			_, err := fmt.Fprintln(w, "no tasks")
			return err
		}
		if q.tree {
			return renderTeamTree(w, dto.Tasks)
		}
		withHandle := false
		for _, task := range dto.Tasks {
			withHandle = withHandle || task.Handle != ""
		}
		t := output.NewTable(w, handleHeaders(withHandle, "ID", "STATUS", "PRIORITY", "BLOCKED", "IDLE", "TITLE", "OWNER", "ASSIGNEE", "ALARMS")...)
		for _, task := range dto.Tasks {
			t.Row(handleCells(withHandle, task.Handle, output.SafeCell(task.ID), output.SafeCell(task.Status), fmt.Sprint(task.Priority), fmt.Sprint(task.Blocked), output.SafeCell(idleCell(task.AgeSeconds)), output.SafeCell(task.Title),
				output.SafeCell(teamPartyCell(task.Owner, task.OwnerTeam)), output.SafeCell(teamAssigneeCell(task)), output.SafeCell(alarmCell(task.OpenAlarms)))...)
		}
		return t.Flush()
	})
}

// teamPartyCell is partyCell plus the join's verdict when it is not "team".
func teamPartyCell(p *taskPartyDTO, team *string) string {
	cell := partyCell(p)
	if team != nil && (*team == "outside" || *team == "unknown") {
		cell += " (" + map[string]string{"outside": "outside", "unknown": "not on roster"}[*team] + ")"
	}
	return cell
}

func teamAssigneeCell(task teamTaskDTO) string {
	if task.Queued {
		return "(queued)"
	}
	return teamPartyCell(task.Assignee, task.AssigneeTeam)
}

func alarmCell(n *int) string {
	if n == nil || *n == 0 {
		return "—"
	}
	return fmt.Sprint(*n)
}

// idleCell renders an age in whole minutes, hours and days.
func idleCell(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// renderTeamTree draws the selected rows as parent/child trees, in the
// server's order. A row whose parent was not selected (filtered by status or
// staleness, or outside the view) is a root; a cycle in corrupt parent links
// cannot hide a row, because anything the walk did not reach renders at the
// top afterwards.
func renderTeamTree(w io.Writer, tasks []teamTaskDTO) error {
	byID := map[string]int{}
	for i, t := range tasks {
		byID[t.ID] = i
	}
	children := map[string][]int{}
	roots := []int{}
	for i, t := range tasks {
		if t.ParentTaskID != nil {
			if _, ok := byID[*t.ParentTaskID]; ok {
				children[*t.ParentTaskID] = append(children[*t.ParentTaskID], i)
				continue
			}
		}
		roots = append(roots, i)
	}
	visited := map[int]bool{}
	var draw func(int, int) error
	draw = func(i, depth int) error {
		if visited[i] {
			return nil
		}
		visited[i] = true
		t := tasks[i]
		alarm := ""
		if t.OpenAlarms != nil && *t.OpenAlarms > 0 {
			alarm = fmt.Sprintf("  ⏰ %d open alarm%s", *t.OpenAlarms, plural(*t.OpenAlarms))
		}
		if _, err := fmt.Fprintf(w, "%s• %s [%s] idle %s  %s → %s  %s%s\n", strings.Repeat("  ", depth), taskRef(t.Handle, t.ID), t.Status,
			idleCell(t.AgeSeconds), teamPartyCell(t.Owner, t.OwnerTeam), teamAssigneeCell(t), t.Title, alarm); err != nil {
			return err
		}
		for _, c := range children[t.ID] {
			if err := draw(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range roots {
		if err := draw(r, 0); err != nil {
			return err
		}
	}
	for i := range tasks {
		if err := draw(i, 0); err != nil {
			return err
		}
	}
	return nil
}
