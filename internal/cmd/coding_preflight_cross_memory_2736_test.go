package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2736 (CHECKED T1): a router can mark a route as an INTENTIONAL cross-memory
// route, so `route-target-moved-memory` stays meaningful for a target that really
// moved. The marker is carried in the route edge's own data ({"crossMemory":true}),
// never in the edge name (cor:dmo:060:03), written by `preflight route
// --cross-memory`, shown by `preflight list`, and honored at both lint sites by
// skipping only the moved-memory comparison.

const crossMemoryMarker2736 = `{"crossMemory":true}`

// crossOut is a route OUT of the router to a target in mem2, with edge data.
func crossOut(id, label, targetLoc, data string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":"preflight:x:` + targetLoc + `","isRunnable":false,
		"priority":0,"data":` + data + `,"target":{"id":"t_` + id + `","name":"` + targetLoc + `","loc":"` + targetLoc + `","memoryId":"mem2"}}`
}

// crossIn is a leaf-owned edge INTO the router from a node in mem2.
func crossIn(id, label, leafLoc, data string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":"` + leafLoc + `:to-router","isRunnable":false,
		"priority":0,"data":` + data + `,"source":{"id":"n_` + leafLoc + `","loc":"` + leafLoc + `","memoryId":"mem2"}}`
}

func lintOut2736(t *testing.T, outgoing, batch string) (string, error) {
	t.Helper()
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("preflight", "", outgoing),
		"NodeBatch": batch,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--json", "--server", gql.URL})
	err := root.Execute()
	return out.String(), err
}

func rulesIn2736(t *testing.T, out string) map[string]bool {
	t.Helper()
	var rows []struct {
		Rule string `json:"rule"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		var wrapped struct {
			Findings []struct {
				Rule string `json:"rule"`
			} `json:"findings"`
		}
		if err2 := json.Unmarshal([]byte(out), &wrapped); err2 != nil {
			t.Fatalf("lint output is not JSON: %v\n%s", err, out)
		}
		for _, f := range wrapped.Findings {
			rows = append(rows, struct {
				Rule string `json:"rule"`
			}{f.Rule})
		}
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Rule] = true
	}
	return got
}

func TestPreflightLintCrossMemoryMarkerSkipsOnlyTheMoveCheck2736(t *testing.T) {
	batch := codingBatch([]string{codingBatchNode("tasks:mint", "", "d")}, "")
	marked, err := lintOut2736(t, crossOut("e1", "to mint a spec", "tasks:mint", crossMemoryMarker2736), batch)
	if err != nil {
		t.Fatalf("a marked cross-memory route is clean, got %v\n%s", err, marked)
	}
	if rulesIn2736(t, marked)["route-target-moved-memory"] {
		t.Errorf("a route marked crossMemory must not raise route-target-moved-memory:\n%s", marked)
	}
	for name, data := range map[string]string{
		"unmarked":      `null`,
		"other keys":    `{"note":"x"}`,
		"marker false":  `{"crossMemory":false}`,
		"marker string": `{"crossMemory":"true"}`,
	} {
		out, _ := lintOut2736(t, crossOut("e1", "to mint a spec", "tasks:mint", data), batch)
		if !rulesIn2736(t, out)["route-target-moved-memory"] {
			t.Errorf("%s: the same route must still raise route-target-moved-memory:\n%s", name, out)
		}
	}
}

// The marker is about intent, not health: an unreadable target, a bad label and a
// retired target are still reported on a marked route.
func TestPreflightLintMarkedRouteStillGetsTheOtherRules2736(t *testing.T) {
	out, err := lintOut2736(t, crossOut("e1", "to mint a spec", "tasks:gone", crossMemoryMarker2736), codingBatch(nil, `"t_e1"`))
	if exitCodeFor(err) != exitcode.Conflict || !rulesIn2736(t, out)["route-target-resolves"] {
		t.Errorf("a marked route to an unreadable target must still be route-target-resolves (exit 5), got err=%v\n%s", err, out)
	}
	out, _ = lintOut2736(t, crossOut("e1", "routes-to", "tasks:mint", crossMemoryMarker2736),
		codingBatch([]string{codingBatchNode("tasks:mint", `"deprecated"`, "d")}, ""))
	got := rulesIn2736(t, out)
	if !got["route-label-phrasing"] || got["route-target-moved-memory"] {
		t.Errorf("a marked route keeps its label rule and loses only the move rule, got %v\n%s", got, out)
	}
}

func TestPreflightLintLeafModelHonorsTheMarker2736(t *testing.T) {
	for name, tc := range map[string]struct {
		data string
		want bool
	}{"marked": {crossMemoryMarker2736, false}, "unmarked": {`null`, true}} {
		t.Run(name, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{
				"GetNode":   codingRootJSON("preflight", crossIn("e1", "to mint a spec", "tasks:mint", tc.data), ""),
				"NodeBatch": codingBatch([]string{codingBatchNode("tasks:mint", "", "d")}, ""),
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--route-model", "leaf", "--json", "--server", gql.URL})
			_ = root.Execute()
			if got := rulesIn2736(t, out.String())["route-target-moved-memory"]; got != tc.want {
				t.Errorf("leaf incoming edge (%s): moved-memory=%v want %v\n%s", name, got, tc.want, out.String())
			}
		})
	}
}

func TestPreflightListShowsTheMarker2736(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("preflight", "",
			crossOut("e1", "to mint a spec", "tasks:mint", crossMemoryMarker2736)+","+crossOut("e2", "to fix a thing", "findings:x", `null`)),
		"NodeBatch": codingBatch([]string{codingBatchNode("tasks:mint", "", "d"), codingBatchNode("findings:x", "", "d")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Target      string `json:"target"`
		CrossMemory *bool  `json:"crossMemory"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("list --json: %v\n%s", err, out.String())
	}
	for _, r := range rows {
		if r.CrossMemory == nil || *r.CrossMemory != (r.Target == "tasks:mint") {
			t.Errorf("crossMemory must be present on every row and true only for the marked route: %+v", r)
		}
	}
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out2.String(), "\n") {
		switch {
		case strings.Contains(line, "tasks:mint") && !strings.Contains(line, "cross-memory"):
			t.Errorf("the marked route's row must say cross-memory: %q", line)
		case strings.Contains(line, "findings:x") && strings.Contains(line, "cross-memory"):
			t.Errorf("an unmarked route's row must not say cross-memory: %q", line)
		}
	}
}

