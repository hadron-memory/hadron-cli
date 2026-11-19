package decision

import (
	"context"
	"encoding/json"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The Questions S2 operations (hadron-server#4513, SDL frozen at 17b1b787).
// They are written by hand, not generated: the vendored schema snapshot is the
// pre-S2 one, genqlient cannot hold two shapes of one type, and this path is
// removed again in cli#2640. The selection and the input types follow the S2
// SDL exactly; TestS2OperationsMatchTheSDL checks every field named here
// against a copy of it.

const s2Fields = `
fragment S2Actor on QuestionActor {
  userId user { id name handle urn }
  workerId worker { id name handle urn }
  appId app { id name handle urn }
}
fragment S2Ref on QuestionRef { ref node spec }
fragment S2Answer on QuestionAnswer { kind key text reason byUserId at via }
fragment S2Question on Question {
  id urn code n logId title background whyNow priority tier authorizes state
  revSeq portalUrl viewerIsDecider askedAt answeredAt neededBy defaultAction
  askedBy { ...S2Actor }
  deciderIds
  deciders { id name handle urn }
  identityMerges { fromUserId from { id name handle urn } toUserId to { id name handle urn } mergedAt }
  options { key label whyChoose whyNot risks }
  recommendation { key reason by { ...S2Actor } }
  noRecommendationReason
  answer { ...S2Answer }
  answers { ...S2Answer }
  blocks { ...S2Ref }
  sources { ...S2Ref }
  specsTouched { ...S2Ref }
  specsMustChange { ...S2Ref }
  withdrawn { at reason by { ...S2Actor } }
  evidence { approval { approvedAt revision integrity byDecider byUserId by { id name handle urn } } }
}
`

const s2DecisionQuery = `query DecisionS2($ref: ID, $code: String, $logRef: ID) {
  question(ref: $ref, code: $code, logRef: $logRef) { ...S2Question }
}` + s2Fields

const s2DecisionsQuery = `query DecisionsS2($logRef: ID, $awaitingMe: Boolean, $authorizes: Boolean, $state: [QuestionState!], $tier: QuestionTier, $priority: [QuestionPriority!], $sort: QuestionSort, $limit: Int, $offset: Int) {
  questions(logRef: $logRef, awaitingMe: $awaitingMe, authorizes: $authorizes, state: $state, tier: $tier, priority: $priority, sort: $sort, limit: $limit, offset: $offset) {
    total
    items { ...S2Question }
  }
}` + s2Fields

const s2AskMutation = `mutation AskDecisionS2($logRef: ID!, $input: AskQuestionInput!) {
  askQuestion(logRef: $logRef, input: $input) { ...S2Question }
}` + s2Fields

const s2AmendMutation = `mutation AmendDecisionS2($ref: ID!, $input: AmendQuestionInput!, $expectedRevision: Int!, $reason: String!) {
  amendQuestion(ref: $ref, input: $input, expectedRevision: $expectedRevision, reason: $reason) { ...S2Question }
}` + s2Fields

const s2WithdrawMutation = `mutation WithdrawDecisionS2($ref: ID!, $reason: String!, $expectedRevision: Int!) {
  withdrawQuestion(ref: $ref, reason: $reason, expectedRevision: $expectedRevision) { ...S2Question }
}` + s2Fields

// --- response types ---

type s2ActorRef struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

type s2Actor struct {
	UserID   *string     `json:"userId"`
	User     *s2ActorRef `json:"user"`
	WorkerID *string     `json:"workerId"`
	Worker   *s2ActorRef `json:"worker"`
	AppID    *string     `json:"appId"`
	App      *s2ActorRef `json:"app"`
}

type s2Ref struct {
	Ref  *string `json:"ref"`
	Node *string `json:"node"`
	Spec *string `json:"spec"`
}

type s2Answer struct {
	Kind     string  `json:"kind"`
	Key      *string `json:"key"`
	Text     *string `json:"text"`
	Reason   *string `json:"reason"`
	ByUserID string  `json:"byUserId"`
	At       string  `json:"at"`
	Via      string  `json:"via"`
}

type s2Question struct {
	ID              string        `json:"id"`
	URN             string        `json:"urn"`
	Code            string        `json:"code"`
	N               int           `json:"n"`
	LogID           *string       `json:"logId"`
	Title           string        `json:"title"`
	Background      *string       `json:"background"`
	WhyNow          *string       `json:"whyNow"`
	Priority        string        `json:"priority"`
	Tier            string        `json:"tier"`
	Authorizes      bool          `json:"authorizes"`
	State           string        `json:"state"`
	RevSeq          int           `json:"revSeq"`
	PortalURL       *string       `json:"portalUrl"`
	ViewerIsDecider bool          `json:"viewerIsDecider"`
	AskedAt         *string       `json:"askedAt"`
	AnsweredAt      *string       `json:"answeredAt"`
	NeededBy        *string       `json:"neededBy"`
	DefaultAction   *string       `json:"defaultAction"`
	AskedBy         *s2Actor      `json:"askedBy"`
	DeciderIDs      []string      `json:"deciderIds"`
	Deciders        []*s2ActorRef `json:"deciders"`
	IdentityMerges  []*struct {
		FromUserID string      `json:"fromUserId"`
		From       *s2ActorRef `json:"from"`
		ToUserID   string      `json:"toUserId"`
		To         *s2ActorRef `json:"to"`
		MergedAt   string      `json:"mergedAt"`
	} `json:"identityMerges"`
	Options []*struct {
		Key       string  `json:"key"`
		Label     string  `json:"label"`
		WhyChoose *string `json:"whyChoose"`
		WhyNot    *string `json:"whyNot"`
		Risks     *string `json:"risks"`
	} `json:"options"`
	Recommendation *struct {
		Key    string   `json:"key"`
		Reason *string  `json:"reason"`
		By     *s2Actor `json:"by"`
	} `json:"recommendation"`
	NoRecommendationReason *string     `json:"noRecommendationReason"`
	Answer                 *s2Answer   `json:"answer"`
	Answers                []*s2Answer `json:"answers"`
	Blocks                 []*s2Ref    `json:"blocks"`
	Sources                []*s2Ref    `json:"sources"`
	SpecsTouched           []*s2Ref    `json:"specsTouched"`
	SpecsMustChange        []*s2Ref    `json:"specsMustChange"`
	Withdrawn              *struct {
		At     string   `json:"at"`
		Reason string   `json:"reason"`
		By     *s2Actor `json:"by"`
	} `json:"withdrawn"`
	Evidence *struct {
		Approval *struct {
			ApprovedAt string      `json:"approvedAt"`
			Revision   int         `json:"revision"`
			Integrity  string      `json:"integrity"`
			ByDecider  bool        `json:"byDecider"`
			ByUserID   *string     `json:"byUserId"`
			By         *s2ActorRef `json:"by"`
		} `json:"approval"`
	} `json:"evidence"`
}

// --- mapping to the decision DTO ---

func s2ActorRefDTO(r *s2ActorRef) *actorRefDTO {
	if r == nil {
		return nil
	}
	return &actorRefDTO{ID: r.ID, Name: r.Name, Handle: r.Handle, URN: r.URN}
}

func s2ActorDTO(a *s2Actor) *actorDTO {
	if a == nil {
		return nil
	}
	return &actorDTO{
		UserID: a.UserID, User: s2ActorRefDTO(a.User),
		WorkerID: a.WorkerID, Worker: s2ActorRefDTO(a.Worker),
		AppID: a.AppID, App: s2ActorRefDTO(a.App),
	}
}

func s2RefsDTO(in []*s2Ref) []refDTO {
	out := []refDTO{}
	for _, r := range in {
		if r != nil {
			out = append(out, refDTO{Ref: r.Ref, Node: r.Node, Spec: r.Spec})
		}
	}
	return out
}

// s2AnswerDTO maps an answer-list entry. S2 does not say who recorded an
// answer for another, nor whether it was free text: recordedByUserId and
// claimedBy are null, and freeform is true for an answer with no option key.
func s2AnswerDTO(a *s2Answer) *answerDTO {
	if a == nil {
		return nil
	}
	return &answerDTO{
		At: a.At, ByUserID: a.ByUserID, Kind: a.Kind,
		Freeform: a.Kind == "ANSWER" && a.Key == nil,
		Key:      a.Key, Text: a.Text, Reason: a.Reason, Via: a.Via,
	}
}

// decisionFromS2 maps a question read from an S2 server to the one decision
// shape the CLI prints. S2 has no parts: the question is the one part, numbered
// 1 and cited <code>-1, whose question text is the title. Fields S2 removed
// (archival, follow-ups, an option's meaning and cost, a recommendation's
// attributedTo) stay empty; context and urgency carry background and priority
// so existing readers of the old keys keep working.
func decisionFromS2(q *s2Question) decisionDTO {
	part := partDTO{
		N: 1, Citation: q.Code + "-1", Question: q.Title,
		Options:                []optionDTO{},
		NoRecommendationReason: q.NoRecommendationReason,
		Answer:                 s2AnswerDTO(q.Answer),
		AnswerHistory:          []answerDTO{},
	}
	for _, o := range q.Options {
		if o != nil {
			part.Options = append(part.Options, optionDTO{Key: o.Key, Label: o.Label, WhyChoose: o.WhyChoose, WhyNot: o.WhyNot, Risks: o.Risks})
		}
	}
	if r := q.Recommendation; r != nil {
		part.Recommendation = &recommendationDTO{Key: r.Key, Reason: r.Reason, By: s2ActorDTO(r.By)}
	}
	for _, a := range q.Answers {
		if a != nil {
			part.AnswerHistory = append(part.AnswerHistory, *s2AnswerDTO(a))
		}
	}
	out := decisionDTO{
		ID: q.ID, URN: q.URN, Code: q.Code, N: q.N, LogID: q.LogID, Title: q.Title,
		Context: q.Background, Background: q.Background, WhyNow: q.WhyNow,
		State: q.State, Tier: q.Tier, Urgency: q.Priority, Priority: q.Priority,
		Authorizes: q.Authorizes, Revision: q.RevSeq, PortalURL: q.PortalURL,
		ViewerIsDecider: q.ViewerIsDecider, AskedAt: q.AskedAt, AnsweredAt: q.AnsweredAt,
		NeededBy: q.NeededBy, DefaultAction: q.DefaultAction,
		AskedBy:    s2ActorDTO(q.AskedBy),
		DeciderIDs: append([]string{}, q.DeciderIDs...),
		Deciders:   []actorRefDTO{}, IdentityMerges: []identityMergeDTO{},
		Parts:  []partDTO{part},
		Blocks: s2RefsDTO(q.Blocks), FollowUps: []refDTO{}, Sources: s2RefsDTO(q.Sources),
		SpecsTouched: s2RefsDTO(q.SpecsTouched), SpecsMustChange: s2RefsDTO(q.SpecsMustChange),
	}
	for _, d := range q.Deciders {
		if d != nil {
			out.Deciders = append(out.Deciders, *s2ActorRefDTO(d))
		}
	}
	for _, m := range q.IdentityMerges {
		if m != nil {
			out.IdentityMerges = append(out.IdentityMerges, identityMergeDTO{
				FromUserID: m.FromUserID, From: s2ActorRefDTO(m.From),
				ToUserID: m.ToUserID, To: s2ActorRefDTO(m.To), MergedAt: m.MergedAt,
			})
		}
	}
	if w := q.Withdrawn; w != nil {
		out.Withdrawn = &withdrawalDTO{At: w.At, Reason: w.Reason, By: s2ActorDTO(w.By)}
	}
	if q.Evidence != nil && q.Evidence.Approval != nil {
		a := q.Evidence.Approval
		out.Approval = &approvalDTO{
			ApprovedAt: a.ApprovedAt, Revision: a.Revision, Integrity: a.Integrity,
			ByDecider: a.ByDecider, ByUserID: a.ByUserID, By: s2ActorRefDTO(a.By),
		}
	}
	return out
}

// --- operations ---

func s2Decision(ctx context.Context, c graphql.Client, ref, code, logRef *string) (*decisionDTO, error) {
	vars := map[string]any{}
	setIf(vars, "ref", ref)
	setIf(vars, "code", code)
	setIf(vars, "logRef", logRef)
	var data struct {
		Question *s2Question `json:"question"`
	}
	if err := s2Call(ctx, c, "DecisionS2", s2DecisionQuery, vars, &data); err != nil {
		return nil, err
	}
	if data.Question == nil {
		return nil, nil
	}
	d := decisionFromS2(data.Question)
	return &d, nil
}

// s2ListFilters are the list filters in the order the S2 operation names them.
type s2ListFilters struct {
	logRef, tier, sort    *string
	awaitingMe, authorize *bool
	states, priorities    []string
	limit, offset         *int
}

func s2Decisions(ctx context.Context, c graphql.Client, f s2ListFilters) (*decisionPageDTO, error) {
	vars := map[string]any{}
	setIf(vars, "logRef", f.logRef)
	setIf(vars, "awaitingMe", f.awaitingMe)
	setIf(vars, "authorizes", f.authorize)
	if f.states != nil {
		vars["state"] = f.states
	}
	setIf(vars, "tier", f.tier)
	if f.priorities != nil {
		vars["priority"] = f.priorities
	}
	setIf(vars, "sort", f.sort)
	setIf(vars, "limit", f.limit)
	setIf(vars, "offset", f.offset)
	var data struct {
		Questions *struct {
			Total int           `json:"total"`
			Items []*s2Question `json:"items"`
		} `json:"questions"`
	}
	if err := s2Call(ctx, c, "DecisionsS2", s2DecisionsQuery, vars, &data); err != nil {
		return nil, err
	}
	if data.Questions == nil {
		return nil, exitcode.Newf(exitcode.Error, "server returned no decision page")
	}
	page := &decisionPageDTO{Total: data.Questions.Total, Items: []decisionDTO{}}
	for _, it := range data.Questions.Items {
		if it != nil {
			page.Items = append(page.Items, decisionFromS2(it))
		}
	}
	return page, nil
}

func s2Mutation(ctx context.Context, c graphql.Client, op, query, field string, vars map[string]any) (*decisionDTO, error) {
	var data map[string]*s2Question
	if err := s2Call(ctx, c, op, query, vars, &data); err != nil {
		return nil, err
	}
	q := data[field]
	if q == nil {
		return nil, exitcode.Newf(exitcode.Error, "server returned no decision")
	}
	d := decisionFromS2(q)
	return &d, nil
}

func s2Ask(ctx context.Context, c graphql.Client, logRef string, input *s2AskInput) (*decisionDTO, error) {
	return s2Mutation(ctx, c, "AskDecisionS2", s2AskMutation, "askQuestion", map[string]any{"logRef": logRef, "input": input})
}

func s2Amend(ctx context.Context, c graphql.Client, ref string, input *s2AmendInput, expected int, reason string) (*decisionDTO, error) {
	return s2Mutation(ctx, c, "AmendDecisionS2", s2AmendMutation, "amendQuestion",
		map[string]any{"ref": ref, "input": input, "expectedRevision": expected, "reason": reason})
}

func s2Withdraw(ctx context.Context, c graphql.Client, ref, reason string, expected int) (*decisionDTO, error) {
	return s2Mutation(ctx, c, "WithdrawDecisionS2", s2WithdrawMutation, "withdrawQuestion",
		map[string]any{"ref": ref, "reason": reason, "expectedRevision": expected})
}

// setIf adds key when the pointer is set, so an unset filter is omitted rather
// than sent as null.
func setIf[T any](m map[string]any, key string, v *T) {
	if v != nil {
		m[key] = *v
	}
}

// --- S2 inputs ---

type s2RefInput struct {
	Ref  *string `json:"ref,omitempty"`
	Node *string `json:"node,omitempty"`
	Spec *string `json:"spec,omitempty"`
}

type s2OptionInput struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	WhyChoose *string `json:"whyChoose,omitempty"`
	WhyNot    *string `json:"whyNot,omitempty"`
	Risks     *string `json:"risks,omitempty"`
}

type s2RecommendationInput struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type s2NoRecommendationInput struct {
	Reason string `json:"reason"`
}

// s2AskInput is AskQuestionInput. Optional fields are omitted when unset;
// deciders is not (null means the log's default deciders, an explicit [] is
// the caller's own request and reaches the server as sent).
type s2AskInput struct {
	Title            string                   `json:"title"`
	Background       *string                  `json:"background,omitempty"`
	WhyNow           *string                  `json:"whyNow,omitempty"`
	Priority         *string                  `json:"priority,omitempty"`
	Tier             *string                  `json:"tier,omitempty"`
	Authorizes       *bool                    `json:"authorizes,omitempty"`
	Grant            json.RawMessage          `json:"grant,omitempty"`
	Deciders         []string                 `json:"deciders"`
	Assignee         *string                  `json:"assignee,omitempty"`
	NeededBy         *string                  `json:"neededBy,omitempty"`
	DefaultAction    *string                  `json:"defaultAction,omitempty"`
	Blocks           []s2RefInput             `json:"blocks,omitempty"`
	Sources          []s2RefInput             `json:"sources,omitempty"`
	SpecsTouched     []s2RefInput             `json:"specsTouched,omitempty"`
	SpecsMustChange  []s2RefInput             `json:"specsMustChange,omitempty"`
	Options          []s2OptionInput          `json:"options,omitempty"`
	Yesno            *bool                    `json:"yesno,omitempty"`
	Recommendation   *s2RecommendationInput   `json:"recommendation,omitempty"`
	NoRecommendation *s2NoRecommendationInput `json:"noRecommendation,omitempty"`
}

// s2AmendInput is AmendQuestionInput. An omitted field is unchanged. For
// background, whyNow, assignee, neededBy and defaultAction a literal null
// clears the value, which a *string cannot carry, so those are raw JSON (nil
// omitted, "null" sent as null). A reference list is a pointer so that [] (clear
// the list) is told apart from absent (unchanged).
type s2AmendInput struct {
	Title            *string                  `json:"title,omitempty"`
	Background       json.RawMessage          `json:"background,omitempty"`
	WhyNow           json.RawMessage          `json:"whyNow,omitempty"`
	Priority         *string                  `json:"priority,omitempty"`
	Tier             *string                  `json:"tier,omitempty"`
	Assignee         json.RawMessage          `json:"assignee,omitempty"`
	NeededBy         json.RawMessage          `json:"neededBy,omitempty"`
	DefaultAction    json.RawMessage          `json:"defaultAction,omitempty"`
	Blocks           *[]s2RefInput            `json:"blocks,omitempty"`
	Sources          *[]s2RefInput            `json:"sources,omitempty"`
	SpecsTouched     *[]s2RefInput            `json:"specsTouched,omitempty"`
	SpecsMustChange  *[]s2RefInput            `json:"specsMustChange,omitempty"`
	Options          []s2OptionInput          `json:"options,omitempty"`
	Yesno            *bool                    `json:"yesno,omitempty"`
	Recommendation   *s2RecommendationInput   `json:"recommendation,omitempty"`
	NoRecommendation *s2NoRecommendationInput `json:"noRecommendation,omitempty"`
}

// isEmpty reports an amend that would change nothing: every field omitted.
// The three reference lists and the tri-state fields count by presence (an
// explicit [] or null is a real change); options counts by length.
func (in *s2AmendInput) isEmpty() bool {
	return in.Title == nil && in.Background == nil && in.WhyNow == nil && in.Priority == nil &&
		in.Tier == nil && in.Assignee == nil && in.NeededBy == nil && in.DefaultAction == nil &&
		in.Blocks == nil && in.Sources == nil && in.SpecsTouched == nil && in.SpecsMustChange == nil &&
		len(in.Options) == 0 && in.Yesno == nil && in.Recommendation == nil && in.NoRecommendation == nil
}
