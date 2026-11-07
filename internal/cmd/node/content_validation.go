package node

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
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type validationReceiptDTO struct {
	NodeID   string              `json:"nodeId"`
	Replayed bool                `json:"replayed"`
	Report   validationReportDTO `json:"report"`
	State    string              `json:"state"`
}

type validationTask struct {
	id, ref  string
	revision int
}

func validationTasksFrom(set *gen.ContentValidationNodeNodeValidationTasksNodeValidationTaskSet) ([]validationTask, error) {
	malformed := func() ([]validationTask, error) {
		return nil, exitcode.Newf(exitcode.Error, "invalid validation task-set response; no report sent")
	}
	if set == nil {
		return nil, exitcode.Newf(exitcode.Forbidden, "validation task set is unavailable for this caller; no report sent")
	}
	if set.Tasks == nil {
		return malformed()
	}
	tasks := []validationTask{}
	seen := map[string]validationTask{}
	broken := false
	for _, entry := range set.Tasks {
		if entry == nil || entry.Broken == nil {
			return malformed()
		}
		if *entry.Broken {
			if entry.TaskId != nil || entry.TaskRef != nil || entry.Revision != nil {
				return malformed()
			}
			broken = true
			continue
		}
		if entry.TaskId == nil || strings.TrimSpace(*entry.TaskId) == "" || entry.TaskRef == nil || !strings.HasPrefix(*entry.TaskRef, "hrn:node:") || entry.Revision == nil || *entry.Revision < 1 {
			return malformed()
		}
		task := validationTask{id: *entry.TaskId, ref: *entry.TaskRef, revision: *entry.Revision}
		if prior, ok := seen[task.id]; ok {
			if prior != task {
				return malformed()
			}
			continue
		}
		seen[task.id] = task
		tasks = append(tasks, task)
	}
	switch set.State {
	case gen.NodeValidationTaskSetStateReady:
		if broken || len(tasks) == 0 {
			return malformed()
		}
		return tasks, nil
	case gen.NodeValidationTaskSetStateNotConfigured:
		if len(set.Tasks) != 0 {
			return malformed()
		}
		return tasks, nil
	case gen.NodeValidationTaskSetStateBroken:
		if !broken {
			return malformed()
		}
		return nil, validationRefusal("CONTENT_VALIDATION_TASK_BROKEN", "The validation task of this node's rule is unavailable, so no report is recorded until a manager of the memory repairs the rule.")
	case gen.NodeValidationTaskSetStateUnavailable:
		if len(set.Tasks) != 0 {
			return malformed()
		}
		return nil, exitcode.Newf(exitcode.Forbidden, "validation task set is unavailable for this caller; no report sent")
	default:
		return malformed()
	}
}

