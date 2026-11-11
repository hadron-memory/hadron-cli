package team

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// workerGetAvailabilityDTO is `worker get`'s availability provenance (cli#1911,
// hadron-server#3865 slice 2, cor:agt:060:02): the stored record's state, the
// reason, who set it and when. The `availability` key of the worker DTO is
// always present; null means the server answered null, which is BOTH "this
// caller may not see worker internals" and "the worker has no record" — the CLI
// cannot tell which, so a null is rendered as nothing and never as "available"
// or "no record", and is never filled from another read.
type workerGetAvailabilityDTO struct {
	State string `json:"state"`
	// Reason is set only for an UNAVAILABLE record (cor:agt:060:02): an
	// available record carries none, even if a stale one arrives.
	Reason *string `json:"reason"`
	Until  *string `json:"until"`
	SetAt  string  `json:"setAt"`
	// SetBy is the setter class as the server names it (a LAPSE is the
	// platform's own rewrite, not a person). SetByParty is who acted when a
	// worker or a person set it; null for the platform's own setters, and for
	// a person setter whose identity the server withheld.
	SetBy      string        `json:"setBy"`
	SetByParty *taskPartyDTO `json:"setByParty"`
	InEffect   bool          `json:"inEffect"`
}

// readWorkerAvailability reads Worker.availability in its own operation, so a
// server that predates #3953 still serves `worker get`: only that documented
// capability absence (every error in the envelope a validation error on the
// field) comes back as nil, and any other error is returned.
func readWorkerAvailability(ctx context.Context, client graphql.Client, id string) (*workerGetAvailabilityDTO, error) {
	resp, err := gen.GetWorkerAvailability(ctx, client, id)
	if err != nil {
		if api.IsGraphQLValidationOnlyFor(err, "availability") {
			return nil, nil
		}
		return nil, api.MapError(err)
	}
	if resp.Worker == nil || resp.Worker.Availability == nil {
		return nil, nil
	}
	a := resp.Worker.Availability
	dto := &workerGetAvailabilityDTO{
		State:      string(a.State),
		Until:      a.Until,
		SetAt:      a.SetAt,
		SetBy:      string(a.SetBy),
		SetByParty: partyDTO(a.SetByParty),
		InEffect:   a.InEffect,
	}
	if a.State == gen.AvailabilityStateUnavailable {
		dto.Reason = reasonPtr(a.Reason)
	}
	return dto, nil
}

// availabilitySetterCell names who set the record. The platform's own setters
// are named as themselves; a setter that is a person or a worker shows the
// party the server named, or says the identity was withheld — never a guess.
func availabilitySetterCell(a *workerGetAvailabilityDTO) string {
	switch a.SetBy {
	case string(gen.AvailabilitySetterLapse):
		return "lapse"
	case string(gen.AvailabilitySetterStallRule):
		return "stall rule"
	case string(gen.AvailabilitySetterContextReset):
		return "context reset"
	}
	if a.SetByParty != nil {
		return fmt.Sprintf("%s (%s)", partyCell(a.SetByParty), output.SafeCell(a.SetBy))
	}
	switch a.SetBy {
	case string(gen.AvailabilitySetterHost):
		return "host"
	case string(gen.AvailabilitySetterHolder), string(gen.AvailabilitySetterAppManager), string(gen.AvailabilitySetterWorker), string(gen.AvailabilitySetterReportsTo):
		return output.SafeCell(a.SetBy) + " (identity withheld)"
	}
	return output.SafeCell(a.SetBy)
}

// writeWorkerAvailability prints the `availability:` line of `worker get`.
// A nil record prints nothing at all.
func writeWorkerAvailability(out io.Writer, a *workerGetAvailabilityDTO) {
	if a == nil {
		return
	}
	state := a.State
	if a.SetBy == string(gen.AvailabilitySetterLapse) {
		state = fmt.Sprintf("%s (lapsed at %s)", state, a.SetAt)
	}
	line := "  availability: " + output.SafeCell(state)
	if a.Reason != nil {
		line += " — " + output.SafeCell(*a.Reason)
	}
	if a.Until != nil {
		line += " until " + output.SafeCell(*a.Until)
	}
	line += "; set by " + availabilitySetterCell(a)
	if a.SetBy != string(gen.AvailabilitySetterLapse) {
		line += " at " + output.SafeCell(a.SetAt)
	}
	fmt.Fprintln(out, line)
}
