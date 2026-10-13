package gqltypes

import "encoding/json"

// AgentTaskStallScheduleInput is bound rather than generated (#864) because
// its wire contract has THREE states per field and genqlient's optional
// pointer has two: the server reads an OMITTED field as "keep the current
// value" and an explicit null as "return it to the platform default"
// (hadron-server #1706/#1863). Each field is a non-pointer json.RawMessage
// with omitempty: a nil (len 0) value is dropped from the wire, the 4-byte
// `null` survives as null, and a number is sent as itself — the same
// reasoning as NodeWhereInput's operands.
type AgentTaskStallScheduleInput struct {
	AssignmentMinutes    json.RawMessage `json:"assignmentMinutes,omitempty"`
	HostLapseCheckIns    json.RawMessage `json:"hostLapseCheckIns,omitempty"`
	NoProgressMinutes    json.RawMessage `json:"noProgressMinutes,omitempty"`
	NotStartedMinutes    json.RawMessage `json:"notStartedMinutes,omitempty"`
	OwnerReactionMinutes json.RawMessage `json:"ownerReactionMinutes,omitempty"`
}
