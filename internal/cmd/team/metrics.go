package team

// `team metrics`: the server's teamMetrics reports for the team App, rendered as
// returned (cli#1826). A read-only view: every count, grouping and bucket name is
// the server's, and the CLI never adds up, regroups, filters or relabels one.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// metricsNotVisible is what a report this reader may not see prints. A hidden
// report is null in --json and never reads as a zero.
const metricsNotVisible = "not visible to you"

// metricsCountDTO is the server's TeamMetricsCount: the exact total, at most 200
// ids, and whether the ids are fewer than the total.
type metricsCountDTO struct {
	Total     int      `json:"total"`
	IDs       []string `json:"ids"`
	Truncated bool     `json:"truncated"`
}

type metricsProjectDTO struct {
	Handle    string  `json:"handle"`
	NoProject bool    `json:"noProject"`
	TaskID    *string `json:"taskId"`
	Title     *string `json:"title"`
}

type metricsOwnerDTO struct {
	ID   string  `json:"id"`
	Kind string  `json:"kind"`
	Name *string `json:"name"`
	URN  *string `json:"urn"`
}

type metricsProjectGroupDTO struct {
	Project metricsProjectDTO `json:"project"`
	Tasks   metricsCountDTO   `json:"tasks"`
}

type metricsOwnerGroupDTO struct {
	Owner             *metricsOwnerDTO         `json:"owner"`
	Tasks             metricsCountDTO          `json:"tasks"`
	ByProject         []metricsProjectGroupDTO `json:"byProject"`
	ProjectsTruncated bool                     `json:"projectsTruncated"`
}

type metricsStartedOwnedDTO struct {
	All             metricsCountDTO        `json:"all"`
	ByOwner         []metricsOwnerGroupDTO `json:"byOwner"`
	OwnersTruncated bool                   `json:"ownersTruncated"`
}

type metricsChildStatusDTO struct {
	Status string          `json:"status"`
	Tasks  metricsCountDTO `json:"tasks"`
}

type metricsProjectRowDTO struct {
	Project  metricsProjectDTO       `json:"project"`
	Children []metricsChildStatusDTO `json:"children"`
}

type metricsProjectsDTO struct {
	Items     []metricsProjectRowDTO `json:"items"`
	Truncated bool                   `json:"truncated"`
}

type metricsWorkersDTO struct {
	Active  metricsCountDTO `json:"active"`
	Idle    metricsCountDTO `json:"idle"`
	Overdue metricsCountDTO `json:"overdue"`
	Inop    metricsCountDTO `json:"inop"`
}

type metricsFlowDTO struct {
	Completed metricsCountDTO `json:"completed"`
	Started   metricsCountDTO `json:"started"`
}

type metricsWindowDTO struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// metricsDTO mirrors TeamMetrics field for field. startedOwned, projects and
// flow are null for a reader who may see the roster but not the App's tasks.
type metricsDTO struct {
	AppID        string                  `json:"appId"`
	AsOf         string                  `json:"asOf"`
	Window       metricsWindowDTO        `json:"window"`
	StartedOwned *metricsStartedOwnedDTO `json:"startedOwned"`
	Projects     *metricsProjectsDTO     `json:"projects"`
	Workers      metricsWorkersDTO       `json:"workers"`
	Flow         *metricsFlowDTO         `json:"flow"`
}

const teamMetricsHelp = `Show a team's computed task and worker flow: the server's teamMetrics
reports, rendered as returned. The CLI adds up, regroups and filters nothing.

Each count comes with the ids behind it: --ids lists them. A list holds at
most 200 ids, and a longer one reads "first 200 of N".

The reports:
  Started owned tasks   tasks in the STARTED status, grouped by their owner
                        (never their assignee) and then by project; "No
                        project" is the server's bucket for tasks under none.
                        It counts only started tasks that track external work
                        (a task with an issue reference or a source), so its
                        total is not all started tasks.
  Started projects      each STARTED project with its direct child tasks
                        counted by status. The server sends no project
                        total, so the heading says how many are shown.
  Workers               active, idle, overdue and inop. A worker can be in
                        several, so the four never add up to a head count.
  Flow in the window    tasks completed and tasks started in [since, until),
                        counting every task, owned or not. A cancelled task
                        is never completed.

The first three are snapshots of now: the window applies to the flow only.
--since and --until take RFC 3339 times, since included and until not. Left
out, the server reads the last 7 days up to now, and the window printed is the
one the server used. A window longer than 31 days is refused by the server
(exit 2); since after until is refused here, before any request.

A report this caller may not see (the three task reports, for someone who may
read the roster but not the App's tasks) prints "not visible to you" and is
null in --json, never zero. An App this caller cannot read is answered as a
missing App (exit 4).

--json prints the server's shape field for field: lists are [] when empty and
a hidden report is null.

Exit codes: 0 read; 2 a bad flag, a window the server refuses, or a server
that does not serve teamMetrics (one from before 2026-11-22); 4 no such App
or not readable; 1 a server fault.`