func newCmdValidation(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "validation", Short: "Record an advisory content-validation result"}
	var memory, outcome, taskChoice, runID string
	cmd := &cobra.Command{
		Use:   "record <node-ref> --outcome PASS|FAIL|ERROR",
		Short: "Record a validation task's result for the current node revision",
		Long: `Name the node by ID or full node URN, or use a bare loc with -m/--memory.
Record the result of a validation task you already ran. This command does
not run the task or edit the node. It reads the current node and task revisions;
record before either changes. The server derives the reporter and checks access,
task selection and authorship. CHECKED compares recorded authors, not independence.

Discovery uses the server's validation task set for this caller. An unavailable
or non-applicable set exits 8; it never guesses from configuration or past reports.
One effective task is selected automatically. Multiple tasks are listed with
revisions: choose one with --task (ID or the listed URN). A choice outside that
set exits 2 before any write. --run-id is passed through for server idempotency;
a replay is success. No refusal or uncertain write is retried automatically.
The JSON receipt contains nodeId, replayed, report and state. Exit 4 means
missing/unreadable node; 8 means access, reporter or author refusal; 2 means
invalid input/task/revision; 5 means configuration or revision/run-ID conflict.
When no task is configured, exit 5 occurs without a write. Exit 7 means outcome unknown: inspect
the node before retrying. The result is advisory, not approval or independence.`,
		Example: `  hadron node validation record instructions -m example.com:kb --outcome PASS --run-id check-42
  hadron node validation record hrn:node:example.com:kb:instructions --outcome FAIL --task <task-id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if outcome != "PASS" && outcome != "FAIL" && outcome != "ERROR" {
				return exitcode.Newf(exitcode.Usage, "--outcome must be PASS, FAIL or ERROR")
			}
			if cmd.Flags().Changed("task") && strings.TrimSpace(taskChoice) == "" {
				return exitcode.Newf(exitcode.Usage, "--task must name a task in the effective set")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			nr, err := gen.ContentValidationNode(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if nr.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node not found")
			}
			n := nr.Node
			if n.Id == "" || n.Revision < 1 {
				return exitcode.Newf(exitcode.Error, "invalid node revision response; no report sent")
			}
			tasks, err := validationTasksFrom(n.ValidationTasks)
			if err != nil {
				return err
			}
			// #1706 author/gate fold: no sentinel task or invented revision.
			if len(tasks) == 0 {
				if taskChoice != "" {
					return exitcode.Newf(exitcode.Usage, "--task is not in the effective validation task set; no report sent")
				}
				return validationRefusal("CONTENT_VALIDATION_NOT_CONFIGURED", "No validation task is configured for this node's role.")
			}
			chosen := tasks[0]
			if taskChoice != "" {
				found := false
				for _, task := range tasks {
					if taskChoice == task.id || taskChoice == task.ref {
						chosen = task
						found = true
						break
					}
				}
				if !found {
					return exitcode.Newf(exitcode.Usage, "--task is not in the effective validation task set; no report sent")
				}
			} else if len(tasks) > 1 {
				choices := []string{}
				for _, task := range tasks {
					choices = append(choices, fmt.Sprintf("%s [id %s] (revision %d)", output.SafeLine(task.ref, 256), output.SafeLine(task.id, 256), task.revision))
				}
				return exitcode.Newf(exitcode.Usage, "multiple validation tasks apply: %s; choose one with --task; no report sent", strings.Join(choices, ", "))
			}
			var run *string
			if cmd.Flags().Changed("run-id") {
				run = &runID
			}
			response, err := gen.RecordContentValidation(cmd.Context(), client, n.Id, n.Revision, chosen.id, chosen.revision, gen.NodeContentValidationOutcome(outcome), run)
			if err != nil {
				return api.MapError(err)
			}
			r := response.RecordNodeContentValidationReport
			if r == nil || r.Report == nil || r.NodeId != n.Id || r.Report.NodeRevision < 1 || r.Report.ReportedAt == "" || r.Report.Outcome == "" || r.State == "" {
				return exitcode.Newf(exitcode.Unavailable, "invalid report receipt; outcome is unknown; verify before retrying")
			}
			check := (*string)(nil)
			if r.Report.AuthorCheck != nil {
				s := string(*r.Report.AuthorCheck)
				check = &s
			}
			dto := validationReceiptDTO{NodeID: r.NodeId, Replayed: r.Replayed, State: string(r.State), Report: validationReportDTO{NodeRevision: r.Report.NodeRevision, ValidationTaskRef: r.Report.ValidationTaskRef, ValidationTaskRevision: r.Report.ValidationTaskRevision, Outcome: string(r.Report.Outcome), ReportedAt: r.Report.ReportedAt, AuthorCheck: &check}}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				verb := "recorded"
				if dto.Replayed {
					verb = "replayed (nothing written)"
				}
				_, err := fmt.Fprintf(w, "validation report %s: %s for revision %d; current state %s%s (advisory)\n", verb, dto.Report.Outcome, dto.Report.NodeRevision, dto.State, authorComparison(&dto.Report))
				return err
			})
		}}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "Memory for a bare node loc")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Result of the validation task: PASS, FAIL or ERROR (required)")
	cmd.Flags().StringVar(&taskChoice, "task", "", "Choose an ID or listed URN from the effective task set")
	cmd.Flags().StringVar(&runID, "run-id", "", "Server idempotency key; passed through without retry")
	group.AddCommand(cmd)
	return group
}

func validationRefusal(code, message string) error {
	return api.MapError(&gqlerror.Error{Message: message, Extensions: map[string]any{"code": code}})
}
