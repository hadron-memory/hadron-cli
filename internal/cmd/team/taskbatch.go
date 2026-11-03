package team

// `hadron team task create --batch-file` (hadron-cli#1565, server#2653
// createAgentTasks, cor:agt:050:06): create 1-25 tasks in ONE call, all or none.
// The file holds a JSON array of items {task, blockedByPositions}. Each item's
// task is CreateAgentTaskInput exactly as the server takes it, so every
// matching field cli#1555/server#3245 owns reaches the batch with no
// batch-specific code; the CLI adds only the wrapper (positions, all-or-none).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// maxTaskBatch is createAgentTasks' bound (cor:agt:050:06). The CLI refuses 0
// or more than this before any call; TASK_BATCH_TOO_LARGE is the backstop.
const maxTaskBatch = 25

// batchItemShape is the typed view of one batch item, used to validate the
// file's keys and values against the generated input type. It is never sent:
// the generated CreateAgentTaskInput marshals an absent optional field as an
// explicit null, and a batch item must keep an omitted key omitted.
type batchItemShape struct {
	BlockedByPositions *[]*int                   `json:"blockedByPositions"`
	Task               *gen.CreateAgentTaskInput `json:"task"`
}

// readBatchFile reads --batch-file from a path, or "-" for piped stdin
// (refused from a terminal, where it would hang waiting).
func readBatchFile(ios *output.IOStreams, path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--batch-file needs a path, or - for piped stdin")
	}
	var data []byte
	var err error
	if path == "-" {
		if ios.IsInputTerminal() {
			return nil, exitcode.Newf(exitcode.Usage, "--batch-file - reads piped stdin; stdin is a terminal")
		}
		data, err = io.ReadAll(ios.In)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading --batch-file: %v", err)
	}
	return data, nil
}

// parseTaskBatch turns the file into the items to send. Unknown, mis-cased and
// duplicate keys are refused against the generated input's exact JSON names;
// every item must carry a task; the count is 1-25. Nothing here makes a call.
func parseTaskBatch(data []byte) ([]*gqltypes.CreateAgentTasksItemInput, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, exitcode.Newf(exitcode.Usage, "--batch-file must hold a JSON array of {task, blockedByPositions} items")
	}
	scan := json.NewDecoder(bytes.NewReader(trimmed))
	scan.UseNumber() // numbers stay text so an out-of-range one cannot end the scan early (#1558)
	if err := scanBatchKeys(cmdutil.CheckKeys(scan, reflect.TypeOf([]batchItemShape{}), "")); err != nil {
		return nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(trimmed, &raws); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "parsing --batch-file: %v", err)
	}
	if len(raws) == 0 {
		return nil, exitcode.Newf(exitcode.Usage, "--batch-file holds no tasks: a batch is 1 to %d", maxTaskBatch)
	}
	if len(raws) > maxTaskBatch {
		return nil, exitcode.Newf(exitcode.Usage, "--batch-file holds %d tasks: a batch is 1 to %d (split it)", len(raws), maxTaskBatch)
	}
	items := make([]*gqltypes.CreateAgentTasksItemInput, 0, len(raws))
	for i, raw := range raws {
		// A strict typed decode: a value of the wrong type is refused here, with
		// the item's position, instead of after a round trip.
		var shape batchItemShape
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&shape); err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "parsing --batch-file: task %d of %d: %v", i+1, len(raws), err)
		}
		if shape.Task == nil {
			return nil, exitcode.Newf(exitcode.Usage, "parsing --batch-file: task %d of %d has no \"task\" object", i+1, len(raws))
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "parsing --batch-file: task %d of %d: %v", i+1, len(raws), err)
		}
		task := bytes.TrimSpace(fields["task"])
		if len(task) == 0 || task[0] != '{' {
			return nil, exitcode.Newf(exitcode.Usage, "parsing --batch-file: task %d of %d: \"task\" must be an object", i+1, len(raws))
		}
		item := &gqltypes.CreateAgentTasksItemInput{Task: task}
		if shape.BlockedByPositions != nil {
			// A JSON null element would decode to 0 and be sent as a position:
			// refuse it here, as the invalid Int it is.
			positions := make([]int, 0, len(*shape.BlockedByPositions))
			for _, p := range *shape.BlockedByPositions {
				if p == nil {
					return nil, exitcode.Newf(exitcode.Usage, "parsing --batch-file: task %d of %d: blockedByPositions must hold integers, not null", i+1, len(raws))
				}
				positions = append(positions, *p)
			}
			item.BlockedByPositions = &positions
		}
		items = append(items, item)
	}
	return items, nil
}

