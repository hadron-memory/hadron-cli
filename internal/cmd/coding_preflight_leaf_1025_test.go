package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1025 (CHECKED T1; Holger's ruling on hadron-server#1797): a leaf owns
// the edge to the router (or to an area parent), labelled `to <action>`.
// `--route-model leaf` writes exactly that edge and nothing on the router's
// outgoing set; `list` and `lint` read incoming edges. The default model stays
// `mirrored`, byte-identical, so memories on the documented mirrored-pair model
// are untouched.

// leafRouter is the router read (GetNode) carrying a body and both edge sets.
func leafRouter(content, incoming, outgoing string) string {
	return `{"data":{"node":{"id":"root","memoryId":"mem1","loc":"preflight","name":"preflight",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","objectType":null,
		"tags":[],"content":` + jsonStr(content) + `,"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",
		"outgoingEdges":[` + outgoing + `],"incomingEdges":[` + incoming + `]}}}`
}

// leafIn is an edge from a leaf INTO the router.
func leafIn(id, label, leafLoc string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":"` + leafLoc + `:to-router","isRunnable":false,
		"priority":0,"source":{"id":"n_` + leafLoc + `","loc":"` + leafLoc + `","memoryId":"mem1"}}`
}

// areaRouter is codingRouterWithBody for an area node rather than the router.
func areaRouter(content string) string {
	r := strings.Replace(codingRouterWithBody(content), `"id":"root"`, `"id":"area"`, 1)
	return strings.Replace(r, `"loc":"preflight"`, `"loc":"preflight:db"`, 1)
}

// leafEdgeJSON is the new node's outgoing edge to `target`, as the confirm read sees it.
func leafEdgeJSON(targetID, targetLoc string) string {
	return `{"id":"e_leaf","name":"to fix a flaky OTP test","loc":"l","isRunnable":false,"priority":0,
		"target":{"id":"` + targetID + `","loc":"` + targetLoc + `","memoryId":"mem1"}}`
}

func leafCreateArgs(serverURL string, extra ...string) []string {
	return preflightCreateArgs(serverURL, append([]string{"--route-model", "leaf"}, extra...)...)
}

type capturedEdges struct {
	Input struct {
		Edges []struct {
			TargetID string `json:"targetId"`
			Name     string `json:"name"`
		} `json:"edges"`
	} `json:"input"`
}

func TestC1025LeafCreateWritesOnlyTheLeafEdge(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {newRouteNodeRead(leafEdgeJSON("root", "preflight"))},
		"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(leafCreateArgs(gql.URL, "--json"))
	if err := root.Execute(); err != nil {
		t.Fatalf("leaf create should succeed, got %v", err)
	}
	var in capturedEdges
	if len(captured["CreateNode"]) != 1 {
		t.Fatalf("want one CreateNode, got %d", len(captured["CreateNode"]))
	}
	_ = json.Unmarshal(captured["CreateNode"][0], &in)
	if len(in.Input.Edges) != 1 || in.Input.Edges[0].TargetID != "root" || in.Input.Edges[0].Name != "to fix a flaky OTP test" {
		t.Fatalf("the leaf must own exactly one edge to the router labelled to <action>, got %+v", in.Input.Edges)
	}
	for _, op := range []string{"CreateEdge", "UpdateNode"} {
		if _, wrote := captured[op]; wrote {
			t.Errorf("leaf mode issued %s: nothing may be written on the router (no outgoing edge, no back-edge, body line off by default)", op)
		}
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if dto["backEdge"] != false || dto["bodyLine"] != "" || dto["router"] != "preflight" {
		t.Errorf("dto must say no back-edge, no body line, attached to the router: %v", dto)
	}
}

func TestC1025LeafCreateAttachToAnAreaNode(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {areaRouter(flatRouterBody)},
		"GetNode":    {newRouteNodeRead(leafEdgeJSON("area", "preflight:db"))},
		"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(leafCreateArgs(gql.URL, "--attach-to", "preflight:db"))
	if err := root.Execute(); err != nil {
		t.Fatalf("attach-to should succeed, got %v", err)
	}
	var in capturedEdges
	_ = json.Unmarshal(captured["CreateNode"][0], &in)
	if len(in.Input.Edges) != 1 || in.Input.Edges[0].TargetID != "area" {
		t.Fatalf("the edge must run leaf -> the attach target, got %+v", in.Input.Edges)
	}
}

func TestC1025LeafCreateSectionSplicesIntoTheAttachTarget(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {areaRouter(flatRouterBody)},
		"GetNode":    {newRouteNodeRead(leafEdgeJSON("area", "preflight:db"))},
		"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(leafCreateArgs(gql.URL, "--attach-to", "preflight:db", "--section", ""))
	// --section "" is "the preamble"; an explicit flag is the opt-in.
	if err := root.Execute(); err != nil {
		t.Fatalf("--section opts the body line in, got %v", err)
	}
	if len(captured["UpdateNode"]) != 1 {
		t.Fatalf("want one body update, got %d", len(captured["UpdateNode"]))
	}
	var upd struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"][0], &upd)
	var id, body string
	_ = json.Unmarshal(upd.Input["id"], &id)
	_ = json.Unmarshal(upd.Input["content"], &body)
	if id != "area" {
		t.Errorf("the line belongs in the ATTACH TARGET's body (area), written to %q", id)
	}
	if _, sent := upd.Input["edges"]; sent {
		t.Error("the body update must never send edges")
	}
	if !strings.Contains(body, "[[findings:flaky-otp-timer]]") {
		t.Errorf("body lacks the routing line:\n%s", body)
	}
}

func TestC1025LeafBodyLineFlags(t *testing.T) {
	// --no-body-line is accepted as a documented no-op in leaf mode.
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {newRouteNodeRead(leafEdgeJSON("root", "preflight"))},
		"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(leafCreateArgs(gql.URL, "--no-body-line", "--no-back-edge"))
	if err := root.Execute(); err != nil {
		t.Fatalf("--no-body-line and --no-back-edge are no-ops in leaf mode, got %v", err)
	}
	if _, wrote := captured["UpdateNode"]; wrote {
		t.Error("--no-body-line wrote a body")
	}

	// --section with --no-body-line stays the usage error it is in mirrored mode.
	gql2, _ := queueGraphQL(t, map[string][]string{"GetNodeRaw": {codingRouterWithBody(flatRouterBody)}})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs(leafCreateArgs(gql2.URL, "--no-body-line", "--section", "x"))
	if got := exitCodeFor(root2.Execute()); got != exitcode.Usage {
		t.Errorf("--section with --no-body-line must exit 2, got %d", got)
	}
}

func TestC1025LeafCreateDryRunWritesNothing(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(leafCreateArgs(gql.URL, "--dry-run", "--json"))
	if err := root.Execute(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	for _, op := range []string{"CreateNode", "CreateEdge", "UpdateNode"} {
		if _, wrote := captured[op]; wrote {
			t.Errorf("--dry-run issued %s", op)
		}
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto["dryRun"] != true || dto["backEdge"] != false || dto["bodyLine"] != "" {
		t.Errorf("leaf dry-run must show the single leaf edge and no line: %v", dto)
	}
}

func TestC1025LeafRouteWritesOnlyTheLeafsEdge(t *testing.T) {
	target := codingNodeJSON("n_target", "findings:flaky-otp-timer", "", "")
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {target},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem1"}}}`},
		"CreateEdge": {`{"data":{"createEdge":` + newRouteEdgeJSON + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "route", "findings:flaky-otp-timer", "-m", codingMem,
		"--route-model", "leaf", "--route", "fix a flaky OTP test", "--description", "d", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("leaf route should succeed, got %v", err)
	}
	if n := len(captured["CreateEdge"]); n != 1 {
		t.Fatalf("want exactly the leaf's edge, got %d CreateEdge calls", n)
	}
	var e struct{ SourceRef, TargetRef, Name string }
	_ = json.Unmarshal(captured["CreateEdge"][0], &e)
	if e.SourceRef != "n_target" || e.TargetRef != "root" || e.Name != "to fix a flaky OTP test" {
		t.Errorf("the edge must run leaf -> router: %+v", e)
	}
	if _, wrote := captured["UpdateNode"]; wrote {
		t.Error("leaf mode writes no body line unless --section is given")
	}
}

