package cmd

// hadron-cli#2915: the three P3s left from the #2910 review of `preflight route`.
// (1) --cross-memory with --no-back-edge: the back-edge is not written, so its
// missing marker must not stop a route from reading as already routed.
// (2) A --dry-run reports alreadyRouted the way the real run does.
// (3) An already-routed run still adds a missing routing line to the router's
// body (a half-routed route is repaired), and says so; the field's comment and
// the help now say the same.

import (
	"strings"
	"testing"
)

const routedLine2915 = "\n- **\"To mint a spec\"** → `tasks:mint` in `hrn:mem:acme.com:specs` — How to mint.\n"

// routerWithLine2915 is existingRouter2736 whose body already carries the routing line.
func routerWithLine2915(data string) string {
	edge := `{"id":"e_route","name":"to mint a spec","loc":"preflight:to-mint-a-spec:tasks:mint","isRunnable":false,"priority":0,"data":` + data + `,
		"target":{"id":"n_target","name":"tasks:mint","loc":"tasks:mint","memoryId":"mem2"}}`
	return strings.Replace(codingRouterWithBody(flatRouterBody+routedLine2915), `"outgoingEdges":[]`, `"outgoingEdges":[`+edge+`]`, 1)
}

func runRoute2915(t *testing.T, router, target string, extra ...string) (map[string][]string, string, error) {
	t.Helper()
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {router, router},
		"GetNode":    {target},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, extra...))
	err := root.Execute()
	counts := map[string][]string{}
	for op, raws := range captured {
		for range raws {
			counts[op] = append(counts[op], op)
		}
	}
	return counts, out.String(), err
}

// Item 1: --cross-memory --no-back-edge writes (and marks) only the route edge, so
// an unmarked back-edge that is not this run's business must not stop "already routed".
func TestPreflightRouteNoBackEdgeIgnoresAnUnmarkedBackEdge2915(t *testing.T) {
	for _, extra := range [][]string{
		{"--cross-memory", "--no-back-edge", "--json"},
		{"--cross-memory", "--no-back-edge", "--no-body-line", "--json"},
	} {
		counts, out, err := runRoute2915(t, existingRouter2736(crossMemoryMarker2736), existingTarget2736("null"), extra...)
		if err != nil {
			t.Fatal(err)
		}
		if !alreadyRouted2844(t, out) {
			t.Errorf("%v: the back-edge is not written, so its missing marker is not this run's work; want already routed:\n%s", extra, out)
		}
		for _, op := range []string{"CreateEdge", "UpdateEdge"} {
			if n := len(counts[op]); n != 0 {
				t.Errorf("%v: nothing to write to the edges, got %d %s", extra, n, op)
			}
		}
	}
}

// Item 2: a rehearsal of an already-routed route says so, as the real run does.
func TestPreflightRouteDryRunReportsAlreadyRouted2915(t *testing.T) {
	counts, out, err := runRoute2915(t, existingRouter2736("null"), existingTarget2736("null"), "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !alreadyRouted2844(t, out) {
		t.Errorf("a dry run of an already-routed route reports alreadyRouted:\n%s", out)
	}
	for _, op := range []string{"CreateEdge", "UpdateEdge", "UpdateNode"} {
		if n := len(counts[op]); n != 0 {
			t.Errorf("a dry run writes nothing, got %d %s", n, op)
		}
	}

	// A route that is not routed yet is not reported as already routed by a rehearsal.
	_, out, err = runRoute2915(t, codingRouterWithBody(flatRouterBody), crossTarget2736(), "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if alreadyRouted2844(t, out) {
		t.Errorf("an unrouted route is not already routed:\n%s", out)
	}
}

// Item 2, text: the rehearsal reads "already routed", never "would route".
func TestPreflightRouteDryRunTextSaysAlreadyRouted2915(t *testing.T) {
	_, out, err := runRoute2915(t, routerWithLine2915("null"), existingTarget2736("null"), "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already routed") || strings.Contains(out, "would route") {
		t.Errorf("a dry run of an already-routed route says so and is not 'would route':\n%s", out)
	}
	if !strings.Contains(out, "nothing to write") {
		t.Errorf("with the edges and the body line both in place, the rehearsal says nothing is to be written:\n%s", out)
	}

	// The body line is missing: the rehearsal says it would add it.
	_, out, err = runRoute2915(t, existingRouter2736("null"), existingTarget2736("null"), "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already routed") || !strings.Contains(out, "would add the body line") {
		t.Errorf("a missing body line is reported as one the run would add:\n%s", out)
	}
}

// Item 3: an already-routed run still adds a missing routing line, and says so; one
// whose body already carries the line writes nothing and says only "already routed".
func TestPreflightRouteAlreadyRoutedRepairsAMissingBodyLine2915(t *testing.T) {
	counts, out, err := runRoute2915(t, existingRouter2736("null"), existingTarget2736("null"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(counts["UpdateNode"]); n != 1 {
		t.Fatalf("a missing routing line is added even when the edges exist: want 1 UpdateNode, got %d", n)
	}
	if !strings.Contains(out, "already routed") || !strings.Contains(out, "body line added") {
		t.Errorf("the verb must say the edges were already there and that a body line was added:\n%s", out)
	}

	counts, out, err = runRoute2915(t, routerWithLine2915("null"), existingTarget2736("null"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(counts["UpdateNode"]); n != 0 {
		t.Errorf("a body that already carries the line is left alone, got %d UpdateNode", n)
	}
	if !strings.Contains(out, "already routed") || strings.Contains(out, "body line added") {
		t.Errorf("nothing was written: plain 'already routed':\n%s", out)
	}
}

// The help says the three behaviours.
func TestPreflightRouteHelpStatesTheFollowUps2915(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "coding", "preflight", "route")), " ")
	for _, want := range []string{
		"With --no-back-edge or the leaf model there is no back-edge, so only the route edge takes the marker",
		"a body that does not is given its routing line even when both edges exist",
		"a --dry-run says the same",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	for _, banned := range []string{"cor:", "hadron-server#", "hadron-cli#"} {
		if strings.Contains(help, banned) {
			t.Errorf("help carries no %q", banned)
		}
	}
}
