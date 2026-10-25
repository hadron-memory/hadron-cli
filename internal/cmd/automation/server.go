package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// maxPages bounds a list walk. A server that keeps handing out a next cursor is
// a server fault, not a reason to loop forever.
const maxPages = 200

// triggerDTO names a trigger on the wire: the kind and the ref the server
// listed (or the run id of a MANUAL origin). Name is the human name when there
// is one.
type triggerDTO struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Name string `json:"name,omitempty"`
}

type rowDTO struct {
	TriggerKind string `json:"triggerKind"`
	TriggerRef  string `json:"triggerRef"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Headline    string `json:"headline"`
}

type omittedDTO struct {
	TriggerKind string `json:"triggerKind"`
	Reason      string `json:"reason"`
}

type flowDTO struct {
	Name   string   `json:"name"`
	URN    string   `json:"urn"`
	Tools  []string `json:"tools"`
	Writes []string `json:"writes"`
}

type summaryDTO struct {
	Name     string    `json:"name"`
	Schedule *string   `json:"schedule"`
	Reads    []string  `json:"reads"`
	Writes   []string  `json:"writes"`
	Tools    []string  `json:"tools"`
	Limits   []string  `json:"limits"`
	CannotDo []string  `json:"cannotDo"`
	Flows    []flowDTO `json:"flows"`
}

// approvalDTO is the typed projection of one trigger. Depth is present only
// when it was asked for.
type approvalDTO struct {
	App     string          `json:"app"`
	Trigger triggerDTO      `json:"trigger"`
	State   string          `json:"state"`
	Hash    string          `json:"hash"`
	Summary summaryDTO      `json:"summary"`
	Depth   json.RawMessage `json:"depth,omitempty"`
}

// unavailable is the one refusal for a server answer this CLI cannot trust: it
// is never turned into an empty or successful result, and nothing was changed.
func unavailable(what string) error {
	return exitcode.Newf(exitcode.Unavailable, "unavailable: the server's answer was malformed (%s); nothing was changed", what)
}

const skewMessage = "this server does not support automation approval (hadron-server#2441): the CLI and server are out of sync — " +
	"update whichever is behind, or the server if it is self-hosted"

// readError classifies a failed READ (list or projection). Schema skew is its
// own sentence and exit 7, ahead of anything prompting; the rest is MapError's.
func readError(err error) error {
	if api.IsSchemaSkew(err) {
		return exitcode.Newf(exitcode.Unavailable, "%s", skewMessage)
	}
	return api.MapError(err)
}

func kindWord(k gen.AutomationTriggerKind) string { return strings.ToLower(string(k)) }

func validListKind(k gen.AutomationTriggerKind) bool {
	return k == gen.AutomationTriggerKindSchedule || k == gen.AutomationTriggerKindWebhook
}

// listing is every row of an App's list plus what the server said it leaves out.
type listing struct {
	Rows    []rowDTO
	Omitted []omittedDTO
}

// collectListing walks the list to exhaustion, in the server's order. Any page
// the CLI cannot validate fails the whole walk: a partial list would let a name
// resolve against rows that are not all there.
func collectListing(ctx context.Context, client graphql.Client, appRef string) (*listing, error) {
	out := &listing{Rows: []rowDTO{}, Omitted: []omittedDTO{}}
	seenOmitted := map[omittedDTO]bool{}
	seenCursor := map[string]bool{}
	var after *string
	for page := 0; page < maxPages; page++ {
		resp, err := gen.AutomationApprovals(ctx, client, appRef, after)
		if err != nil {
			return nil, readError(err)
		}
		if resp == nil || resp.AutomationApprovals == nil {
			return nil, unavailable("no list page")
		}
		p := resp.AutomationApprovals
		if p.Rows == nil || p.Omitted == nil {
			return nil, unavailable("the page's rows or omitted list is null")
		}
		for _, r := range p.Rows {
			if r == nil || !validListKind(r.TriggerKind) || r.TriggerRef == "" || r.Name == "" ||
				(r.State != gen.AutomationAdoptionStateAdopted && r.State != gen.AutomationAdoptionStateUnadopted) {
				return nil, unavailable("a list row is missing its kind, ref, name or state")
			}
			out.Rows = append(out.Rows, rowDTO{string(r.TriggerKind), r.TriggerRef, r.Name, string(r.State), r.Headline})
		}
		for _, o := range p.Omitted {
			if o == nil {
				return nil, unavailable("an omitted entry is null")
			}
			d := omittedDTO{string(o.TriggerKind), o.Reason}
			if !seenOmitted[d] {
				seenOmitted[d] = true
				out.Omitted = append(out.Omitted, d)
			}
		}
		if p.NextCursor == nil || *p.NextCursor == "" {
			return out, nil
		}
		if seenCursor[*p.NextCursor] {
			return nil, unavailable("the next-page cursor repeated")
		}
		seenCursor[*p.NextCursor] = true
		after = p.NextCursor
	}
	return nil, unavailable("the list did not end")
}

// validateProjection refuses a projection missing anything the CLI would show
// or send. Required lists must be present (a null list is not an empty one).
func validateProjection(f *gen.AutomationApprovalFields) error {
	if f == nil || f.Summary == nil {
		return unavailable("no summary")
	}
	if f.Hash == "" {
		return unavailable("no hash")
	}
	switch f.State {
	case gen.AutomationAdoptionStateUnadopted, gen.AutomationAdoptionStateAdopted, gen.AutomationAdoptionStateChanged:
	default:
		return unavailable(fmt.Sprintf("unknown state %q", string(f.State)))
	}
	s := f.Summary
	if s.Reads == nil || s.Writes == nil || s.Tools == nil || s.Limits == nil || s.CannotDo == nil || s.Flows == nil {
		return unavailable("a required list in the summary is null")
	}
	for _, fl := range s.Flows {
		if fl == nil || fl.Tools == nil || fl.Writes == nil {
			return unavailable("a flow is null or has a null list")
		}
	}
	return nil
}

func projectionDTO(app string, t triggerDTO, f *gen.AutomationApprovalFields) approvalDTO {
	s := f.Summary
	flows := make([]flowDTO, 0, len(s.Flows))
	for _, fl := range s.Flows {
		flows = append(flows, flowDTO{Name: fl.Name, URN: fl.Urn, Tools: fl.Tools, Writes: fl.Writes})
	}
	return approvalDTO{
		App: app, Trigger: t, State: string(f.State), Hash: f.Hash,
		Summary: summaryDTO{Name: s.Name, Schedule: s.Schedule, Reads: s.Reads, Writes: s.Writes,
			Tools: s.Tools, Limits: s.Limits, CannotDo: s.CannotDo, Flows: flows},
	}
}

// fetchApproval reads the projection of one trigger, with the graph depth only
// when asked. It validates before returning, so a caller never prompts over a
// projection it cannot trust.
func fetchApproval(ctx context.Context, client graphql.Client, app string, t triggerDTO, kind gen.AutomationTriggerKind, withDepth bool) (*approvalDTO, error) {
	if withDepth {
		resp, err := gen.AutomationApprovalDepth(ctx, client, kind, t.Ref)
		if err != nil {
			return nil, readError(err)
		}
		if resp == nil || resp.AutomationApproval == nil {
			return nil, unavailable("no projection")
		}
		if err := validateProjection(&resp.AutomationApproval.AutomationApprovalFields); err != nil {
			return nil, err
		}
		depth := resp.AutomationApproval.Depth
		if len(depth) == 0 || string(depth) == "null" || !json.Valid(depth) {
			return nil, unavailable("no graph depth")
		}
		dto := projectionDTO(app, t, &resp.AutomationApproval.AutomationApprovalFields)
		dto.Depth = depth
		return &dto, nil
	}
	resp, err := gen.AutomationApproval(ctx, client, kind, t.Ref)
	if err != nil {
		return nil, readError(err)
	}
	if resp == nil || resp.AutomationApproval == nil {
		return nil, unavailable("no projection")
	}
	if err := validateProjection(&resp.AutomationApproval.AutomationApprovalFields); err != nil {
		return nil, err
	}
	dto := projectionDTO(app, t, &resp.AutomationApproval.AutomationApprovalFields)
	return &dto, nil
}

// requireSupport asks the server for the first list page only to learn whether
// it has the approval operations at all. Only schema skew is acted on — any
// other answer (a refusal, an empty page) says nothing about support and is
// ignored. A command that has nothing to read before its write (decline of a
// manual run) uses this so an old server is refused before the prompt.
func requireSupport(ctx context.Context, client graphql.Client, appRef string) error {
	if _, err := gen.AutomationApprovals(ctx, client, appRef, nil); err != nil && api.IsSchemaSkew(err) {
		return readError(err)
	}
	return nil
}