func TestC1025RouteModelValidation(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(preflightCreateArgs(gql.URL, "--route-model", "sideways"))
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("an unknown --route-model must exit 2, got %d", got)
	}
}

// flag > config > default, resolved by what the write actually does.
func TestC1025RouteModelPrecedence(t *testing.T) {
	run := func(t *testing.T, cfg string, extra ...string) map[string][]json.RawMessage {
		t.Helper()
		dir := t.TempDir()
		if cfg != "" {
			if err := os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".hadron", "config.json"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Chdir(dir)
		gql, captured := queueGraphQL(t, map[string][]string{
			"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
			"GetNode":    {newRouteNodeRead(backEdgeJSON)},
			"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
			"CreateEdge": {`{"data":{"createEdge":` + newRouteEdgeJSON + `}}`},
			"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(preflightCreateArgs(gql.URL, extra...))
		if err := root.Execute(); err != nil {
			t.Fatalf("create: %v", err)
		}
		return captured
	}
	isLeaf := func(c map[string][]json.RawMessage) bool { _, router := c["CreateEdge"]; return !router }

	if isLeaf(run(t, "")) {
		t.Error("default must stay mirrored")
	}
	if !isLeaf(run(t, `{"preflightRouteModel":"leaf"}`)) {
		t.Error("config preflightRouteModel=leaf must select the leaf model")
	}
	if isLeaf(run(t, `{"preflightRouteModel":"leaf"}`, "--route-model", "mirrored")) {
		t.Error("the flag must beat the config")
	}
	if !isLeaf(run(t, `{"preflightRouteModel":"mirrored"}`, "--route-model", "leaf")) {
		t.Error("the flag must beat the config (leaf over mirrored)")
	}
}

func TestC1025BadConfigModelIsUsage(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".hadron", "config.json"), []byte(`{"preflightRouteModel":"both"}`), 0o644)
	t.Chdir(dir)
	gql := fakeGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(preflightCreateArgs(gql.URL))
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("a bad preflightRouteModel must exit 2, got %d", got)
	}
}