func routeCrossArgs2736(serverURL string, extra ...string) []string {
	return append([]string{"coding", "preflight", "route", "hrn:node:acme.com:specs:tasks:mint",
		"-m", codingMem, "--route", "mint a spec", "--description", "How to mint", "--server", serverURL}, extra...)
}

func crossTarget2736() string {
	return strings.Replace(codingNodeJSON("n_target", "tasks:mint", "", ""), `"memoryId":"mem1"`, `"memoryId":"mem2","memory":{"urn":"hrn:mem:acme.com:specs"}`, 1)
}

func dataVar2736(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(raw, &v)
	return string(v.Data)
}

func TestPreflightRouteCrossMemoryFlagWritesTheMarker2736(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody), codingRouterWithBody(flatRouterBody)},
		"GetNode":    {crossTarget2736()},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + strings.Replace(newRouteEdgeJSON, `"isRunnable":false`, `"isRunnable":false,"data":`+crossMemoryMarker2736, 1) + `}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory"))
	if err := root.Execute(); err != nil {
		t.Fatalf("--cross-memory on a cross-memory route should succeed, got %v", err)
	}
	if n := len(captured["CreateEdge"]); n != 2 {
		t.Fatalf("want the route edge and the back-edge, got %d", n)
	}
	for i, raw := range captured["CreateEdge"] {
		if got := dataVar2736(t, raw); got != crossMemoryMarker2736 {
			t.Errorf("CreateEdge #%d carries data %s, want %s", i, got, crossMemoryMarker2736)
		}
		if strings.Contains(string(raw), `"name":"cross`) {
			t.Errorf("the marker must never ride in the edge name: %s", raw)
		}
	}
	if _, updated := captured["UpdateEdge"]; updated {
		t.Error("a freshly written marker needs no UpdateEdge")
	}
}

