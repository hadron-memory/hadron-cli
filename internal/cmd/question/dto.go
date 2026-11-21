package question

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// The --json shapes of the question group. They are a public contract, so they
// are explicit structs mapped from the generated types, never the generated
// types themselves. Lists are always [] (never null); a value the server leaves
// out (no answer yet, no approval yet) is null.

type actorRefDTO struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

// actorDTO is an authenticated actor: a user, a worker, an App, or several.
type actorDTO struct {
	UserID   *string      `json:"userId"`
	User     *actorRefDTO `json:"user"`
	WorkerID *string      `json:"workerId"`
	Worker   *actorRefDTO `json:"worker"`
	AppID    *string      `json:"appId"`
	App      *actorRefDTO `json:"app"`
}

// refDTO is exactly one of an external ref, a node id/URN or a spec citation.
type refDTO struct {
	Ref  *string `json:"ref"`
	Node *string `json:"node"`
	Spec *string `json:"spec"`
}

type identityMergeDTO struct {
	FromUserID string       `json:"fromUserId"`
	From       *actorRefDTO `json:"from"`
	ToUserID   string       `json:"toUserId"`
	To         *actorRefDTO `json:"to"`
	MergedAt   string       `json:"mergedAt"`
}

type optionDTO struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	WhyChoose *string `json:"whyChoose"`
	WhyNot    *string `json:"whyNot"`
	Risks     *string `json:"risks"`
}

type recommendationDTO struct {
	Key    string    `json:"key"`
	Reason *string   `json:"reason"`
	By     *actorDTO `json:"by"`
}

// answerDTO is one entry of the answer list: an answer, or a retraction that
// takes the standing answer back (kind says which). Appended, never edited.
type answerDTO struct {
	Kind     string       `json:"kind"`
	Key      *string      `json:"key"`
	Text     *string      `json:"text"`
	Reason   *string      `json:"reason"`
	ByUserID string       `json:"byUserId"`
	By       *actorRefDTO `json:"by"`
	At       string       `json:"at"`
	Via      string       `json:"via"`
}

type withdrawalDTO struct {
	At     string    `json:"at"`
	Reason string    `json:"reason"`
	By     *actorDTO `json:"by"`
}

type approvalDTO struct {
	ApprovedAt string       `json:"approvedAt"`
	Revision   int          `json:"revision"`
	Integrity  string       `json:"integrity"`
	ByDecider  bool         `json:"byDecider"`
	ByUserID   *string      `json:"byUserId"`
	By         *actorRefDTO `json:"by"`
}

// questionDTO is one question. revision is the node's current revision: the
// --expected-revision of the next amend, answer, retract or withdraw. state is
// derived by the server on every read; only DECIDED may be relied on as a ruling.
// answer is the standing answer or null; answers is every entry, oldest first.
// viewerMayAnswer is the server's word on whether the caller may answer or take
// back an answer right now (a decider acting as themselves).
type questionDTO struct {
	ID                     string             `json:"id"`
	URN                    string             `json:"urn"`
	Code                   string             `json:"code"`
	N                      int                `json:"n"`
	LogID                  *string            `json:"logId"`
	Title                  string             `json:"title"`
	Background             *string            `json:"background"`
	WhyNow                 *string            `json:"whyNow"`
	State                  string             `json:"state"`
	Tier                   string             `json:"tier"`
	Priority               string             `json:"priority"`
	Answered               bool               `json:"answered"`
	NeededBy               *string            `json:"neededBy"`
	DefaultAction          *string            `json:"defaultAction"`
	Authorizes             bool               `json:"authorizes"`
	Revision               int                `json:"revision"`
	PortalURL              *string            `json:"portalUrl"`
	ViewerIsDecider        bool               `json:"viewerIsDecider"`
	ViewerMayAnswer        bool               `json:"viewerMayAnswer"`
	TaskID                 *string            `json:"taskId"`
	AskedAt                *string            `json:"askedAt"`
	AnsweredAt             *string            `json:"answeredAt"`
	AskedBy                *actorDTO          `json:"askedBy"`
	AssigneeID             *string            `json:"assigneeId"`
	Assignee               *actorRefDTO       `json:"assignee"`
	EffectiveAssigneeID    *string            `json:"effectiveAssigneeId"`
	EffectiveAssignee      *actorRefDTO       `json:"effectiveAssignee"`
	DeciderIDs             []string           `json:"deciderIds"`
	Deciders               []actorRefDTO      `json:"deciders"`
	IdentityMerges         []identityMergeDTO `json:"identityMerges"`
	Options                []optionDTO        `json:"options"`
	Recommendation         *recommendationDTO `json:"recommendation"`
	NoRecommendationReason *string            `json:"noRecommendationReason"`
	Answer                 *answerDTO         `json:"answer"`
	Answers                []answerDTO        `json:"answers"`
	Blocks                 []refDTO           `json:"blocks"`
	Sources                []refDTO           `json:"sources"`
	SpecsTouched           []refDTO           `json:"specsTouched"`
	SpecsMustChange        []refDTO           `json:"specsMustChange"`
	Withdrawn              *withdrawalDTO     `json:"withdrawn"`
	Approval               *approvalDTO       `json:"approval"`
}