// One fixture memory carrying both models: two leaf-owned edges (one also a
// legacy outgoing route) and one legacy-only route.
func mixedRouter() string {
	return leafRouter("# Preflight\n\n- **\"x\"** → [[findings:leaf-a]] — a\n- **\"y\"** → [[findings:unlinked]] — b\n",
		leafIn("i1", "to fix a build", "findings:leaf-a")+","+leafIn("i2", "fix without stem", "findings:leaf-b"),
		outEdge("o1", "to fix a build", "findings:leaf-a")+","+outEdge("o2", "to read legacy", "findings:legacy"))
}

func mixedBatch() string {
	return codingBatch([]string{
		codingBatchNode("findings:leaf-a", "", ""), codingBatchNode("findings:leaf-b", `"retired"`, ""),
		codingBatchNode("findings:legacy", "", ""),
	}, "")
}

func TestC1025ListMergesBothModelsOneRowPerLeaf(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"GetNode": mixedRouter(), "NodeBatch": mixedBatch()})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--route-model", "leaf", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	models := map[string]string{}
	for _, r := range rows {
		models[r["target"].(string)] = r["model"].(string)
	}
	want := map[string]string{"findings:leaf-a": "leaf", "findings:leaf-b": "leaf", "findings:legacy": "legacy"}
	if len(rows) != 3 || fmt.Sprint(models) != fmt.Sprint(want) {
		t.Fatalf("want one row per distinct leaf, leaf winning over legacy: %v", models)
	}
}

func TestC1025MirroredListIsByteIdentical(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   mixedRouter(),
		"NodeBatch": mixedBatch(),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"model"`) {
		t.Errorf("the mirrored model's --json must not grow a model field:\n%s", out.String())
	}
	var rows []map[string]any
	_ = json.Unmarshal([]byte(out.String()), &rows)
	if len(rows) != 2 {
		t.Errorf("mirrored reads outgoing routes only (2), got %d", len(rows))
	}
}

func TestC1025LeafLintRulesOnAMixedMemory(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"GetNode": mixedRouter(), "NodeBatch": mixedBatch()})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--route-model", "leaf", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("warnings alone exit 0, got %v", err)
	}
	s := out.String()
	for _, want := range []string{
		`"route-label-phrasing"`,  // incoming label without the stem (leaf-b)
		`"route-target-retired"`,  // retired SOURCE leaf (leaf-b)
		`"legacy-outgoing-route"`, // router still routes out (leaf-a, legacy)
		`"leaf-missing-edge"`,     // body links findings:unlinked, no edge to the router
		`findings:unlinked`,       // ... and names it
	} {
		if !strings.Contains(s, want) {
			t.Errorf("lint lacks %s:\n%s", want, s)
		}
	}
	// --strict promotes the warnings to errors.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--route-model", "leaf", "--strict", "--server", gql.URL})
	if got := exitCodeFor(root2.Execute()); got != exitcode.Conflict {
		t.Errorf("--strict on a mixed memory must exit 5, got %d", got)
	}
}

func TestC1025LeafLintNamesWhatItCannotSee(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   leafRouter("# Preflight\n", leafIn("i1", "to fix a build", "findings:leaf-a"), ""),
		"NodeBatch": codingBatch([]string{codingBatchNode("findings:leaf-a", "", "")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--route-model", "leaf", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 incoming edges visible to this caller") {
		t.Errorf("lint must never claim completeness, got %q", out.String())
	}
}

func TestC1025MirroredLintIsUnchanged(t *testing.T) {
	// The legacy rules run as they do today: no leaf rules, incoming edges ignored.
	gql := fakeGraphQL(t, map[string]string{"GetNode": mixedRouter(), "NodeBatch": mixedBatch()})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--json", "--server", gql.URL})
	_ = root.Execute()
	for _, banned := range []string{"legacy-outgoing-route", "leaf-missing-edge", "visible to this caller"} {
		if strings.Contains(out.String(), banned) {
			t.Errorf("the mirrored model must not run %q:\n%s", banned, out.String())
		}
	}
}

// The router already has 115 incoming edges: every one must be read.
func TestC1025LeafListReadsEveryIncomingEdge(t *testing.T) {
	var in []string
	var nodes []string
	for i := 0; i < 160; i++ {
		loc := fmt.Sprintf("findings:leaf-%03d", i)
		in = append(in, leafIn(fmt.Sprintf("i%d", i), "to do thing "+fmt.Sprint(i), loc))
		nodes = append(nodes, codingBatchNode(loc, "", ""))
	}
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   leafRouter("# Preflight\n", strings.Join(in, ","), ""),
		"NodeBatch": codingBatch(nodes, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--route-model", "leaf", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	_ = json.Unmarshal([]byte(out.String()), &rows)
	if len(rows) != 160 {
		t.Errorf("want all 160 incoming edges as rows, got %d", len(rows))
	}
}
