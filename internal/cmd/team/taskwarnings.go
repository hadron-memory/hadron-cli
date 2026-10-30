package team

// Action-register warnings (cli#962, server#2198/#2207): task create, assign,
// reassign and finish — and since cli#1283 start, progress, cancel, park and
// ready — may SUCCEED with typed warnings in the response's
// extensions.warnings. Text mode prints them on stderr, after the unchanged
// stdout receipt; --json adds an always-present `warnings` array beside the
// task. The server's code and text are shown verbatim, unknown codes
// included; the exit code is unchanged.

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// taskWarningRegisterDTO is where the App's register maps a warning's code:
// state mapped (taskLoc, taskUrn), unmapped or unresolved.
type taskWarningRegisterDTO struct {
	State   string  `json:"state"`
	TaskLoc *string `json:"taskLoc"`
	TaskURN *string `json:"taskUrn"`
}

// taskWarningDTO is one warning; register is null when the server sent none
// (a caller with no Agent).
type taskWarningDTO struct {
	Code     string                  `json:"code"`
	Text     string                  `json:"text"`
	Register *taskWarningRegisterDTO `json:"register"`
}

// taskMutationDTO is the --json shape of the warning-bearing verbs: the
// task object, plus its warnings ([] when none).
type taskMutationDTO struct {
	taskDTO
	Warnings []taskWarningDTO `json:"warnings"`
}

// taskWarningsFor projects the sink's warnings for one mutation's response
// key; we send no aliases, so the key is the mutation field's name.
func taskWarningsFor(sink *api.WarningSink, key string) []taskWarningDTO {
	out := []taskWarningDTO{}
	for _, w := range sink.For(key) {
		dto := taskWarningDTO{Code: w.Code, Text: w.Text}
		if w.Register != nil {
			dto.Register = &taskWarningRegisterDTO{State: w.Register.State, TaskLoc: w.Register.TaskLoc, TaskURN: w.Register.TaskURN}
		}
		out = append(out, dto)
	}
	return out
}

// writeTaskMutation renders a warning-bearing verb's result: the receipt (or
// the JSON object) exactly as before, then, in text mode, one stderr line per
// warning.
func writeTaskMutation(f *cmdutil.Factory, task taskDTO, warnings []taskWarningDTO, receipt func(io.Writer) error) error {
	if err := output.Write(f.IOStreams, f.JSON, taskMutationDTO{taskDTO: task, Warnings: warnings}, receipt); err != nil {
		return err
	}
	if f.JSON {
		return nil
	}
	for _, w := range warnings {
		where := ""
		if w.Register != nil {
			switch {
			case w.Register.State == "mapped" && w.Register.TaskURN != nil:
				where = " → " + output.SafeCell(*w.Register.TaskURN)
			case w.Register.State == "unmapped":
				where = " (no task mapped)"
			}
		}
		if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "warning: [%s] %s%s\n", output.SafeCell(w.Code), output.SafeCell(w.Text), where); err != nil {
			return err
		}
	}
	return nil
}
