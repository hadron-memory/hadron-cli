package decision

import (
	"context"
	"fmt"
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

// shapeProbeQuery asks the server for the field names of its Question type
// (GraphQL introspection). A server whose Question has a "background" field
// speaks S2: no pre-S2 server has it and every S2 server does. A validation
// probe (select the field and read the refusal) would work with introspection
// switched off, but it would depend on the wording of an error; the one
// introspection read does not.
const shapeProbeQuery = `query DecisionShape { __type(name: "Question") { fields { name } } }`

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

func probeShape(ctx context.Context, client graphql.Client) (shape, error) {
	var data struct {
		Type *struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"__type"`
	}
	req := &graphql.Request{OpName: "DecisionShape", Query: shapeProbeQuery}
	if err := client.MakeRequest(ctx, req, &graphql.Response{Data: &data}); err != nil {
		mapped := api.MapError(err)
		return shapeLegacy, exitcode.New(exitcode.FromError(mapped), fmt.Errorf(
			"cannot tell which decision API this server speaks (asking it for the fields of its Question type failed): %w", mapped))
	}
	if data.Type == nil {
		return shapeLegacy, exitcode.Newf(exitcode.Error,
			"cannot tell which decision API this server speaks: it reports no Question type")
	}
	for _, f := range data.Type.Fields {
		if f.Name == "background" {
			return shapeS2, nil
		}
	}
	return shapeLegacy, nil
}

// s2Call makes one S2 operation and decodes its data into out.
func s2Call(ctx context.Context, client graphql.Client, op, query string, vars map[string]any, out any) error {
	req := &graphql.Request{OpName: op, Query: query, Variables: vars}
	if err := client.MakeRequest(ctx, req, &graphql.Response{Data: out}); err != nil {
		return api.MapError(err)
	}
	return nil
}
