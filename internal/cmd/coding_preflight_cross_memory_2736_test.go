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

func lintOut2736(t *testing.T, outgoing string, batch string, extra ...string) (string, error) {
	t.Helper()
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("preflight", "", outgoing),
		"NodeBatch": batch,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"coding", "preflight", "lint", "-m", codingMem, "--json", "--server", gql.URL}, extra...))
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

// createEdge is idempotent on the derived loc: an existing unmarked route comes
// back as it was. Marking it then takes an UpdateEdge that keeps the edge's other
// data keys; an already-marked route takes no write.
func TestPreflightRouteCrossMemoryMarksAnExistingEdge2736(t *testing.T) {
	existing := strings.Replace(newRouteEdgeJSON, `"isRunnable":false`, `"isRunnable":false,"data":{"note":"keep"}`, 1)
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody), codingRouterWithBody(flatRouterBody)},
		"GetNode":    {crossTarget2736()},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem2"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + existing + `}}`},
		"UpdateEdge": {`{"data":{"updateEdge":` + existing + `}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(routeCrossArgs2736(gql.URL, "--cross-memory"))
	if err := root.Execute(); err != nil {
		t.Fatalf("marking an existing route should succeed, got %v", err)
	}
	if len(captured["UpdateEdge"]) == 0 {
		t.Fatal("an existing unmarked edge must be updated to carry the marker")
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(dataVar2736(t, captured["UpdateEdge"][0])), &data); err != nil || data["crossMemory"] != true || data["note"] != "keep" {
		t.Errorf("the update must add the marker and keep the other data keys, got %v (%v)", data, err)
	}
}
