package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2844 item 1: a PLAIN re-run (no --cross-memory) of a route that already
// exists must report it as already routed and skip the create, because a real
// server refuses createEdge on an occupied loc. The occupied-loc refusal itself is
// never treated as success: the same refusal can come from a DIFFERENT edge.

func runPlainRoute2844(t *testing.T, routerJSON, targetJSON string, create string) (map[string][]json.RawMessage, string, error) {
	t.Helper()
	responses := map[string][]string{
		"GetNodeRaw": {routerJSON, routerJSON},
		"GetNode":    {targetJSON},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	}
	if create != "" {
		responses["CreateEdge"] = []string{create}
	}
	gql, captured := queueGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--json"))
	err := root.Execute()
	return captured, out.String(), err
}

func TestPreflightRoutePlainRerunReportsAlreadyRoutedWithoutCreating2844(t *testing.T) {
	captured, out, err := runPlainRoute2844(t, existingRouter2736("null"), existingTarget2736("null"), "")
	if err != nil {
		t.Fatalf("a plain re-run of an existing route is safe, got %v", err)
	}
	for _, op := range []string{"CreateEdge", "UpdateEdge"} {
		if n := len(captured[op]); n != 0 {
			t.Errorf("an existing route needs no %s (a real server refuses the duplicate loc), got %d", op, n)
		}
	}
	var dto struct {
		EdgeID        string `json:"edgeId"`
		BackEdge      bool   `json:"backEdge"`
		AlreadyRouted bool   `json:"alreadyRouted"`
	}
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if dto.EdgeID != "e_route" || !dto.AlreadyRouted || !dto.BackEdge {
		t.Errorf("want edgeId e_route, backEdge true, alreadyRouted true, got %+v\n%s", dto, out)
	}
}

func TestPreflightRoutePlainRerunTextSaysAlreadyRouted2844(t *testing.T) {
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {existingRouter2736("null"), existingRouter2736("null")},
		"GetNode":    {existingTarget2736("null")},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already routed") {
		t.Errorf("text output must say the route was already routed, got:\n%s", out.String())
	}
}

// Only the forward edge exists: the missing back-edge is still created, and the
// run is not reported as "already routed" because it wrote something.
func TestPreflightRoutePlainRerunCreatesOnlyTheMissingBackEdge2844(t *testing.T) {
	captured, out, err := runPlainRoute2844(t, existingRouter2736("null"), crossTarget2736(),
		`{"data":{"createEdge":`+newRouteEdgeJSON+`}}`)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(captured["CreateEdge"]); n != 1 {
		t.Fatalf("want exactly one CreateEdge (the back-edge), got %d", n)
	}
	var e struct {
		SourceRef string `json:"sourceRef"`
		TargetRef string `json:"targetRef"`
	}
	_ = json.Unmarshal(captured["CreateEdge"][0], &e)
	if e.SourceRef != "n_target" || e.TargetRef != "root" {
		t.Errorf("the created edge must be the back-edge target -> router, got %+v", e)
	}
	if strings.Contains(out, `"alreadyRouted":true`) {
		t.Errorf("a run that created the back-edge is not already routed:\n%s", out)
	}
}

// A differently-labelled edge to the same node is a DIFFERENT route: it does not
// count as existing, so the create still goes out.
func TestPreflightRoutePlainRerunDifferentLabelStillCreates2844(t *testing.T) {
	other := strings.Replace(existingRouter2736("null"), `"name":"to mint a spec"`, `"name":"to do something else"`, 1)
	captured, _, err := runPlainRoute2844(t, other, crossTarget2736(), `{"data":{"createEdge":`+newRouteEdgeJSON+`}}`)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(captured["CreateEdge"]); n != 2 {
		t.Errorf("a different label is a different route; want 2 CreateEdge, got %d", n)
	}
}

// The occupied-loc refusal is NOT success: with no matching edge read, it is
// the server refusing a different edge at that loc, and it must surface.
func TestPreflightRouteOccupiedRefusalIsNotSuccess2844(t *testing.T) {
	_, _, err := runPlainRoute2844(t, codingRouterWithBody(flatRouterBody), crossTarget2736(),
		`{"errors":[{"message":"Edge loc \"preflight:to-mint-a-spec:tasks:mint\" is already occupied","extensions":{"code":"CONFLICT"}}]}`)
	if err == nil || exitCodeFor(err) == 0 {
		t.Fatalf("an occupied-loc refusal with no matching edge must stay an error, got %v", err)
	}
}

// cli#2844 follow-up (Jessie F1): a null createEdge answer WITHOUT --cross-memory is
// an unknown outcome like the marked path, never exit 0 with no edge.
func TestPreflightRoutePlainNullCreateIsAnUnknownOutcome2844(t *testing.T) {
	_, _, err := runPlainRoute2844(t, codingRouterWithBody(flatRouterBody), crossTarget2736(), `{"data":{"createEdge":null}}`)
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a null createEdge answer must be an unknown outcome (exit 7), got %v (exit %d)", err, exitCodeFor(err))
	}
}
