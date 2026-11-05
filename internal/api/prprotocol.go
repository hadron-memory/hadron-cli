package api

import (
	"encoding/json"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// PRSlotFailure is the server's failing-slot projection, not a client readiness decision.
type PRSlotFailure struct {
	Slot          string  `json:"slot"`
	CurrentTaskID *string `json:"currentTaskId"`
	State         string  `json:"state"`
	Verdict       *string `json:"verdict"`
	Head          *string `json:"head"`
	Counts        bool    `json:"counts"`
}

type prProtocolError struct {
	error
	code  string
	slots []PRSlotFailure
}

func (e *prProtocolError) Unwrap() error                     { return e.error }
func (e *prProtocolError) ServerCode() string                { return e.code }
func (e *prProtocolError) PRReadinessSlots() []PRSlotFailure { return e.slots }
func (e *prProtocolError) HumanMessage() string {
	text := e.code + ": " + output.SafeCell(e.Error())
	for _, s := range e.slots {
		cell := func(value *string) string {
			if value == nil {
				return "-"
			}
			return output.SafeCell(*value)
		}
		counts := "does not count"
		if s.Counts {
			counts = "counts"
		}
		text += "\nslot " + output.SafeCell(s.Slot) + ": " + output.SafeCell(s.State) + "; seat " + cell(s.CurrentTaskID) + "; verdict " + cell(s.Verdict) + "; head " + cell(s.Head) + " (" + counts + ")"
	}
	return text
}

// MapPRProtocolError preserves recognized protocol conflicts' provider message
// and code and exposes only the typed PR_NOT_READY slot fields. Those conflict
// chains omit raw errors/extensions; other errors retain the existing mapper.
func MapPRProtocolError(err error) error {
	if err == nil {
		return nil
	}
	code := ServerCodeOf(err)
	conflict := false
	switch code {
	case "REVIEW_SEAT_NOT_SUPERSEDABLE", "REVIEW_SEAT_ALREADY_ASSIGNED", "REVIEW_SEAT_SUPERSEDES_REQUIRED", "REVIEW_HEAD_MISMATCH", "REVIEW_SEAT_UNASSIGNED", "PR_NOT_READY", "PR_HEAD_NOT_FROZEN", "REVIEW_SCHEDULE_MISSING", "REVIEW_SLOT_UNKNOWN", "REVIEW_SEAT_SUPERSEDE_INVALID", "PR_REFERENCE_MISSING":
		conflict = true
	}
	if !conflict {
		return MapErrorKeepingCode(err)
	}
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != code {
			continue
		}
		mapped := &prProtocolError{error: exitcode.Newf(exitcode.Conflict, "%s", e.Message), code: code}
		if code == "PR_NOT_READY" {
			mapped.slots = []PRSlotFailure{}
			if data, marshalErr := json.Marshal(e.Extensions["slots"]); marshalErr == nil && strings.HasPrefix(string(data), "[") {
				var slots []PRSlotFailure
				if json.Unmarshal(data, &slots) == nil {
					mapped.slots = slots
				}
			}
		}
		return mapped
	}
	return MapErrorKeepingCode(err)
}
