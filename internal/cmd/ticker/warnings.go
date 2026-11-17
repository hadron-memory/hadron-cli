package ticker

// ticker create may SUCCEED with typed warnings in the response's
// extensions.warnings (server#2733: ticker.code.unmapped). Text mode prints
// them on stderr after the unchanged stdout receipt; --json adds an
// always-present `warnings` array beside the ticker. Code and text are the
// server's, shown verbatim, unknown codes included; the exit code never
// changes. The same shape as the task commands' warnings (cli#962).

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// warningRegisterDTO is where the App's register maps a warning's code.
type warningRegisterDTO struct {
	State   string  `json:"state"`
	TaskLoc *string `json:"taskLoc"`
	TaskURN *string `json:"taskUrn"`
}

// warningDTO is one warning; register is null when the server sent none.
type warningDTO struct {
	Code     string              `json:"code"`
	Text     string              `json:"text"`
	Register *warningRegisterDTO `json:"register"`
}

// createdDTO is create's --json shape: the ticker, plus its warnings ([] when
// none).
type createdDTO struct {
	tickerDTO
	Warnings []warningDTO `json:"warnings"`
}

// warningsFor projects the sink's warnings filed under one response key; we
// send no aliases, so the key is the mutation field's name.
func warningsFor(sink *api.WarningSink, key string) []warningDTO {
	out := []warningDTO{}
	for _, w := range sink.For(key) {
		dto := warningDTO{Code: w.Code, Text: w.Text}
		if w.Register != nil {
			dto.Register = &warningRegisterDTO{State: w.Register.State, TaskLoc: w.Register.TaskLoc, TaskURN: w.Register.TaskURN}
		}
		out = append(out, dto)
	}
	return out
}

func writeWarnings(w io.Writer, warnings []warningDTO) error {
	for _, x := range warnings {
		where := ""
		if x.Register != nil && x.Register.State == "mapped" && x.Register.TaskURN != nil {
			where = " → " + output.SafeCell(*x.Register.TaskURN)
		}
		if _, err := fmt.Fprintf(w, "warning: [%s] %s%s\n", output.SafeCell(x.Code), output.SafeCell(x.Text), where); err != nil {
			return err
		}
	}
	return nil
}
