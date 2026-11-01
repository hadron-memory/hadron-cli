package team

// The stable --json shapes for the task-surface commands (#830). Explicit
// DTO structs, never genqlient types, so regeneration cannot move the public
// contract; every list is initialized to []T{} so an empty field renders as
// [] rather than null.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
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
	MinTier          *string  `json:"minTier"`
	Role             *string  `json:"role"`
	FamilyIn         []string `json:"familyIn"`
	FamilyNotIn      []string `json:"familyNotIn"`
	ModelGroupIn     []string `json:"modelGroupIn"`
	ModelGroupNotIn  []string `json:"modelGroupNotIn"`
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
	Priority          int                    `json:"priority"`
	Blocked           bool                   `json:"blocked"`
	BlockedBy         *taskDependencyPageDTO `json:"blockedBy"`
	Blocks            *taskDependencyPageDTO `json:"blocks"`
	ID                string                 `json:"id"`
	Handle            string                 `json:"handle"`
	TaskNumber        string                 `json:"taskNumber"`
	AppID             string                 `json:"appId"`
	Title             string                 `json:"title"`
	Description       *string                `json:"description"`
	Status            string                 `json:"status"`
	Queued            bool                   `json:"queued"`
	WaitingOnChildren bool                   `json:"waitingOnChildren"`
	Owner             *taskPartyDTO          `json:"owner"`
	Assignee          *taskPartyDTO          `json:"assignee"`
	AssignedBy        *taskPartyDTO          `json:"assignedBy"`
	CreatedBy         *taskPartyDTO          `json:"createdBy"`
	ParentTaskID      *string                `json:"parentTaskId"`
	Tags              []string               `json:"tags"`
	Source            *string                `json:"source"`
	// ExternalID is the outside item the task claims (cli#1524, cor:agt:120:02):
	// set once at create, never edited; null when the task claims none.
	ExternalID           *string              `json:"externalId"`
	References           []taskReferenceDTO   `json:"references"`
	HandBack             *taskHandBackDTO     `json:"handBack"`
	StartBy              *string              `json:"startBy"`
	CreatedAt            string               `json:"createdAt"`
	UpdatedAt            *string              `json:"updatedAt"`
	StartedAt            *string              `json:"startedAt"`
	FinishedAt           *string              `json:"finishedAt"`
	CancelledAt          *string              `json:"cancelledAt"`
	CloseSessionWhenDone bool                 `json:"closeSessionWhenDone"`
	Requirements         *taskRequirementsDTO `json:"requirements"`
	Result               *taskResultDTO       `json:"result"`
	// Properties is the task's OWN optional JSON object (#2420): never
	// inherited, so a child shows its own value, and null when unset.
	Properties json.RawMessage `json:"properties"`
	OpenAlarms *int            `json:"openAlarms,omitempty"`
}

// taskRef is how a human-readable line names a task: the server's short handle
// (AT3.405) first, then the canonical id, which stays the identity. A server
// that returns no handle keeps the id alone. The handle is server text and
// goes through the same sanitiser as every other rendered value.
func taskRef(handle, id string) string {
	if handle == "" {
		return id
	}
	return output.SafeCell(handle) + " " + id
}