func TestPreflightRouteWithoutTheFlagSendsNoData2736(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody), codingRouterWithBody(flatRouterBody)},
		"GetNode":    {crossTarget2736()},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + newRouteEdgeJSON + `}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for i, raw := range captured["CreateEdge"] {
		if got := dataVar2736(t, raw); got != "" {
			t.Errorf("CreateEdge #%d: no flag, no marker, no data variable, got %s", i, got)
		}
	}
}

// A route to a node in the router's OWN memory has nothing to mark: refuse, and
// write nothing, rather than store a marker that can only mislead.
func TestPreflightRouteCrossMemoryRefusesASameMemoryTarget2736(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {codingNodeJSON("n_target", "findings:x", "", "")},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem1"}}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "route", "findings:x", "-m", codingMem, "--route", "fix a thing",
		"--description", "d", "--cross-memory", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "own memory") {
		t.Fatalf("a same-memory target with --cross-memory is a usage error that says why, got %v", err)
	}
	for _, op := range []string{"CreateEdge", "UpdateEdge", "UpdateNode"} {
		if _, wrote := captured[op]; wrote {
			t.Errorf("nothing may be written on a refusal, saw %s", op)
		}
	}
}

// A real server refuses createEdge on an existing route ("Edge loc ... is already
// occupied", QA on #2814), so marking an EXISTING route never creates: the edge
// the router already reads is updated, keeping its other data EXACTLY (including
// numbers a float64 would round). Same for the target's existing back-edge.
func existingRouter2736(data string) string {
	edge := `{"id":"e_route","name":"to mint a spec","loc":"preflight:to-mint-a-spec:tasks:mint","isRunnable":false,"priority":0,"data":` + data + `,
		"target":{"id":"n_target","name":"tasks:mint","loc":"tasks:mint","memoryId":"mem2"}}`
	return strings.Replace(codingRouterWithBody(flatRouterBody), `"outgoingEdges":[]`, `"outgoingEdges":[`+edge+`]`, 1)
}

func existingTarget2736(data string) string {
	back := `{"id":"e_back","name":"to mint a spec","loc":"tasks:mint:to-mint-a-spec:preflight","isRunnable":false,"priority":0,"data":` + data + `,
		"target":{"id":"root","name":"preflight","loc":"preflight","memoryId":"mem1"}}`
	return strings.Replace(crossTarget2736(), `"outgoingEdges":[]`, `"outgoingEdges":[`+back+`]`, 1)
}

func runExisting2736(t *testing.T, data string, updateAnswer string) (map[string][]json.RawMessage, error) {
	t.Helper()
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {existingRouter2736(data), existingRouter2736(data)},
		"GetNode":    {existingTarget2736(data)},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"UpdateEdge": {updateAnswer},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory", "--json"))
	return captured, root.Execute()
}

func updated2736(data string) string {
	return `{"data":{"updateEdge":{"id":"e","name":"to mint a spec","loc":"l","isRunnable":false,"priority":0,"data":` + data + `,"source":{"id":"root","loc":"preflight"},"target":{"id":"n_target","loc":"tasks:mint"}}}}`
}

func TestPreflightRouteCrossMemoryMarksExistingEdgesWithoutCreating2736(t *testing.T) {
	const big = `{"note":"keep","counter":9007199254740993,"nested":{"a":[1,2.50,3e2]}}`
	captured, err := runExisting2736(t, big, updated2736(`{"note":"keep","counter":9007199254740993,"nested":{"a":[1,2.50,3e2]},"crossMemory":true}`))
	if err != nil {
		t.Fatalf("marking an existing route should succeed, got %v", err)
	}
	if n := len(captured["CreateEdge"]); n != 0 {
		t.Fatalf("an existing route must not be re-created (a real server refuses it), got %d CreateEdge calls", n)
	}
	if n := len(captured["UpdateEdge"]); n != 2 {
		t.Fatalf("want one update for the route edge and one for the back-edge, got %d", n)
	}
	for i, raw := range captured["UpdateEdge"] {
		d := dataVar2736(t, raw)
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(d), &m) != nil || string(m["crossMemory"]) != "true" || string(m["counter"]) != "9007199254740993" ||
			string(m["note"]) != `"keep"` || string(m["nested"]) != `{"a":[1,2.50,3e2]}` {
			t.Errorf("UpdateEdge #%d must add the marker and keep every other value EXACTLY, got %s", i, d)
		}
	}
}

func TestPreflightRouteCrossMemoryAlreadyMarkedWritesNothing2736(t *testing.T) {
	captured, err := runExisting2736(t, crossMemoryMarker2736, updated2736(crossMemoryMarker2736))
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"CreateEdge", "UpdateEdge"} {
		if _, wrote := captured[op]; wrote {
			t.Errorf("an already-marked route needs no %s", op)
		}
	}
}

// The write may have committed: an unconfirmed answer is an unknown outcome, never
// "marked", and is not retried.
func TestPreflightRouteCrossMemoryUnconfirmedUpdateIsAnUnknownOutcome2736(t *testing.T) {
	for name, answer := range map[string]string{
		"null result":         `{"data":{"updateEdge":null}}`,
		"result without data": updated2736(`null`),
		"data without marker": updated2736(`{"note":"keep"}`),
	} {
		t.Run(name, func(t *testing.T) {
			captured, err := runExisting2736(t, `{"note":"keep"}`, answer)
			if err == nil || !strings.Contains(err.Error(), "may not have been") || !strings.Contains(err.Error(), "preflight list") {
				t.Fatalf("an unconfirmed marker update must fail with a read-back instruction, got %v", err)
			}
			if n := len(captured["UpdateEdge"]); n != 1 {
				t.Errorf("no retry: want one UpdateEdge, got %d", n)
			}
		})
	}
}

// Edge data that is not an object cannot take a key without losing what it holds.
func TestPreflightRouteCrossMemoryRefusesNonObjectEdgeData2736(t *testing.T) {
	captured, err := runExisting2736(t, `[1,2]`, updated2736(crossMemoryMarker2736))
	if err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("non-object edge data must be refused, got %v", err)
	}
	if _, wrote := captured["UpdateEdge"]; wrote {
		t.Error("nothing may be overwritten")
	}
}

// --dry-run says what would be marked and writes nothing.
func TestPreflightRouteCrossMemoryDryRun2736(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {crossTarget2736()},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory", "--dry-run"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "marked") || !strings.Contains(out.String(), "cross-memory") {
		t.Errorf("a dry run says the route would be marked, got %q", out.String())
	}
	for _, op := range []string{"CreateEdge", "UpdateEdge", "UpdateNode"} {
		if _, wrote := captured[op]; wrote {
			t.Errorf("a dry run writes nothing, saw %s", op)
		}
	}
}

// Leaf model: the ONE edge belongs to the target and runs target -> router, and it
// carries the marker; nothing else is written and no back-edge exists.
func TestPreflightRouteCrossMemoryLeafModelWritesOneMarkedEdge2736(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {crossTarget2736()},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + strings.Replace(newRouteEdgeJSON, `"isRunnable":false`, `"isRunnable":false,"data":`+crossMemoryMarker2736, 1) + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory", "--route-model", "leaf"))
	if err := root.Execute(); err != nil {
		t.Fatalf("leaf --cross-memory should succeed, got %v", err)
	}
	if n := len(captured["CreateEdge"]); n != 1 {
		t.Fatalf("the leaf model writes exactly one edge, got %d", n)
	}
	var e struct {
		SourceRef string `json:"sourceRef"`
		TargetRef string `json:"targetRef"`
	}
	_ = json.Unmarshal(captured["CreateEdge"][0], &e)
	if e.SourceRef != "n_target" || e.TargetRef != "root" || dataVar2736(t, captured["CreateEdge"][0]) != crossMemoryMarker2736 {
		t.Errorf("the leaf edge runs target -> router and carries the marker, got %+v data %s", e, dataVar2736(t, captured["CreateEdge"][0]))
	}
}

// A marker that is not confirmed on the BACK-edge is reported as exactly that: the
// back-edge exists, only its marker is unconfirmed (not "did not land").
func TestPreflightRouteCrossMemoryBackEdgeMarkerFailureIsNotMisreported2736(t *testing.T) {
	back := `{"id":"e_back","name":"to mint a spec","loc":"tasks:mint:to-mint-a-spec:preflight","isRunnable":false,"priority":0,"data":{"note":"keep"},
		"target":{"id":"root","name":"preflight","loc":"preflight","memoryId":"mem1"}}`
	targetWithBack := strings.Replace(crossTarget2736(), `"outgoingEdges":[]`, `"outgoingEdges":[`+back+`]`, 1)
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {targetWithBack},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + strings.Replace(newRouteEdgeJSON, `"isRunnable":false`, `"isRunnable":false,"data":`+crossMemoryMarker2736, 1) + `}}`},
		"UpdateEdge": {`{"data":{"updateEdge":null}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory", "--no-body-line"))
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "back-edge") || !strings.Contains(err.Error(), "exists") || strings.Contains(err.Error(), "did not land") {
		t.Fatalf("an unconfirmed back-edge marker must not be reported as the back-edge missing, got %v", err)
	}
}

