package decision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2639: hadron-server#4513 (Questions S2) replaces the decision API's
// shape: QuestionPart, urgency, context and the option meaning and cost are
// gone, and the question's own fields (background, whyNow, priority, options,
// recommendation, answer) move onto Question. The CLI ships before #4513
// merges and must work on both sides of it, so each command that reads or
// writes a decision asks the server which shape it speaks and then uses that
// shape's operations. The old-shape path goes away in cli#2640.

// shape is the decision API a server speaks.
type shape int

const (
	// shapeLegacy is the pre-S2 API: a decision has parts, urgency and context.
	shapeLegacy shape = iota
	// shapeS2 is the Questions S2 API (hadron-server#4513): one question per
	// decision, priority and background.
	shapeS2
)

// shapeProbeQuery is the probe, and it is a VALIDATION probe: it selects
// Question.background through the one read both servers have, the decisions
// awaiting the caller. A server that has the field runs it (a page of at most
// one decision, or an execution error that is not about the shape); a server
// that does not refuses it at validation, GRAPHQL_VALIDATION_FAILED naming
// "background". It must not be an introspection read (__type, __schema): the
// hosted server switches introspection off (hadron api --help; cli#1056), so
// an introspection probe fails on the one server this check exists for.
const shapeProbeQuery = `query DecisionShape { questions(awaitingMe: true, limit: 1) { total items { background } } }`

// validationFailed is the extensions.code of a query the server refused before
// running it.
const validationFailed = "GRAPHQL_VALIDATION_FAILED"

// prober answers the shape question once per invocation: one command line is
// one process, and the answer is fixed for the server it runs against.
type prober struct {
	once  sync.Once
	shape shape
	err   error
}

// of returns the server's decision shape, asking it on the first call only.
// A failed probe is remembered too: the command fails with it, so there is no
// second attempt to make.
func (p *prober) of(ctx context.Context, client graphql.Client) (shape, error) {
	p.once.Do(func() { p.shape, p.err = probeShape(ctx, client) })
	return p.shape, p.err
}

// probeShape sends the validation probe and reads the server's answer:
//   - it ran: the server has Question.background, so it speaks S2;
//   - GRAPHQL_VALIDATION_FAILED, every such error about "background": it does
//     not, so it is an older server;
//   - an execution error (a GraphQL error that is not a validation failure, on
//     an HTTP 200): the query passed validation, so the field exists and it is
//     S2; the command's own call will show the error again if it is real;
//   - anything else (a transport failure, a non-200 that is not a validation
//     refusal, a validation failure about something other than background, an
//     error with no GraphQL body) is NOT "older": the command stops with it
//     rather than send the wrong shape.
func probeShape(ctx context.Context, client graphql.Client) (shape, error) {
	var data struct {
		Questions *struct {
			Total int `json:"total"`
		} `json:"questions"`
	}
	req := &graphql.Request{OpName: "DecisionShape", Query: shapeProbeQuery}
	err := client.MakeRequest(ctx, req, &graphql.Response{Data: &data})
	if err == nil {
		if data.Questions == nil {
			return shapeLegacy, exitcode.Newf(exitcode.Error,
				"cannot tell which decision API this server speaks: its answer to the check had no decisions page")
		}
		return shapeS2, nil
	}
	details := api.ErrorDetails(err)
	var httpErr *graphql.HTTPError
	status200 := !errors.As(err, &httpErr) // a GraphQL-level error rides a 200
	var validation, aboutBackground, other int
	for _, d := range details {
		switch {
		case d.Code != validationFailed:
			other++
		case strings.Contains(d.Message, "background"):
			validation++
			aboutBackground++
		default:
			validation++
		}
	}
	switch {
	case validation > 0 && other == 0 && aboutBackground == validation:
		return shapeLegacy, nil
	case len(details) > 0 && validation == 0 && status200:
		return shapeS2, nil
	}
	mapped := api.MapError(err)
	return shapeLegacy, exitcode.New(exitcode.FromError(mapped), fmt.Errorf(
		"cannot tell which decision API this server speaks (the check that asks it whether a decision has a background failed): %w", mapped))
}

// s2Call makes one S2 operation and decodes its data into out.
func s2Call(ctx context.Context, client graphql.Client, op, query string, vars map[string]any, out any) error {
	req := &graphql.Request{OpName: op, Query: query, Variables: vars}
	if err := client.MakeRequest(ctx, req, &graphql.Response{Data: out}); err != nil {
		return api.MapError(err)
	}
	return nil
}
