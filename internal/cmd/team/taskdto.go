package team

// The stable --json shapes for the task-surface commands (#830). Explicit
// DTO structs, never genqlient types, so regeneration cannot move the public
// contract; every list is initialized to []T{} so an empty field renders as
// [] rather than null.

import (
	"encoding/json"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// taskPartyDTO is the sanitized party projection: a worker's name and URN, or
// a person's handle. Never an account object.
type taskPartyDTO struct {
	ID   string  `json:"id"`
	Kind string  `json:"kind"`
	Name *string `json:"name"`
	Urn  *string `json:"urn"`
}

type taskRequirementsDTO struct {
	Role             *string  `json:"role"`
	FamilyIn         []string `json:"familyIn"`
	FamilyNotIn      []string `json:"familyNotIn"`
	ExcludeWorkerIds []string `json:"excludeWorkerIds"`
}

type taskResultDTO struct {
	Outcome  string  `json:"outcome"`
	Summary  *string `json:"summary"`
	Evidence *string `json:"evidence"`
}

// taskDTO is one task. OpenAlarms is set only by `task tree`, which joins the
// unacknowledged alarms onto the nodes it renders; omitempty keeps the other
// commands' shapes identical to the plain task read.
type taskDTO struct {
	ID                   string               `json:"id"`
	AppID                string               `json:"appId"`
	Title                string               `json:"title"`
	Description          *string              `json:"description"`
	Status               string               `json:"status"`
	Queued               bool                 `json:"queued"`
	WaitingOnChildren    bool                 `json:"waitingOnChildren"`
	Owner                *taskPartyDTO        `json:"owner"`
	Assignee             *taskPartyDTO        `json:"assignee"`
	AssignedBy           *taskPartyDTO        `json:"assignedBy"`
	CreatedBy            *taskPartyDTO        `json:"createdBy"`
	ParentTaskID         *string              `json:"parentTaskId"`
	Tags                 []string             `json:"tags"`
	Source               *string              `json:"source"`
	StartBy              *string              `json:"startBy"`
	CreatedAt            string               `json:"createdAt"`
	UpdatedAt            *string              `json:"updatedAt"`
	StartedAt            *string              `json:"startedAt"`
	FinishedAt           *string              `json:"finishedAt"`
	CancelledAt          *string              `json:"cancelledAt"`
	CloseSessionWhenDone bool                 `json:"closeSessionWhenDone"`
	Requirements         *taskRequirementsDTO `json:"requirements"`
	Result               *taskResultDTO       `json:"result"`
	OpenAlarms           *int                 `json:"openAlarms,omitempty"`
}

// taskPartyGetter is the one shape every generated party projection shares;
// the getters exist on each wrapper (use_struct_references gives them all the
// same method set).
type taskPartyGetter interface {
	GetId() string
	GetKind() gen.AgentTaskPartyKind
	GetName() *string
	GetUrn() *string
}

func partyDTO[T taskPartyGetter](p T) *taskPartyDTO {
	var zero T
	if any(p) == any(zero) {
		return nil
	}
	return &taskPartyDTO{
		ID:   p.GetId(),
		Kind: string(p.GetKind()),
		Name: p.GetName(),
		Urn:  p.GetUrn(),
	}
}

func taskRequirementsDTOFrom(r *gen.AgentTaskFieldsRequirementsAgentTaskRequirements) *taskRequirementsDTO {
	if r == nil {
		return nil
	}
	dto := &taskRequirementsDTO{
		Role:             r.Role,
		FamilyIn:         []string{},
		FamilyNotIn:      []string{},
		ExcludeWorkerIds: []string{},
	}
	if r.FamilyIn != nil {
		dto.FamilyIn = r.FamilyIn
	}
	if r.FamilyNotIn != nil {
		dto.FamilyNotIn = r.FamilyNotIn
	}
	if r.ExcludeWorkerIds != nil {
		dto.ExcludeWorkerIds = r.ExcludeWorkerIds
	}
	return dto
}

func taskResultDTOFrom(r *gen.AgentTaskFieldsResultAgentTaskResult) *taskResultDTO {
	if r == nil {
		return nil
	}
	return &taskResultDTO{Outcome: r.Outcome, Summary: r.Summary, Evidence: r.Evidence}
}

func taskDTOFrom(fields gen.AgentTaskFields) taskDTO {
	dto := taskDTO{
		ID:                   fields.Id,
		AppID:                fields.AppId,
		Title:                fields.Title,
		Description:          fields.Description,
		Status:               string(fields.Status),
		Queued:               fields.Queued,
		WaitingOnChildren:    fields.WaitingOnChildren,
		Owner:                partyDTO(fields.Owner),
		Assignee:             partyDTO(fields.Assignee),
		AssignedBy:           partyDTO(fields.AssignedBy),
		CreatedBy:            partyDTO(fields.CreatedBy),
		ParentTaskID:         fields.ParentTaskId,
		Tags:                 []string{},
		Source:               fields.Source,
		StartBy:              fields.StartBy,
		CreatedAt:            fields.CreatedAt,
		UpdatedAt:            fields.UpdatedAt,
		StartedAt:            fields.StartedAt,
		FinishedAt:           fields.FinishedAt,
		CancelledAt:          fields.CancelledAt,
		CloseSessionWhenDone: fields.CloseSessionWhenDone,
		Requirements:         taskRequirementsDTOFrom(fields.Requirements),
		Result:               taskResultDTOFrom(fields.Result),
	}
	if fields.Tags != nil {
		dto.Tags = fields.Tags
	}
	return dto
}

// ---- events ---------------------------------------------------------------

type alarmWakeDTO struct {
	ChainIndex int           `json:"chainIndex"`
	DueAt      string        `json:"dueAt"`
	Owner      *taskPartyDTO `json:"owner"`
	WokenAt    *string       `json:"wokenAt"`
}

type teamEventDTO struct {
	ID             string          `json:"id"`
	AppID          string          `json:"appId"`
	TaskID         *string         `json:"taskId"`
	Type           string          `json:"type"`
	Severity       *string         `json:"severity"`
	CreatedAt      string          `json:"createdAt"`
	Actor          *taskPartyDTO   `json:"actor"`
	ActorRunID     *string         `json:"actorRunId"`
	SubjectWorker  *taskPartyDTO   `json:"subjectWorker"`
	Payload        json.RawMessage `json:"payload"`
	AcknowledgedAt *string         `json:"acknowledgedAt"`
	AcknowledgedBy *taskPartyDTO   `json:"acknowledgedBy"`
	EpisodeStart   *string         `json:"episodeStart"`
	Chain          json.RawMessage `json:"chain"`
	Wakes          []alarmWakeDTO  `json:"wakes"`
}

func teamEventDTOFrom(e *gen.AgentTeamEventFields) teamEventDTO {
	dto := teamEventDTO{
		ID:             e.Id,
		AppID:          e.AppId,
		TaskID:         e.TaskId,
		Type:           e.Type,
		CreatedAt:      e.CreatedAt,
		Actor:          partyDTO(e.Actor),
		ActorRunID:     e.ActorRunId,
		SubjectWorker:  partyDTO(e.SubjectWorker),
		Payload:        json.RawMessage("null"),
		AcknowledgedAt: e.AcknowledgedAt,
		AcknowledgedBy: partyDTO(e.AcknowledgedBy),
		EpisodeStart:   e.EpisodeStart,
		Chain:          json.RawMessage("null"),
		Wakes:          []alarmWakeDTO{},
	}
	if e.Severity != nil {
		s := string(*e.Severity)
		dto.Severity = &s
	}
	if e.Payload != nil {
		dto.Payload = e.Payload
	}
	if e.Chain != nil {
		dto.Chain = *e.Chain
	}
	for _, w := range e.Wakes {
		if w == nil {
			continue
		}
		wake := alarmWakeDTO{
			ChainIndex: w.ChainIndex,
			DueAt:      w.DueAt,
			Owner:      partyDTO(w.Owner),
			WokenAt:    w.WokenAt,
		}
		dto.Wakes = append(dto.Wakes, wake)
	}
	return dto
}

// ---- roster ---------------------------------------------------------------

type rosterEntryDTO struct {
	Worker              taskPartyDTO     `json:"worker"`
	State               string           `json:"state"`
	Role                *string          `json:"role"`
	Bound               bool             `json:"bound"`
	LiveSession         bool             `json:"liveSession"`
	Family              *string          `json:"family"`
	FreeForWork         bool             `json:"freeForWork"`
	FreeFamilies        []string         `json:"freeFamilies"`
	LaunchableFamilies  []string         `json:"launchableFamilies"`
	UnavailableFamilies []string         `json:"unavailableFamilies"`
	UnavailableReason   *string          `json:"unavailableReason"`
	UnavailableUntil    *string          `json:"unavailableUntil"`
	LastActiveAt        *string          `json:"lastActiveAt"`
	CheckStatus         *channelCheckDTO `json:"checkStatus"`
	LastAssignedAt      *string          `json:"lastAssignedAt"`
	PendingTasks        []string         `json:"pendingTasks"`
	StartedTasks        []string         `json:"startedTasks"`
	WaitingOn           []string         `json:"waitingOn"`
	WaitingSince        *string          `json:"waitingSince"`
	UnreactedChildTask  *string          `json:"unreactedChildTask"`
	Alarms              []string         `json:"alarms"`
	ReportsTo           *taskPartyDTO    `json:"reportsTo"`
	ReportsToRetired    bool             `json:"reportsToRetired"`
}

type rosterDTO struct {
	AppID   string           `json:"appId"`
	AsOf    string           `json:"asOf"`
	Workers []rosterEntryDTO `json:"workers"`
}

func strListOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func reasonPtr(r *gen.AvailabilityReason) *string {
	if r == nil {
		return nil
	}
	s := string(*r)
	return &s
}

// ---- availability ---------------------------------------------------------

type workerAvailabilityDTO struct {
	Worker     taskPartyDTO  `json:"worker"`
	State      string        `json:"state"`
	Reason     *string       `json:"reason"`
	Until      *string       `json:"until"`
	SetAt      string        `json:"setAt"`
	SetBy      string        `json:"setBy"`
	SetByParty *taskPartyDTO `json:"setByParty"`
	InEffect   bool          `json:"inEffect"`
}

type familyAvailabilityDTO struct {
	AppID    string        `json:"appId"`
	Family   string        `json:"family"`
	State    string        `json:"state"`
	Reason   *string       `json:"reason"`
	Until    *string       `json:"until"`
	SetAt    string        `json:"setAt"`
	SetBy    *taskPartyDTO `json:"setBy"`
	InEffect bool          `json:"inEffect"`
}

type teamAvailabilityDTO struct {
	Workers  []workerAvailabilityDTO `json:"workers"`
	Families []familyAvailabilityDTO `json:"families"`
}