// Under the leaf model the unconfirmed-marker message names the route in its real
// direction (target -> router).
func TestPreflightRouteCrossMemoryLeafMessageDirection2736(t *testing.T) {
	existing := `{"id":"e_leaf","name":"to mint a spec","loc":"tasks:mint:to-mint-a-spec:preflight","isRunnable":false,"priority":0,"data":null,
		"target":{"id":"root","name":"preflight","loc":"preflight","memoryId":"mem1"}}`
	targetWithEdge := strings.Replace(crossTarget2736(), `"outgoingEdges":[]`, `"outgoingEdges":[`+existing+`]`, 1)
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {targetWithEdge},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"UpdateEdge": {`{"data":{"updateEdge":null}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory", "--route-model", "leaf"))
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "the route from the target to the router ") || strings.Contains(err.Error(), "tasks:mint") {
		t.Fatalf("the leaf edge runs target -> router, and the message must say so without the locs typed, got %v", err)
	}
}

// route --json reports the edge's state: a re-run WITHOUT the flag on a marked
// route still says crossMemory true.
func TestPreflightRouteReportsAnExistingMarker2736(t *testing.T) {
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {existingRouter2736(crossMemoryMarker2736), existingRouter2736(crossMemoryMarker2736)},
		"GetNode":    {existingTarget2736(crossMemoryMarker2736)},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + strings.Replace(newRouteEdgeJSON, `"isRunnable":false`, `"isRunnable":false,"data":`+crossMemoryMarker2736, 1) + `}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--json", "--dry-run"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		CrossMemory bool `json:"crossMemory"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || !dto.CrossMemory {
		t.Errorf("the report must reflect the edge's marker, not only this run's flag: %v %s", err, out.String())
	}
}