// scanBatchKeys maps CheckKeys' result: a syntax error is left to the typed
// decode (which reports it), every other error refuses.
func scanBatchKeys(err error) error {
	var syn *json.SyntaxError
	switch {
	case err == nil, errors.As(err, &syn), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return nil
	default:
		return exitcode.Newf(exitcode.Usage, "parsing --batch-file: %v", err)
	}
}

// taskBatchError carries a batch refusal's 1-based position: text adds
// "(task n of m)", and the --json error envelope adds `position`.
type taskBatchError struct {
	err   error
	pos   int
	total int
}

func (e *taskBatchError) suffix() string { return fmt.Sprintf(" (task %d of %d)", e.pos, e.total) }
func (e *taskBatchError) Error() string  { return e.err.Error() + e.suffix() }
func (e *taskBatchError) Unwrap() error  { return e.err }
func (e *taskBatchError) HumanMessage() string {
	var h interface{ HumanMessage() string }
	if errors.As(e.err, &h) {
		return h.HumanMessage() + e.suffix()
	}
	return e.Error()
}

// BatchPosition is the refused task's 1-based position in the call.
func (e *taskBatchError) BatchPosition() int { return e.pos }

// mapTaskBatchError maps a refused createAgentTasks call: the usual task
// mapping and server code, plus the position when the server named one.
func mapTaskBatchError(err error, total int) error {
	mapped := mapTaskError(err)
	if pos, ok := api.ExtensionInt(err, "position"); ok && pos >= 1 && mapped != nil {
		var coded *exitcode.CodedError
		if errors.As(mapped, &coded) {
			mapped = &exitcode.CodedError{Code: coded.Code, Err: &taskBatchError{err: coded.Err, pos: pos, total: total}}
		} else {
			mapped = &taskBatchError{err: mapped, pos: pos, total: total}
		}
	}
	return api.WithServerCode(mapped, err)
}

// taskBatchWarningDTO is one warning with the 1-based position of the task it
// belongs to (the server files them under "createAgentTasks.<position>");
// position is null for a warning that names none.
type taskBatchWarningDTO struct {
	Position *int `json:"position"`
	taskWarningDTO
}

// taskBatchDTO is the --json shape of a batch create: the tasks in list order
// and the warnings by position ([] when none).
type taskBatchDTO struct {
	Tasks    []taskDTO             `json:"tasks"`
	Warnings []taskBatchWarningDTO `json:"warnings"`
}

func taskBatchWarnings(sink *api.WarningSink) []taskBatchWarningDTO {
	out := []taskBatchWarningDTO{}
	for _, w := range sink.All() {
		rest, ok := strings.CutPrefix(w.Path, "createAgentTasks")
		if !ok {
			continue
		}
		dto := taskBatchWarningDTO{taskWarningDTO: taskWarningsFrom(w)}
		if n, err := strconv.Atoi(strings.TrimPrefix(rest, ".")); err == nil && n > 0 {
			dto.Position = &n
		}
		out = append(out, dto)
	}
	return out
}

// runTaskBatchCreate is `task create --batch-file`: exactly one
// createAgentTasks call, never retried (the call is not idempotent).
func runTaskBatchCreate(cmd *cobra.Command, f *cmdutil.Factory, path string) error {
	data, err := readBatchFile(f.IOStreams, path)
	if err != nil {
		return err
	}
	items, err := parseTaskBatch(data)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
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
	mctx, sink := api.WithWarningSink(ctx)
	resp, err := gen.AgentTaskCreateBatch(mctx, client, scope.Ref, items)
	if err != nil {
		return mapTaskBatchError(err, len(items))
	}
	dto := taskBatchDTO{Tasks: []taskDTO{}, Warnings: taskBatchWarnings(sink)}
	for _, t := range resp.CreateAgentTasks {
		dto.Tasks = append(dto.Tasks, taskDTOFrom(t.AgentTaskFields))
	}
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "✓ created %d tasks\n", len(dto.Tasks)); err != nil {
			return err
		}
		for i, t := range dto.Tasks {
			if _, err := fmt.Fprintf(w, "  %d. %s (%s) %s\n", i+1, taskRef(t.Handle, t.ID), t.Status, output.SafeLine(t.Title, 200)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if f.JSON {
		return nil
	}
	for _, w := range dto.Warnings {
		at := ""
		if w.Position != nil {
			at = fmt.Sprintf(" (task %d)", *w.Position)
		}
		if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "warning: [%s] %s%s\n", output.SafeCell(w.Code), output.SafeCell(w.Text), at); err != nil {
			return err
		}
	}
	return nil
}
