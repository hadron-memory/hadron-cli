package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// Explicit DTOs keep the page contract independent of generated client types.
type registerEntryDTO struct {
	Code      string  `json:"code"`
	State     string  `json:"state"`
	ActionURN *string `json:"actionUrn"`
	EntryURN  *string `json:"entryUrn"`
	DefinedBy *string `json:"definedBy"`
	TaskLoc   *string `json:"taskLoc"`
	TaskURN   *string `json:"taskUrn"`
}
type registerPageDTO struct {
	Entries []registerEntryDTO `json:"entries"`
	Total   int                `json:"total"`
	HasMore bool               `json:"hasMore"`
}

func newCmdTeamRegister(f *cmdutil.Factory) *cobra.Command {
	var agent, taskLoc, code string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Read the action register's code-to-task index",
		Long: `Read one page of the team's action register, resolved by the server through
an Agent's chain with your own read access. This is read-only: it never runs
a task, writes an entry, installs anything or caches the answer.

--app selects the team App; otherwise the configured App or worktree binding
is used. --agent names an Agent installed in that App or cast there as a
worker. With no --agent the CLI omits it: the server uses the authenticated
worker's or run's Agent. A person or App key without --agent gets BAD_USER_INPUT
(exit 2). Unknown, foreign or unusable Agents give AGENT_NOT_FOUND (exit 4),
and unknown, foreign or non-participant Apps give APP_NOT_FOUND (exit 4).

--task-loc and --code filter the index. MAPPED names the runnable task;
UNRESOLVED names the loc where no runnable task was found; UNMAPPED marks a
malformed entry. Empty pages disclose no reason. The server orders entries
by code. Omitted paging flags use its defaults (20 rows, offset 0); limits
above 200 are sent unchanged and capped by the server. --limit 0 returns
only the count. Use --offset for the next page; the summary shows total and
whether more rows exist. --json returns {entries, total, hasMore}, preserving
all server values and nullable fields. Human cells are terminal-safe and
bounded as described by team --help. Requires server#3312 or later.`,
		Example: `  hadron team register --app acme.com:eng --agent hrn:agent:acme.com:coder
  hadron team register --code task.finish.open-children --json
  hadron team register --task-loc tasks:finish-a-task --limit 0`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate only flag shape, never infer principals or reimplement gates.
			for _, flag := range []string{"agent", "task-loc", "code"} {
				if cmd.Flags().Changed(flag) {
					v, _ := cmd.Flags().GetString(flag)
					if strings.TrimSpace(v) == "" {
						return exitcode.Newf(exitcode.Usage, "--%s must not be empty", flag)
					}
				}
			}
			if cmd.Flags().Changed("limit") && limit < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit must be at least 0")
			}
			if cmd.Flags().Changed("offset") && offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--offset must be at least 0")
			}
			b, err := readBindingOrNilWithApp(cmd.Context(), f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(cmd.Context(), f, b)
			if err != nil {
				return err
			}
			var a, l, c *string
			var n, o *int
			if cmd.Flags().Changed("agent") {
				a = &agent
			}
			if cmd.Flags().Changed("task-loc") {
				l = &taskLoc
			}
			if cmd.Flags().Changed("code") {
				c = &code
			}
			if cmd.Flags().Changed("limit") {
				n = &limit
			}
			if cmd.Flags().Changed("offset") {
				o = &offset
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamActionRegisterIndex(cmd.Context(), client, scope.Ref, a, l, c, n, o)
			if err != nil {
				return api.WithServerCode(prefixedRefusal(err, []string{"APP_NOT_FOUND", "AGENT_NOT_FOUND", "BAD_USER_INPUT"}), err)
			}
			page := resp.ActionRegisterIndex
			dto := registerPageDTO{Entries: []registerEntryDTO{}, Total: page.Total, HasMore: page.HasMore}
			for _, e := range page.Entries {
				dto.Entries = append(dto.Entries, registerEntryDTO{Code: e.Code, State: string(e.State), ActionURN: e.ActionUrn, EntryURN: e.EntryUrn, DefinedBy: e.DefinedBy, TaskLoc: e.TaskLoc, TaskURN: e.TaskUrn})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", output.SafeCell(describeApp(cmd.Context(), f, scope.Ref)), scope.Source); err != nil {
					return err
				}
				if len(dto.Entries) == 0 {
					if _, err := fmt.Fprintln(w, "no register entries returned"); err != nil {
						return err
					}
				} else {
					t := output.NewTable(w, "CODE", "STATE", "TASK / REASON", "ACTION", "ENTRY", "DEFINED BY")
					safe := func(p *string) string {
						if p == nil {
							return "-"
						}
						return output.SafeCell(*p)
					}
					for _, e := range dto.Entries {
						task := safe(e.TaskURN)
						switch e.State {
						case "UNRESOLVED":
							task = "no runnable task found at " + safe(e.TaskLoc)
						case "UNMAPPED":
							task = "entry is malformed"
						}
						t.Row(output.SafeCell(e.Code), output.SafeCell(e.State), output.SafeCell(task), safe(e.ActionURN), safe(e.EntryURN), safe(e.DefinedBy))
					}
					if err := t.Flush(); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "%d shown of %d; more: %t\n", len(dto.Entries), dto.Total, dto.HasMore)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Asking Agent id or URN; omitted uses the server's authenticated worker/run Agent")
	cmd.Flags().StringVar(&taskLoc, "task-loc", "", "Only entries naming this task loc")
	cmd.Flags().StringVar(&code, "code", "", "Only this register code")
	cmd.Flags().IntVar(&limit, "limit", 20, "Page size (0=count only, server cap 200); unset uses server default")
	cmd.Flags().IntVar(&offset, "offset", 0, "Page offset; unset uses server default")
	return cmd
}