type purgeRecordDTO struct {
	Code        string       `json:"code"`
	At          string       `json:"at"`
	Reason      string       `json:"reason"`
	ContentHash string       `json:"contentHash"`
	ByUserID    string       `json:"byUserId"`
	By          *actorRefDTO `json:"by"`
}

// questionLogDTO is a log's settings. revision is the --expected-revision of the
// next set-deciders.
type questionLogDTO struct {
	ID                 string             `json:"id"`
	URN                string             `json:"urn"`
	Loc                string             `json:"loc"`
	Name               string             `json:"name"`
	Prefix             string             `json:"prefix"`
	LastCode           int                `json:"lastCode"`
	Revision           int                `json:"revision"`
	PortalURL          *string            `json:"portalUrl"`
	MemoryID           string             `json:"memoryId"`
	DefaultDeciderIDs  []string           `json:"defaultDeciderIds"`
	DefaultDeciders    []actorRefDTO      `json:"defaultDeciders"`
	EligibleDeciderIDs []string           `json:"eligibleDeciderIds"`
	EligibleDeciders   []actorRefDTO      `json:"eligibleDeciders"`
	IdentityMerges     []identityMergeDTO `json:"identityMerges"`
	Purges             []purgeRecordDTO   `json:"purges"`
}

type questionPageDTO struct {
	Total int           `json:"total"`
	Items []questionDTO `json:"items"`
}

type verificationDTO struct {
	State             string   `json:"state"`
	Revision          int      `json:"revision"`
	ApprovedRevision  *int     `json:"approvedRevision"`
	ApprovalIntegrity string   `json:"approvalIntegrity"`
	ApprovalByDecider bool     `json:"approvalByDecider"`
	Codec             *string  `json:"codec"`
	Renderer          *string  `json:"renderer"`
	Findings          []string `json:"findings"`
}

type purgeDTO struct {
	Ref    string `json:"ref"`
	Purged bool   `json:"purged"`
}

// --- mapping ---

func actorRef(r *gen.QuestionActorRefFields) *actorRefDTO {
	if r == nil {
		return nil
	}
	return &actorRefDTO{ID: r.Id, Name: r.Name, Handle: r.Handle, URN: r.Urn}
}

// actorRefs maps a list of generated wrappers that each embed the actor-ref
// fragment; get returns that fragment (nil entries are skipped).
func actorRefs[T any](in []*T, get func(*T) *gen.QuestionActorRefFields) []actorRefDTO {
	out := []actorRefDTO{}
	for _, x := range in {
		if x == nil {
			continue
		}
		if r := actorRef(get(x)); r != nil {
			out = append(out, *r)
		}
	}
	return out
}

