package decision

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// The --json shapes of the decision group. They are a public contract, so
// they are explicit structs mapped from the generated types, never the
// generated types themselves. Lists are always [] (never null); a value the
// server leaves out (an unanswered part, no approval yet) is null.

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
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Meaning *string `json:"meaning"`
	Cost    *string `json:"cost"`
}

type recommendationDTO struct {
	Key          string    `json:"key"`
	Reason       *string   `json:"reason"`
	AttributedTo *string   `json:"attributedTo"`
	By           *actorDTO `json:"by"`
}

type answerDTO struct {
	At               string  `json:"at"`
	ByUserID         string  `json:"byUserId"`
	RecordedByUserID *string `json:"recordedByUserId"`
	ClaimedBy        *string `json:"claimedBy"`
	Freeform         bool    `json:"freeform"`
	Key              *string `json:"key"`
	Text             *string `json:"text"`
	Reason           *string `json:"reason"`
	Via              string  `json:"via"`
	Source           *refDTO `json:"source"`
}

type partDTO struct {
	N                      int                `json:"n"`
	Citation               string             `json:"citation"`
	Question               string             `json:"question"`
	Options                []optionDTO        `json:"options"`
	Recommendation         *recommendationDTO `json:"recommendation"`
	NoRecommendationReason *string            `json:"noRecommendationReason"`
	Answer                 *answerDTO         `json:"answer"`
	AnswerHistory          []answerDTO        `json:"answerHistory"`
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

// decisionDTO is one decision. revision is the node's current revision: the
// --expected-revision of the next amend or withdraw. state is derived by the
// server on every read; only DECIDED may be relied on as a ruling.
type decisionDTO struct {
	ID              string             `json:"id"`
	URN             string             `json:"urn"`
	Code            string             `json:"code"`
	N               int                `json:"n"`
	LogID           *string            `json:"logId"`
	Title           string             `json:"title"`
	Context         *string            `json:"context"`
	State           string             `json:"state"`
	Tier            string             `json:"tier"`
	Urgency         string             `json:"urgency"`
	Authorizes      bool               `json:"authorizes"`
	Archival        bool               `json:"archival"`
	Revision        int                `json:"revision"`
	PortalURL       *string            `json:"portalUrl"`
	ViewerIsDecider bool               `json:"viewerIsDecider"`
	AskedAt         *string            `json:"askedAt"`
	AnsweredAt      *string            `json:"answeredAt"`
	AskedBy         *actorDTO          `json:"askedBy"`
	DeciderIDs      []string           `json:"deciderIds"`
	Deciders        []actorRefDTO      `json:"deciders"`
	IdentityMerges  []identityMergeDTO `json:"identityMerges"`
	Parts           []partDTO          `json:"parts"`
	Blocks          []refDTO           `json:"blocks"`
	FollowUps       []refDTO           `json:"followUps"`
	Sources         []refDTO           `json:"sources"`
	Withdrawn       *withdrawalDTO     `json:"withdrawn"`
	Approval        *approvalDTO       `json:"approval"`
}

type purgeRecordDTO struct {
	Code        string       `json:"code"`
	At          string       `json:"at"`
	Reason      string       `json:"reason"`
	ContentHash string       `json:"contentHash"`
	ByUserID    string       `json:"byUserId"`
	By          *actorRefDTO `json:"by"`
}

// decisionLogDTO is a log's settings. revision is the --expected-revision of
// the next set-deciders.
type decisionLogDTO struct {
	ID                 string             `json:"id"`
	URN                string             `json:"urn"`
	Loc                string             `json:"loc"`
	Name               string             `json:"name"`
	Prefix             string             `json:"prefix"`
	LastCode           int                `json:"lastCode"`
	Revision           int                `json:"revision"`
	PortalURL          *string            `json:"portalUrl"`
	DefaultDeciderIDs  []string           `json:"defaultDeciderIds"`
	DefaultDeciders    []actorRefDTO      `json:"defaultDeciders"`
	EligibleDeciderIDs []string           `json:"eligibleDeciderIds"`
	EligibleDeciders   []actorRefDTO      `json:"eligibleDeciders"`
	IdentityMerges     []identityMergeDTO `json:"identityMerges"`
	Purges             []purgeRecordDTO   `json:"purges"`
}

type decisionPageDTO struct {
	Total int           `json:"total"`
	Items []decisionDTO `json:"items"`
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

func actorRef(r *gen.DecisionActorRefFields) *actorRefDTO {
	if r == nil {
		return nil
	}
	return &actorRefDTO{ID: r.Id, Name: r.Name, Handle: r.Handle, URN: r.Urn}
}

// actorRefs maps a list of generated wrappers that each embed the actor-ref
// fragment; get returns that fragment (nil entries are skipped).
func actorRefs[T any](in []*T, get func(*T) *gen.DecisionActorRefFields) []actorRefDTO {
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

func actor(a *gen.DecisionActorFields) *actorDTO {
	if a == nil {
		return nil
	}
	d := &actorDTO{UserID: a.UserId, WorkerID: a.WorkerId, AppID: a.AppId}
	if a.User != nil {
		d.User = actorRef(&a.User.DecisionActorRefFields)
	}
	if a.Worker != nil {
		d.Worker = actorRef(&a.Worker.DecisionActorRefFields)
	}
	if a.App != nil {
		d.App = actorRef(&a.App.DecisionActorRefFields)
	}
	return d
}

func ref(r *gen.DecisionRefFields) *refDTO {
	if r == nil {
		return nil
	}
	return &refDTO{Ref: r.Ref, Node: r.Node, Spec: r.Spec}
}

func refs[T any](in []*T, get func(*T) *gen.DecisionRefFields) []refDTO {
	out := []refDTO{}
	for _, x := range in {
		if x == nil {
			continue
		}
		out = append(out, *ref(get(x)))
	}
	return out
}

func identityMerges[T any](in []*T, get func(*T) *gen.DecisionIdentityMergeFields) []identityMergeDTO {
	out := []identityMergeDTO{}
	for _, x := range in {
		if x == nil {
			continue
		}
		m := get(x)
		d := identityMergeDTO{FromUserID: m.FromUserId, ToUserID: m.ToUserId, MergedAt: m.MergedAt}
		if m.From != nil {
			d.From = actorRef(&m.From.DecisionActorRefFields)
		}
		if m.To != nil {
			d.To = actorRef(&m.To.DecisionActorRefFields)
		}
		out = append(out, d)
	}
	return out
}

func answer(a *gen.DecisionAnswerFields) *answerDTO {
	if a == nil {
		return nil
	}
	d := &answerDTO{
		At: a.At, ByUserID: a.ByUserId, RecordedByUserID: a.RecordedByUserId, ClaimedBy: a.ClaimedBy,
		Freeform: a.Freeform, Key: a.Key, Text: a.Text, Reason: a.Reason, Via: string(a.Via),
	}
	if a.Source != nil {
		d.Source = ref(&a.Source.DecisionRefFields)
	}
	return d
}

func parts(in []*gen.DecisionFieldsPartsDecisionPart) []partDTO {
	out := []partDTO{}
	for _, p := range in {
		if p == nil {
			continue
		}
		d := partDTO{
			N: p.N, Citation: p.Citation, Question: p.Question,
			NoRecommendationReason: p.NoRecommendationReason,
			Options:                []optionDTO{}, AnswerHistory: []answerDTO{},
		}
		for _, o := range p.Options {
			if o != nil {
				d.Options = append(d.Options, optionDTO{Key: o.Key, Label: o.Label, Meaning: o.Meaning, Cost: o.Cost})
			}
		}
		if r := p.Recommendation; r != nil {
			d.Recommendation = &recommendationDTO{Key: r.Key, Reason: r.Reason, AttributedTo: r.AttributedTo}
			if r.By != nil {
				d.Recommendation.By = actor(&r.By.DecisionActorFields)
			}
		}
		if p.Answer != nil {
			d.Answer = answer(&p.Answer.DecisionAnswerFields)
		}
		for _, h := range p.AnswerHistory {
			if h != nil {
				d.AnswerHistory = append(d.AnswerHistory, *answer(&h.DecisionAnswerFields))
			}
		}
		out = append(out, d)
	}
	return out
}

func decisionFrom(d *gen.DecisionFields) decisionDTO {
	out := decisionDTO{
		ID: d.Id, URN: d.Urn, Code: d.Code, N: d.N, LogID: d.LogId, Title: d.Title, Context: d.Context,
		State: string(d.State), Tier: string(d.Tier), Urgency: string(d.Urgency),
		Authorizes: d.Authorizes, Archival: d.Archival, Revision: d.RevSeq, PortalURL: d.PortalUrl,
		ViewerIsDecider: d.ViewerIsDecider, AskedAt: d.AskedAt, AnsweredAt: d.AnsweredAt,
		DeciderIDs: append([]string{}, d.DeciderIds...),
		Deciders: actorRefs(d.Deciders, func(x *gen.DecisionFieldsDecidersCommentActorRef) *gen.DecisionActorRefFields {
			return &x.DecisionActorRefFields
		}),
		IdentityMerges: identityMerges(d.IdentityMerges, func(x *gen.DecisionFieldsIdentityMergesDecisionIdentityMerge) *gen.DecisionIdentityMergeFields {
			return &x.DecisionIdentityMergeFields
		}),
		Parts: parts(d.Parts),
		Blocks: refs(d.Blocks, func(x *gen.DecisionFieldsBlocksDecisionRef) *gen.DecisionRefFields {
			return &x.DecisionRefFields
		}),
		FollowUps: refs(d.FollowUps, func(x *gen.DecisionFieldsFollowUpsDecisionRef) *gen.DecisionRefFields {
			return &x.DecisionRefFields
		}),
		Sources: refs(d.Sources, func(x *gen.DecisionFieldsSourcesDecisionRef) *gen.DecisionRefFields {
			return &x.DecisionRefFields
		}),
	}
	if d.AskedBy != nil {
		out.AskedBy = actor(&d.AskedBy.DecisionActorFields)
	}
	if w := d.Withdrawn; w != nil {
		out.Withdrawn = &withdrawalDTO{At: w.At, Reason: w.Reason}
		if w.By != nil {
			out.Withdrawn.By = actor(&w.By.DecisionActorFields)
		}
	}
	if d.Evidence != nil && d.Evidence.Approval != nil {
		a := d.Evidence.Approval
		out.Approval = &approvalDTO{
			ApprovedAt: a.ApprovedAt, Revision: a.Revision, Integrity: a.Integrity,
			ByDecider: a.ByDecider, ByUserID: a.ByUserId,
		}
		if a.By != nil {
			out.Approval.By = actorRef(&a.By.DecisionActorRefFields)
		}
	}
	return out
}

func decisionLogFrom(l *gen.DecisionLogFields) decisionLogDTO {
	out := decisionLogDTO{
		ID: l.Id, URN: l.Urn, Loc: l.Loc, Name: l.Name, Prefix: l.Prefix, LastCode: l.LastCode,
		Revision: l.RevSeq, PortalURL: l.PortalUrl,
		DefaultDeciderIDs:  append([]string{}, l.DefaultDeciderIds...),
		EligibleDeciderIDs: append([]string{}, l.EligibleDeciderIds...),
		DefaultDeciders: actorRefs(l.DefaultDeciders, func(x *gen.DecisionLogFieldsDefaultDecidersCommentActorRef) *gen.DecisionActorRefFields {
			return &x.DecisionActorRefFields
		}),
		EligibleDeciders: actorRefs(l.EligibleDeciders, func(x *gen.DecisionLogFieldsEligibleDecidersCommentActorRef) *gen.DecisionActorRefFields {
			return &x.DecisionActorRefFields
		}),
		IdentityMerges: identityMerges(l.IdentityMerges, func(x *gen.DecisionLogFieldsIdentityMergesDecisionIdentityMerge) *gen.DecisionIdentityMergeFields {
			return &x.DecisionIdentityMergeFields
		}),
		Purges: []purgeRecordDTO{},
	}
	for _, p := range l.Purges {
		if p == nil {
			continue
		}
		r := purgeRecordDTO{Code: p.Code, At: p.At, Reason: p.Reason, ContentHash: p.ContentHash, ByUserID: p.ByUserId}
		if p.By != nil {
			r.By = actorRef(&p.By.DecisionActorRefFields)
		}
		out.Purges = append(out.Purges, r)
	}
	return out
}