// An unconfirmed marker update keeps the exit class of what went wrong (cli#2736
// LOCAL delta P2): a refusal is the refusal's exit, not a generic 1, so a script can
// tell an auth failure from an outage. A null answer is an unknown outcome (7).
func TestPreflightRouteCrossMemoryUpdateFailureKeepsItsExitClass2736(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		want         int
	}{
		{"forbidden", `{"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}],"data":null}`, exitcode.Forbidden},
		{"not found", `{"errors":[{"message":"gone","extensions":{"code":"NOT_FOUND"}}],"data":null}`, exitcode.NotFound},
		{"unauthenticated", `{"errors":[{"message":"who","extensions":{"code":"UNAUTHENTICATED"}}],"data":null}`, exitcode.AuthRequired},
		{"null answer", `{"data":{"updateEdge":null}}`, exitcode.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runExisting2736(t, `{"note":"keep"}`, tc.answer)
			if err == nil || exitCodeFor(err) != tc.want {
				t.Fatalf("exit = %d (%v), want %d", exitCodeFor(err), err, tc.want)
			}
			if !strings.Contains(err.Error(), "may not have been") || !strings.Contains(err.Error(), "preflight list") {
				t.Errorf("the read-back instruction stays: %v", err)
			}
		})
	}
}

// createEdge answering nothing under --cross-memory is an unknown outcome too, not
// exit 0 with the marker reported absent.
func TestPreflightRouteCrossMemoryNullCreateIsAnUnknownOutcome2736(t *testing.T) {
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody), codingRouterWithBody(flatRouterBody)},
		"GetNode":    {crossTarget2736()},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":null}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory", "--json"))
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Unavailable || !strings.Contains(err.Error(), "may not have been") {
		t.Fatalf("a null createEdge answer under --cross-memory must be an unknown outcome (exit 7), got %v (exit %d)", err, exitCodeFor(err))
	}
}