func actor(a *gen.QuestionActorFields) *actorDTO {
	if a == nil {
		return nil
	}
	d := &actorDTO{UserID: a.UserId, WorkerID: a.WorkerId, AppID: a.AppId}
	if a.User != nil {
		d.User = actorRef(&a.User.QuestionActorRefFields)
	}
	if a.Worker != nil {
		d.Worker = actorRef(&a.Worker.QuestionActorRefFields)
	}
	if a.App != nil {
		d.App = actorRef(&a.App.QuestionActorRefFields)
	}
	return d
}

func ref(r *gen.QuestionRefFields) refDTO {
	return refDTO{Ref: r.Ref, Node: r.Node, Spec: r.Spec}
}

func refs[T any](in []*T, get func(*T) *gen.QuestionRefFields) []refDTO {
	out := []refDTO{}
	for _, x := range in {
		if x == nil {
			continue
		}
		out = append(out, ref(get(x)))
	}
	return out
}

func identityMerges[T any](in []*T, get func(*T) *gen.QuestionIdentityMergeFields) []identityMergeDTO {
	out := []identityMergeDTO{}
	for _, x := range in {
		if x == nil {
			continue
		}
		m := get(x)
		d := identityMergeDTO{FromUserID: m.FromUserId, ToUserID: m.ToUserId, MergedAt: m.MergedAt}
		if m.From != nil {
			d.From = actorRef(&m.From.QuestionActorRefFields)
		}
		if m.To != nil {
			d.To = actorRef(&m.To.QuestionActorRefFields)
		}
		out = append(out, d)
	}
	return out
}

func answer(a *gen.QuestionAnswerFields) *answerDTO {
	if a == nil {
		return nil
	}
	d := &answerDTO{
		Kind: string(a.Kind), Key: a.Key, Text: a.Text, Reason: a.Reason,
		ByUserID: a.ByUserId, At: a.At, Via: string(a.Via),
	}
	if a.By != nil {
		d.By = actorRef(&a.By.QuestionActorRefFields)
	}
	return d
}