// taskHandleCell is the HANDLE column of a task table.
func taskHandleCell(handle string) string {
	return output.SafeCell(handle)
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
		MinTier:          r.MinTier,
		FamilyIn:         []string{},
		FamilyNotIn:      []string{},
		ModelGroupIn:     strListOrEmpty(r.ModelGroupIn),
		ModelGroupNotIn:  strListOrEmpty(r.ModelGroupNotIn),
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

// taskHandBackDTO is the task's hand-back (cor:agt:050:29): null when the task
// was never handed back. text is another worker's free text, stored and
// returned verbatim; only the human view sanitises it.
type taskHandBackDTO struct {
	Code         string  `json:"code"`
	Text         *string `json:"text"`
	HandedBackAt string  `json:"handedBackAt"`
}

func taskHandBackDTOFrom(h *gen.AgentTaskFieldsHandBackAgentTaskHandBack) *taskHandBackDTO {
	if h == nil {
		return nil
	}
	return &taskHandBackDTO{Code: string(h.Code), Text: h.Text, HandedBackAt: h.HandedBackAt}
}

// handBackLine is the one-line human view: "Handed back: <code> at <time>[: <text>]".
// The text goes through the same sanitiser as the other server values, and the
// trailing ": <text>" is dropped when nothing printable is left.
func handBackLine(h *taskHandBackDTO) string {
	line := "Handed back: " + output.SafeCell(h.Code) + " at " + output.SafeCell(h.HandedBackAt)
	if h.Text != nil {
		if text := strings.TrimSpace(output.SafeLine(*h.Text, 2048)); text != "" {
			line += ": " + text
		}
	}
	return line
}

func taskResultDTOFrom(r *gen.AgentTaskFieldsResultAgentTaskResult) *taskResultDTO {
	if r == nil {
		return nil
	}
	return &taskResultDTO{Outcome: r.Outcome, Summary: r.Summary, Evidence: r.Evidence}
}

func taskDTOFrom(fields gen.AgentTaskFields) taskDTO {
	dto := taskDTO{Priority: fields.Priority, Blocked: fields.Blocked,
		ID:                   fields.Id,
		Handle:               fields.Handle,
		TaskNumber:           fields.TaskNumber,
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
		ExternalID:           fields.ExternalId,
		References:           taskReferencesDTOFrom(fields.References),
		HandBack:             taskHandBackDTOFrom(fields.HandBack),
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
	if p := fields.BlockedBy; p != nil {
		dto.BlockedBy = dependencyPage(p.Total, p.HasMore, p.Items)
	}
	if p := fields.Blocks; p != nil {
		dto.Blocks = dependencyPage(p.Total, p.HasMore, p.Items)
	}
	if fields.Tags != nil {
		dto.Tags = fields.Tags
	}
	if fields.Properties != nil {
		dto.Properties = *fields.Properties
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
	Worker          taskPartyDTO `json:"worker"`
	State           string       `json:"state"`
	Role            *string      `json:"role"`
	Bound           bool         `json:"bound"`
	LiveSession     bool         `json:"liveSession"`
	Family          *string      `json:"family"`
	Model           *string      `json:"model"`
	ModelReportedAt *string      `json:"modelReportedAt"`
	Tier            *string      `json:"tier"`
	// ModelGroups are the server's live matching model-group names (cli#1485
	// S3; advisory, never authorization). [] is a real answer (no model or no
	// group matches); null means the lookup failed for this row, with
	// ModelGroupsError the server's error code (never its message).
	ModelGroups         []string           `json:"modelGroups"`
	ModelGroupsError    *string            `json:"modelGroupsError"`
	FreeForWork         bool               `json:"freeForWork"`
	FreeFamilies        []string           `json:"freeFamilies"`
	LaunchableFamilies  []string           `json:"launchableFamilies"`
	UnavailableFamilies []string           `json:"unavailableFamilies"`
	UnavailableReason   *string            `json:"unavailableReason"`
	UnavailableUntil    *string            `json:"unavailableUntil"`
	LastActiveAt        *string            `json:"lastActiveAt"`
	CheckStatus         *channelCheckDTO   `json:"checkStatus"`
	Terminal            *workerTerminalDTO `json:"terminal"`
	LastAssignedAt      *string            `json:"lastAssignedAt"`
	PendingTasks        []string           `json:"pendingTasks"`
	StartedTasks        []string           `json:"startedTasks"`
	WaitingOn           []string           `json:"waitingOn"`
	WaitingSince        *string            `json:"waitingSince"`
	UnreactedChildTask  *string            `json:"unreactedChildTask"`
	Alarms              []string           `json:"alarms"`
	ReportsTo           *taskPartyDTO      `json:"reportsTo"`
	ReportsToRetired    bool               `json:"reportsToRetired"`
	HeldBy              *rosterHolderDTO   `json:"heldBy"`
	HeldAt              *string            `json:"heldAt"`
}

// rosterHolderDTO is the sanitized person holding a worker's name (server#2223).
// Name is null both when the server withheld it and when it is unset; the CLI
// renders what it is given and adds no masking of its own (cli#1032).
type rosterHolderDTO struct {
	ID     string  `json:"id"`
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
	Name   *string `json:"name"`
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

// safeTaskError keeps the mapped error's chain and exit code but renders its
// text through SafeCell: a task refusal quotes server-supplied handles (the
// bare-handle qualification refusal names the handle), and the root renderer
// prints err.Error() raw, so ESC, bidi and newlines would otherwise reach the
// terminal. Unwrap keeps errors.As/Is (details, codes) working; the --json envelope
// uses Error() and still carries the original text (cli#1131).
// taskErrorMaxRunes bounds a rendered refusal; room for the schema-skew hint.
const taskErrorMaxRunes = 1024

type safeTaskError struct{ err error }

// Error stays raw (the --json envelope's message); HumanMessage is what the
// terminal branch of the root renderer prints.
func (e safeTaskError) Error() string { return e.err.Error() }
func (e safeTaskError) HumanMessage() string {
	return output.SafeLine(e.err.Error(), taskErrorMaxRunes)
}
func (e safeTaskError) Unwrap() error { return e.err }

// taskErrorCodes are the public refusal codes shown as the message prefix: every
// TASK_ code the schema documents on the task mutations, plus BAD_USER_INPUT
// (#1285). Add a new TASK_ code here when the server documents one.
var taskErrorCodes = []string{
	"TASK_BLOCKED", "TASK_BLOCKER_CYCLE", "TASK_NOT_FOUND", "TASK_NOT_OWNER",
	"TASK_INVALID_TRANSITION", "TASK_HANDLE_QUALIFY", "BAD_USER_INPUT",
	"TASK_ENDED", "TASK_NOT_ASSIGNEE", "TASK_CHILD_OWNER_MISMATCH",
	"TASK_OWNER_INVALID", "TASK_ASSIGNEE_INVALID", "TASK_STOPPED",
	"PROPERTIES_NOT_AN_OBJECT", "PROPERTIES_TOO_LARGE",
	"PROPERTIES_SCHEMA_VIOLATION", "PROPERTIES_SCHEMA_UNUSABLE",
}

// mapTaskError is api.MapError for the task commands: the server owns range,
// state, authority and cycle decisions, so its public code is preserved beside
// the mapped message, and the human message is made terminal-safe.
func mapTaskError(err error) error {
	// The server's code rides on the --json envelope as serverCode (cli#1524):
	// a script tells TASK_EXTERNAL_ID_TAKEN from another conflict by it.
	return api.WithServerCode(mapTaskErrorMessage(err), err)
}

func mapTaskErrorMessage(err error) error {
	mapped := api.MapError(err)
	if mapped == nil {
		return nil
	}
	// The prefix goes INSIDE the terminal-safe wrapper: the root renderer
	// prints the innermost HumanMessage, so a prefix added outside it would
	// show in --json and vanish on the terminal. The codes are constants, never
	// server text.
	prefix := ""
	for _, code := range taskErrorCodes {
		if api.HasErrorCode(err, code) {
			prefix = code
			break
		}
	}
	var coded *exitcode.CodedError
	if errors.As(mapped, &coded) {
		inner := coded.Err
		if prefix != "" {
			inner = fmt.Errorf("%s: %w", prefix, inner)
		}
		return &exitcode.CodedError{Code: coded.Code, Err: safeTaskError{inner}}
	}
	if prefix != "" {
		mapped = fmt.Errorf("%s: %w", prefix, mapped)
	}
	return safeTaskError{mapped}
}

// handleHeaders and handleCells add the HANDLE column only when at least one
// task carries a handle, so a server that returns none keeps the table it had.
func handleHeaders(any bool, headers ...string) []string {
	if any {
		return append([]string{"HANDLE"}, headers...)
	}
	return headers
}

func handleCells(any bool, handle string, cells ...string) []string {
	if any {
		return append([]string{taskHandleCell(handle)}, cells...)
	}
	return cells
}
