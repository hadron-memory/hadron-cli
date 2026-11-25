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
func routerWithLine2915() string {
	edge := `{"id":"e_route","name":"to mint a spec","loc":"preflight:to-mint-a-spec:tasks:mint","isRunnable":false,"priority":0,"data":null,
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
		"CreateEdge": {`{"data":{"createEdge":` + newRouteEdgeJSON + `}}`, `{"data":{"createEdge":` + newRouteEdgeJSON + `}}`},
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
	_, out, err := runRoute2915(t, routerWithLine2915(), existingTarget2736("null"), "--dry-run")
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

	counts, out, err = runRoute2915(t, routerWithLine2915(), existingTarget2736("null"))
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

// hadron-cli#3002 item 3: the leaf arm of writesBack. Under the leaf model the
// back-edge is never this run's work, so a leaf whose one edge (leaf -> router)
// already exists reads as already routed with NO --no-back-edge. A mutant that drops
// `!leaf` from writesBack would wait for a back-edge the leaf model never has and
// report the route as not routed.
func TestPreflightRouteLeafModelExistingEdgeIsAlreadyRouted3002(t *testing.T) {
	for _, extra := range [][]string{
		{"--route-model", "leaf", "--json"},
		{"--route-model", "leaf", "--dry-run", "--json"},
	} {
		counts, out, err := runRoute2915(t, codingRouterWithBody(flatRouterBody), existingTarget2736("null"), extra...)
		if err != nil {
			t.Fatal(err)
		}
		if !alreadyRouted2844(t, out) {
			t.Errorf("%v: the leaf's own edge exists and there is no back-edge to wait for; want already routed:\n%s", extra, out)
		}
		for _, op := range []string{"CreateEdge", "UpdateEdge", "UpdateNode"} {
			if n := len(counts[op]); n != 0 {
				t.Errorf("%v: nothing to write, got %d %s", extra, n, op)
			}
		}
	}
}

// hadron-cli#3002 items 1 and 2: the --cross-memory paragraph of `preflight route`
// renders within 80 columns, and its lint sentence names the route, not "it".
func TestPreflightRouteHelpCrossMemoryParagraph3002(t *testing.T) {
	help := cmdHelpOut(t, "coding", "preflight", "route")
	start := strings.Index(help, "--cross-memory marks")
	end := strings.Index(help, "Re-running is safe")
	if start < 0 || end < start {
		t.Fatalf("cross-memory paragraph not found:\n%s", help)
	}
	para := help[start:end]
	for _, line := range strings.Split(para, "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("rendered line is %d columns, want at most 80: %q", n, line)
		}
	}
	flat := strings.Join(strings.Fields(strings.ReplaceAll(para, "`", "")), " ")
	if strings.Contains(flat, "skips only route-target-moved-memory for it") {
		t.Errorf("the lint sentence must name the route, not 'it': %q", flat)
	}
	if !strings.Contains(flat, "preflight lint then skips only route-target-moved-memory for a marked route") {
		t.Errorf("the lint sentence names a marked route: %q", flat)
	}
}

// hadron-cli#3002 (blind review of the note): the contract the release note states.
// A --dry-run judges "already routed" exactly as the real run does, in every case,
// and the key is only ever present as true.
func TestPreflightRouteDryRunAlreadyRoutedMatchesTheRealRun3002(t *testing.T) {
	cases := []struct {
		name           string
		router, target string
		extra          []string
		want           bool
	}{
		{"mirrored, both edges exist", existingRouter2736("null"), existingTarget2736("null"), nil, true},
		{"mirrored, back-edge missing", existingRouter2736("null"), crossTarget2736(), nil, false},
		{"mirrored, nothing exists", codingRouterWithBody(flatRouterBody), crossTarget2736(), nil, false},
		{"leaf, the leaf's edge exists", codingRouterWithBody(flatRouterBody), existingTarget2736("null"), []string{"--route-model", "leaf"}, true},
		{"no back-edge, route edge exists", existingRouter2736("null"), crossTarget2736(), []string{"--no-back-edge"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, mode := range [][]string{{"--dry-run"}, {}} {
				args := append(append([]string{"--json"}, c.extra...), mode...)
				_, out, err := runRoute2915(t, c.router, c.target, args...)
				if err != nil {
					t.Fatal(err)
				}
				if has := alreadyRouted2844(t, out); has != c.want {
					t.Errorf("%v: alreadyRouted = %v, want %v:\n%s", args, has, c.want, out)
				}
				if !c.want && strings.Contains(out, `"alreadyRouted"`) {
					t.Errorf("%v: the key is omitted, never false:\n%s", args, out)
				}
			}
		})
	}
}

// With --cross-memory an existing edge that still needs the marker is NOT already
// routed: a rehearsal omits the key, as the real run (which would write the marker) does.
func TestPreflightRouteDryRunMarkerNeededIsNotAlreadyRouted3002(t *testing.T) {
	_, out, err := runRoute2915(t, existingRouter2736("null"), existingTarget2736("null"), "--cross-memory", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if alreadyRouted2844(t, out) || strings.Contains(out, `"alreadyRouted"`) {
		t.Errorf("an existing edge that needs the marker is not already routed, and the key is omitted:\n%s", out)
	}
	// Only the route edge lacks the marker (the back-edge has it): still not routed.
	_, out, err = runRoute2915(t, existingRouter2736("null"), existingTarget2736(crossMemoryMarker2736), "--cross-memory", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if alreadyRouted2844(t, out) {
		t.Errorf("an unmarked route edge needs the marker even when the back-edge has it:\n%s", out)
	}
	// And only the back-edge lacks it: not routed either.
	_, out, err = runRoute2915(t, existingRouter2736(crossMemoryMarker2736), existingTarget2736("null"), "--cross-memory", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if alreadyRouted2844(t, out) {
		t.Errorf("an unmarked back-edge needs the marker even when the route edge has it:\n%s", out)
	}
	// Once the edges carry the marker, the same rehearsal is already routed.
	_, out, err = runRoute2915(t, existingRouter2736(crossMemoryMarker2736), existingTarget2736(crossMemoryMarker2736), "--cross-memory", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !alreadyRouted2844(t, out) {
		t.Errorf("marked edges are already routed:\n%s", out)
	}
}

// hadron-cli#3002 (blind delta): the release note names the exact text a dry run
// prints. Pin the first line of each case, so a changed verb fails here.
func TestPreflightRouteDryRunTextVerbs3002(t *testing.T) {
	firstLine := func(out string) string { return strings.SplitN(out, "\n", 2)[0] }
	cases := []struct {
		name           string
		router, target string
		extra          []string
		want           string
	}{
		{"already routed, nothing to write", routerWithLine2915(), existingTarget2736("null"), []string{"--dry-run"}, "already routed (nothing to write)"},
		{"already routed, body line missing", existingRouter2736("null"), existingTarget2736("null"), []string{"--dry-run"}, "already routed (edges); would add the body line"},
		{"not routed yet", codingRouterWithBody(flatRouterBody), crossTarget2736(), []string{"--dry-run"}, "would route"},
		{"real run, already routed", routerWithLine2915(), existingTarget2736("null"), nil, "✓ already routed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, out, err := runRoute2915(t, c.router, c.target, c.extra...)
			if err != nil {
				t.Fatal(err)
			}
			if got := firstLine(out); !strings.HasPrefix(got, c.want) {
				t.Errorf("first line = %q, want it to start with %q", got, c.want)
			}
			if c.name != "not routed yet" && strings.Contains(firstLine(out), "would route") {
				t.Errorf("an already-routed route is never 'would route': %q", firstLine(out))
			}
		})
	}
}