func questionFrom(q *gen.QuestionFields) questionDTO {
	out := questionDTO{
		ID: q.Id, URN: q.Urn, Code: q.Code, N: q.N, LogID: q.LogId, Title: q.Title,
		Background: q.Background, WhyNow: q.WhyNow,
		State: string(q.State), Tier: string(q.Tier), Priority: string(q.Priority), Answered: q.Answered,
		NeededBy: q.NeededBy, DefaultAction: q.DefaultAction,
		Authorizes: q.Authorizes, Revision: q.RevSeq, PortalURL: q.PortalUrl,
		ViewerIsDecider: q.ViewerIsDecider, ViewerMayAnswer: q.ViewerMayAnswer, TaskID: q.TaskId,
		AskedAt: q.AskedAt, AnsweredAt: q.AnsweredAt,
		AssigneeID: q.AssigneeId, EffectiveAssigneeID: q.EffectiveAssigneeId,
		NoRecommendationReason: q.NoRecommendationReason,
		DeciderIDs:             append([]string{}, q.DeciderIds...),
		Deciders: actorRefs(q.Deciders, func(x *gen.QuestionFieldsDecidersCommentActorRef) *gen.QuestionActorRefFields {
			return &x.QuestionActorRefFields
		}),
		IdentityMerges: identityMerges(q.IdentityMerges, func(x *gen.QuestionFieldsIdentityMergesQuestionIdentityMerge) *gen.QuestionIdentityMergeFields {
			return &x.QuestionIdentityMergeFields
		}),
		Options: []optionDTO{}, Answers: []answerDTO{},
		Blocks: refs(q.Blocks, func(x *gen.QuestionFieldsBlocksQuestionRef) *gen.QuestionRefFields {
			return &x.QuestionRefFields
		}),
		Sources: refs(q.Sources, func(x *gen.QuestionFieldsSourcesQuestionRef) *gen.QuestionRefFields {
			return &x.QuestionRefFields
		}),
		SpecsTouched: refs(q.SpecsTouched, func(x *gen.QuestionFieldsSpecsTouchedQuestionRef) *gen.QuestionRefFields {
			return &x.QuestionRefFields
		}),
		SpecsMustChange: refs(q.SpecsMustChange, func(x *gen.QuestionFieldsSpecsMustChangeQuestionRef) *gen.QuestionRefFields {
			return &x.QuestionRefFields
		}),
	}
	if q.AskedBy != nil {
		out.AskedBy = actor(&q.AskedBy.QuestionActorFields)
	}
	if q.Assignee != nil {
		out.Assignee = actorRef(&q.Assignee.QuestionActorRefFields)
	}
	if q.EffectiveAssignee != nil {
		out.EffectiveAssignee = actorRef(&q.EffectiveAssignee.QuestionActorRefFields)
	}
	for _, o := range q.Options {
		if o != nil {
			out.Options = append(out.Options, optionDTO{Key: o.Key, Label: o.Label, WhyChoose: o.WhyChoose, WhyNot: o.WhyNot, Risks: o.Risks})
		}
	}
	if r := q.Recommendation; r != nil {
		out.Recommendation = &recommendationDTO{Key: r.Key, Reason: &r.Reason}
		if r.By != nil {
			out.Recommendation.By = actor(&r.By.QuestionActorFields)
		}
	}
	if q.Answer != nil {
		out.Answer = answer(&q.Answer.QuestionAnswerFields)
	}
	for _, a := range q.Answers {
		if a != nil {
			out.Answers = append(out.Answers, *answer(&a.QuestionAnswerFields))
		}
	}
	if w := q.Withdrawn; w != nil {
		out.Withdrawn = &withdrawalDTO{At: w.At, Reason: w.Reason}
		if w.By != nil {
			out.Withdrawn.By = actor(&w.By.QuestionActorFields)
		}
	}
	if q.Evidence != nil && q.Evidence.Approval != nil {
		a := q.Evidence.Approval
		out.Approval = &approvalDTO{
			ApprovedAt: a.ApprovedAt, Revision: a.Revision, Integrity: a.Integrity,
			ByDecider: a.ByDecider, ByUserID: a.ByUserId,
		}
		if a.By != nil {
			out.Approval.By = actorRef(&a.By.QuestionActorRefFields)
		}
	}
	return out
}

func questionLogFrom(l *gen.QuestionLogFields) questionLogDTO {
	out := questionLogDTO{
		ID: l.Id, URN: l.Urn, Loc: l.Loc, Name: l.Name, Prefix: l.Prefix, LastCode: l.LastCode,
		Revision: l.RevSeq, PortalURL: l.PortalUrl, MemoryID: l.MemoryId,
		DefaultDeciderIDs:  append([]string{}, l.DefaultDeciderIds...),
		EligibleDeciderIDs: append([]string{}, l.EligibleDeciderIds...),
		DefaultDeciders: actorRefs(l.DefaultDeciders, func(x *gen.QuestionLogFieldsDefaultDecidersCommentActorRef) *gen.QuestionActorRefFields {
			return &x.QuestionActorRefFields
		}),
		EligibleDeciders: actorRefs(l.EligibleDeciders, func(x *gen.QuestionLogFieldsEligibleDecidersCommentActorRef) *gen.QuestionActorRefFields {
			return &x.QuestionActorRefFields
		}),
		IdentityMerges: identityMerges(l.IdentityMerges, func(x *gen.QuestionLogFieldsIdentityMergesQuestionIdentityMerge) *gen.QuestionIdentityMergeFields {
			return &x.QuestionIdentityMergeFields
		}),
		Purges: []purgeRecordDTO{},
	}
	for _, p := range l.Purges {
		if p == nil {
			continue
		}
		r := purgeRecordDTO{Code: p.Code, At: p.At, Reason: p.Reason, ContentHash: p.ContentHash, ByUserID: p.ByUserId}
		if p.By != nil {
			r.By = actorRef(&p.By.QuestionActorRefFields)
		}
		out.Purges = append(out.Purges, r)
	}
	return out
}