func newCmdTeamMetrics(f *cmdutil.Factory) *cobra.Command {
	var since, until string
	var ids bool
	cmd := &cobra.Command{
		Use:   "metrics [--since <time>] [--until <time>] [--ids]",
		Short: "A team's task and worker flow, as the server computes it",
		Long:  teamMetricsHelp,
		Example: `  hadron team metrics
  hadron team metrics --since 2026-11-02T00:00:00Z --until 2026-11-09T00:00:00Z
  hadron team metrics --ids --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var sinceArg, untilArg *string
			var sinceAt, untilAt time.Time
			if cmd.Flags().Changed("since") {
				t, err := time.Parse(time.RFC3339, since)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--since takes an RFC 3339 time such as 2026-11-02T00:00:00Z, got %q", since)
				}
				sinceAt, sinceArg = t, &since
			}
			if cmd.Flags().Changed("until") {
				t, err := time.Parse(time.RFC3339, until)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--until takes an RFC 3339 time such as 2026-11-09T00:00:00Z, got %q", until)
				}
				untilAt, untilArg = t, &until
			}
			if sinceArg != nil && untilArg != nil && sinceAt.After(untilAt) {
				return exitcode.Newf(exitcode.Usage, "--since %s is after --until %s", since, until)
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamMetrics(ctx, client, scope.Ref, sinceArg, untilArg)
			if err != nil {
				if api.IsFrameworkValidationOnlyFor(err, "teamMetrics") {
					return api.WithServerCode(exitcode.Newf(exitcode.Usage,
						"this server does not serve teamMetrics (it predates 2026-11-22); nothing was read"), err)
				}
				return api.MapErrorKeepingCode(err)
			}
			if resp.TeamMetrics == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no metrics (teamMetrics was null)")
			}
			dto := metricsFromGen(resp.TeamMetrics)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderMetrics(w, dto, ids)
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "start of the flow window, an RFC 3339 time (default: 7 days before --until)")
	cmd.Flags().StringVar(&until, "until", "", "end of the flow window, an RFC 3339 time, not included (default: now)")
	cmd.Flags().BoolVar(&ids, "ids", false, "list the ids behind each count")
	return cmd
}

func countFromGen(c gen.TeamMetricsCountFields) metricsCountDTO {
	out := metricsCountDTO{Total: c.Total, IDs: []string{}, Truncated: c.Truncated}
	out.IDs = append(out.IDs, c.Ids...)
	return out
}

func projectFromGen(p gen.TeamMetricsProjectFields) metricsProjectDTO {
	return metricsProjectDTO{Handle: p.Handle, NoProject: p.NoProject, TaskID: p.TaskId, Title: p.Title}
}

// metricsFromGen copies the answer into the command's own DTO, so --json does
// not move when the generated client does. A missing nested count (the schema
// makes none optional) reads as an empty one rather than a crash. The lists are
// [X!]! in the SDL, so a nil list entry is unreachable from a conforming server;
// the loops skip one rather than crash, and cannot make a truncated list read as
// whole (truncation is its own flag, copied as sent).
func metricsFromGen(m *gen.TeamMetricsTeamMetrics) metricsDTO {
	empty := metricsCountDTO{IDs: []string{}}
	dto := metricsDTO{AppID: m.AppId, AsOf: m.AsOf}
	if m.Window != nil {
		dto.Window = metricsWindowDTO{Since: m.Window.Since, Until: m.Window.Until}
	}
	if so := m.StartedOwned; so != nil {
		out := &metricsStartedOwnedDTO{All: empty, ByOwner: []metricsOwnerGroupDTO{}, OwnersTruncated: so.OwnersTruncated}
		if so.All != nil {
			out.All = countFromGen(so.All.TeamMetricsCountFields)
		}
		for _, g := range so.ByOwner {
			if g == nil {
				continue
			}
			og := metricsOwnerGroupDTO{Tasks: empty, ByProject: []metricsProjectGroupDTO{}, ProjectsTruncated: g.ProjectsTruncated}
			if g.Owner != nil {
				og.Owner = &metricsOwnerDTO{ID: g.Owner.Id, Kind: string(g.Owner.Kind), Name: g.Owner.Name, URN: g.Owner.Urn}
			}
			if g.Tasks != nil {
				og.Tasks = countFromGen(g.Tasks.TeamMetricsCountFields)
			}
			for _, pg := range g.ByProject {
				if pg == nil {
					continue
				}
				row := metricsProjectGroupDTO{Tasks: empty}
				if pg.Project != nil {
					row.Project = projectFromGen(pg.Project.TeamMetricsProjectFields)
				}
				if pg.Tasks != nil {
					row.Tasks = countFromGen(pg.Tasks.TeamMetricsCountFields)
				}
				og.ByProject = append(og.ByProject, row)
			}
			out.ByOwner = append(out.ByOwner, og)
		}
		dto.StartedOwned = out
	}
	if pr := m.Projects; pr != nil {
		out := &metricsProjectsDTO{Items: []metricsProjectRowDTO{}, Truncated: pr.Truncated}
		for _, it := range pr.Items {
			if it == nil {
				continue
			}
			row := metricsProjectRowDTO{Children: []metricsChildStatusDTO{}}
			if it.Project != nil {
				row.Project = projectFromGen(it.Project.TeamMetricsProjectFields)
			}
			for _, ch := range it.Children {
				if ch == nil {
					continue
				}
				c := metricsChildStatusDTO{Status: string(ch.Status), Tasks: empty}
				if ch.Tasks != nil {
					c.Tasks = countFromGen(ch.Tasks.TeamMetricsCountFields)
				}
				row.Children = append(row.Children, c)
			}
			out.Items = append(out.Items, row)
		}
		dto.Projects = out
	}
	dto.Workers = metricsWorkersDTO{Active: empty, Idle: empty, Overdue: empty, Inop: empty}
	if w := m.Workers; w != nil {
		if w.Active != nil {
			dto.Workers.Active = countFromGen(w.Active.TeamMetricsCountFields)
		}
		if w.Idle != nil {
			dto.Workers.Idle = countFromGen(w.Idle.TeamMetricsCountFields)
		}
		if w.Overdue != nil {
			dto.Workers.Overdue = countFromGen(w.Overdue.TeamMetricsCountFields)
		}
		if w.Inop != nil {
			dto.Workers.Inop = countFromGen(w.Inop.TeamMetricsCountFields)
		}
	}
	if fl := m.Flow; fl != nil {
		out := &metricsFlowDTO{Completed: empty, Started: empty}
		if fl.Completed != nil {
			out.Completed = countFromGen(fl.Completed.TeamMetricsCountFields)
		}
		if fl.Started != nil {
			out.Started = countFromGen(fl.Started.TeamMetricsCountFields)
		}
		dto.Flow = out
	}
	return dto
}

func renderMetrics(w io.Writer, dto metricsDTO, ids bool) error {
	var sb strings.Builder
	line := func(indent int, format string, a ...any) {
		sb.WriteString(strings.Repeat("  ", indent))
		fmt.Fprintf(&sb, format, a...)
		sb.WriteString("\n")
	}
	// idLine lists a count's ids under it (--ids): a cut-short list says it is partial.
	idLine := func(indent int, c metricsCountDTO) {
		if !ids || len(c.IDs) == 0 {
			return
		}
		safe := make([]string, len(c.IDs))
		for i, id := range c.IDs {
			safe[i] = output.SafeCell(id)
		}
		label := "ids"
		if c.Truncated {
			label = fmt.Sprintf("ids (first %d of %d)", len(c.IDs), c.Total)
		}
		line(indent, "%s: %s", label, strings.Join(safe, " "))
	}
	projectLabel := func(p metricsProjectDTO) string {
		name := output.SafeCell(p.Handle)
		if p.NoProject {
			name = "No project"
			if p.Handle != "" {
				name += " (" + output.SafeCell(p.Handle) + ")"
			}
		} else if name == "" {
			name = "(no handle)"
		}
		if p.Title != nil && *p.Title != "" {
			name += " " + output.SafeCell(*p.Title)
		}
		if ids && p.TaskID != nil {
			name += " [" + output.SafeCell(*p.TaskID) + "]"
		}
		return name
	}

	line(0, "Team metrics for app %s, as of %s", output.SafeCell(dto.AppID), output.SafeCell(dto.AsOf))
	line(0, "")

	if so := dto.StartedOwned; so == nil {
		line(0, "Started owned tasks: %s", metricsNotVisible)
	} else {
		line(0, "Started owned tasks: %d   (only started tasks that track external work)", so.All.Total)
		idLine(1, so.All)
		for _, g := range so.ByOwner {
			owner := "(no owner shown)"
			if g.Owner != nil {
				owner = strOrDash(g.Owner.Name) + " (" + strings.ToLower(output.SafeCell(g.Owner.Kind)) + ")"
				owner = output.SafeCell(owner)
			}
			line(1, "%s: %d", owner, g.Tasks.Total)
			idLine(2, g.Tasks)
			for _, pg := range g.ByProject {
				line(2, "%s: %d", projectLabel(pg.Project), pg.Tasks.Total)
				idLine(3, pg.Tasks)
			}
			if g.ProjectsTruncated {
				line(2, "(more projects than shown for this owner)")
			}
		}
		if so.OwnersTruncated {
			line(1, "(more owners than shown)")
		}
	}
	line(0, "")

	if pr := dto.Projects; pr == nil {
		line(0, "Started projects: %s", metricsNotVisible)
	} else {
		// The projects report carries no total (cor:agt:150), so this is how many the CLI is showing, never the
		// server's count: a truncated report says "(more projects than shown)" below.
		line(0, "Started projects: %d shown", len(pr.Items))
		for _, it := range pr.Items {
			parts := make([]string, 0, len(it.Children))
			for _, ch := range it.Children {
				parts = append(parts, fmt.Sprintf("%s %d", output.SafeCell(ch.Status), ch.Tasks.Total))
			}
			counts := strings.Join(parts, ", ")
			if counts == "" {
				counts = "no child tasks"
			}
			line(1, "%s: %s", projectLabel(it.Project), counts)
			for _, ch := range it.Children {
				if ids && len(ch.Tasks.IDs) > 0 {
					line(2, "%s", output.SafeCell(ch.Status))
					idLine(3, ch.Tasks)
				}
			}
		}
		if pr.Truncated {
			line(1, "(more projects than shown)")
		}
	}
	line(0, "")

	wk := dto.Workers
	line(0, "Workers: active %d, idle %d, overdue %d, inop %d   (a worker can be in several)",
		wk.Active.Total, wk.Idle.Total, wk.Overdue.Total, wk.Inop.Total)
	for _, row := range []struct {
		name string
		c    metricsCountDTO
	}{{"active", wk.Active}, {"idle", wk.Idle}, {"overdue", wk.Overdue}, {"inop", wk.Inop}} {
		if ids && len(row.c.IDs) > 0 {
			line(1, "%s", row.name)
			idLine(2, row.c)
		}
	}
	line(0, "")

	window := fmt.Sprintf("[%s, %s)", output.SafeCell(dto.Window.Since), output.SafeCell(dto.Window.Until))
	if fl := dto.Flow; fl == nil {
		line(0, "Flow in the window %s: %s", window, metricsNotVisible)
	} else {
		line(0, "Flow in the window %s: completed %d, started %d", window, fl.Completed.Total, fl.Started.Total)
		if ids && len(fl.Completed.IDs) > 0 {
			line(1, "completed")
			idLine(2, fl.Completed)
		}
		if ids && len(fl.Started.IDs) > 0 {
			line(1, "started")
			idLine(2, fl.Started)
		}
	}
	_, err := io.WriteString(w, sb.String())
	return err
}
