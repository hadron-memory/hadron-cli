package team

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
	"io"
	"strings"
)

type sessionWorkItemDTO struct {
	NodeID     string           `json:"nodeId"`
	SessionID  string           `json:"sessionId"`
	WorkerID   *string          `json:"workerId"`
	WorkerName string           `json:"workerName"`
	Tool       string           `json:"tool"`
	Kind       string           `json:"kind"`
	Ref        string           `json:"ref"`
	Action     string           `json:"action"`
	At         string           `json:"at"`
	Model      *string          `json:"model"`
	Detail     *json.RawMessage `json:"detail"`
}
type sessionWorkPageDTO struct {
	Total int                  `json:"total"`
	Items []sessionWorkItemDTO `json:"items"`
}

func newCmdSessionWorkItems(f *cmdutil.Factory) *cobra.Command {
	var worker string
	var limit, offset int
	cmd := &cobra.Command{
		Use: "work-items --worker <ref> [--limit <n>] [--offset <n>]", Short: "Read one worker's worklog, including retired workers",
		Long: `Read one worker's recorded work in the team App. The server resolves its
name within the App, id or URN, including retired workers. Retirement ends a
casting, not its history. No roster lookup or client-side session fan-out.

App scope comes from --app, the App context, or this worktree's binding.
One server page is returned (default 50); --limit 0 asks for a count only.
JSON is {total, items}; total is the server's count under the worker filter.
Use the returned row count to advance --offset. A refusal never retries
without the worker filter.`,
		Example: "  hadron team session work-items --worker Iris --app acme.com:eng-team --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(worker) == "" {
				return exitcode.Newf(exitcode.Usage, "--worker is required and must not be empty")
			}
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
			}
			b, err := readBindingOrNilWithApp(cmd.Context(), f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(cmd.Context(), f, b)
			if err != nil {
				return err
			}
			c, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.TeamWorkItemsByWorker(cmd.Context(), c, scope.Ref, worker, nil, nil, nil, nil, &limit, &offset)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.TeamWorkItems == nil {
				return exitcode.Newf(exitcode.Error, "server returned no work-items page")
			}
			dto := sessionWorkPageDTO{Total: r.TeamWorkItems.Total, Items: []sessionWorkItemDTO{}}
			for _, row := range r.TeamWorkItems.Items {
				if row == nil {
					return exitcode.Newf(exitcode.Error, "server returned an empty work-item row")
				}
				v := row.TeamWorkItemFields
				dto.Items = append(dto.Items, sessionWorkItemDTO{NodeID: v.NodeId, SessionID: v.SessionId, WorkerID: v.WorkerId, WorkerName: v.WorkerName, Tool: v.Tool, Kind: v.Kind, Ref: v.Ref, Action: v.Action, At: v.At, Model: v.Model, Detail: v.Detail})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", output.SafeCell(scope.Ref), scope.Source); err != nil {
					return err
				}
				t := output.NewTable(w, "WORKER", "KIND", "REF", "ACTION", "AT", "SESSION")
				for _, v := range dto.Items {
					t.Row(output.SafeCell(v.WorkerName), output.SafeCell(v.Kind), output.SafeCell(v.Ref), output.SafeCell(v.Action), output.SafeCell(v.At), output.SafeCell(v.SessionID))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "%d returned; %d total\n", len(dto.Items), dto.Total)
				return err
			})
		}}
	cmd.Flags().StringVar(&worker, "worker", "", "worker name within the App, id or URN; retired workers are included")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum rows on this server page (0 is count only)")
	cmd.Flags().IntVar(&offset, "offset", 0, "matching rows to skip")
	return cmd
}
