package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const codingMem = "acme.com::kb"

// codingRootJSON is a lint root (review or preflight) carrying the given edges.
func codingRootJSON(loc, incoming, outgoing string) string {
	return codingNodeJSON("root", loc, incoming, outgoing)
}

// codingNodeJSON is one node in the GetNode projection.
func codingNodeJSON(id, loc, incoming, outgoing string) string {
	return `{"data":{"node":{"id":"` + id + `","memoryId":"mem1","loc":"` + loc + `","name":"` + loc + `",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","objectType":null,
		"tags":[],"content":null,"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",
		"outgoingEdges":[` + outgoing + `],"incomingEdges":[` + incoming + `]}}}`
}

// queueGraphQL serves a SEQUENCE of responses per operation, and captures every
// request's variables. `coding review create` reads GetNode twice — once to
// resolve the review parent, once to confirm the new check's edge landed — and
// those two calls must answer differently, which the operation-keyed fake
// cannot express. The last response repeats once the queue is drained.
func queueGraphQL(t *testing.T, responses map[string][]string) (*httptest.Server, map[string][]json.RawMessage) {
	t.Helper()
	captured := map[string][]json.RawMessage{}
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured[body.OperationName] = append(captured[body.OperationName], body.Variables)
		queue, ok := responses[body.OperationName]
		if !ok || len(queue) == 0 {
			t.Errorf("unexpected operation %q", body.OperationName)
			queue = []string{`{"errors":[{"message":"unexpected operation"}]}`}
		}
		i := calls[body.OperationName]
		calls[body.OperationName]++
		if i >= len(queue) {
			i = len(queue) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, queue[i])))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

// inEdge is an edge pointing AT the root; its far endpoint is the source.
func inEdge(id, label, sourceLoc string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":"` + sourceLoc + `:x:review","isRunnable":false,
		"priority":0,"source":{"id":"s_` + id + `","loc":"` + sourceLoc + `","memoryId":"` + codingMem + `"}}`
}

// outEdge is a route OUT of the root; its far endpoint is the target.
func outEdge(id, label, targetLoc string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":"preflight:x:` + targetLoc + `","isRunnable":false,
		"priority":0,"target":{"id":"t_` + id + `","loc":"` + targetLoc + `","memoryId":"` + codingMem + `"}}`
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// codingListNode is one node in the FindNodes listing projection. Membership
// is decided by the loc prefix, so the tag set is immaterial here.
func codingListNode(loc string) string {
	return `{"id":"n_` + loc + `","memoryId":"mem1","loc":"` + loc + `","name":"` + loc + `",
		"nodeType":"info","tags":[],"seq":null,"isRunnable":false,"updatedAt":"2026-07-30T00:00:00Z"}`
}

// codingBatchNode is one node in the nodeBatch projection.
func codingBatchNode(loc, tags, description string) string {
	return `{"id":"n_` + loc + `","memoryId":"mem1","loc":"` + loc + `","name":"` + loc + `",
		"alias":null,"nodeType":"info","objectType":null,"isRunnable":false,"description":` + jsonStr(description) + `,
		"abstract":null,"abstractOriginHash":null,"tags":[` + tags + `],"seq":null,"data":null,"properties":null,
		"content":null,"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
}

// codingBatchWithContent is a one-node batch carrying a body.
func codingBatchWithContent(loc, tags, description, content string) string {
	n := `{"id":"n_` + loc + `","memoryId":"mem1","loc":"` + loc + `","name":"` + loc + `",
		"alias":null,"nodeType":"info","objectType":null,"isRunnable":false,"description":` + jsonStr(description) + `,
		"abstract":null,"abstractOriginHash":null,"tags":[` + tags + `],"seq":null,"data":null,"properties":null,
		"content":` + jsonStr(content) + `,"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
	return codingBatch([]string{n}, "")
}

func codingBatch(nodes []string, unavailable string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[` + unavailable + `],
		"nodes":[` + strings.Join(nodes, ",") + `]}}}`
}

func TestCodingReviewLintClean(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "Applies when a resolver changes", "review:ok"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:ok") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:ok", `"review"`, "Applies when a resolver changes.")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("clean corpus should exit 0, got %v", err)
	}
	if !strings.Contains(out.String(), "1 check(s) OK") {
		t.Errorf("expected the OK summary, got %q", out.String())
	}
}

func TestCodingReviewLintErrorsExit5(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			inEdge("e1", "child-of", "review:bad")+","+inEdge("e2", "", "review:empty"), ""),
		"FindNodes": `{"data":{"nodes":[` +
			codingListNode("review:bad") + `,` + codingListNode("review:empty") + `]}}`,
		"NodeBatch": codingBatch([]string{
			codingBatchNode("review:bad", `"review"`, "Verifies a thing."),
			codingBatchNode("review:empty", `"review"`, "Verifies another thing."),
		}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Errorf("errors should exit 5 (Conflict), got %d", got)
	}
	s := out.String()
	if !strings.Contains(s, "label-is-condition") || !strings.Contains(s, "label-present") {
		t.Errorf("expected both label rules in the table, got %q", s)
	}
}

// Warnings alone exit 0; --strict promotes them and flips the exit code.
func TestCodingReviewLintStrict(t *testing.T) {
	mk := func() string {
		return codingRootJSON("review", inEdge("e1", "Applies when a thing changes", "review:nodesc"), "")
	}
	responses := map[string]string{
		"GetNode":   mk(),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:nodesc") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:nodesc", `"review"`, "")}, ""),
	}

	gql := fakeGraphQL(t, responses)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("warnings alone should exit 0, got %v", err)
	}

	gql2 := fakeGraphQL(t, responses)
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--strict", "--server", gql2.URL})
	if got := exitcode.FromError(root2.Execute()); got != exitcode.Conflict {
		t.Errorf("--strict should promote warnings to errors (exit 5), got %d", got)
	}
}

func TestCodingReviewLintJSON(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "child-of", "review:bad"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:bad") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:bad", `"review"`, "Verifies a thing.")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--json", "--server", gql.URL})
	_ = root.Execute()

	var findings []struct {
		Node     string `json:"node"`
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(out.String()), &findings); err != nil {
		t.Fatalf("--json must emit a JSON array: %v (%q)", err, out.String())
	}
	if len(findings) != 1 || findings[0].Node != "review:bad" || findings[0].Rule != "label-is-condition" {
		t.Errorf("unexpected --json payload: %+v", findings)
	}
	if findings[0].Severity != "error" {
		t.Errorf("severity should be error, got %q", findings[0].Severity)
	}
}

// A node that lists but cannot be read must be surfaced, not dropped.
func TestCodingReviewLintSurfacesUnavailable(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "related", "tasks:ghost"), ""),
		"FindNodes": `{"data":{"nodes":[]}}`,
		"NodeBatch": codingBatch(nil, `"s_e1"`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an unreadable endpoint should warn, not error: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "check-node-resolves") || !strings.Contains(s, "tasks:ghost") {
		t.Errorf("expected the unreadable node surfaced by bare loc, got %q", s)
	}
}

// Decision 3: review reads the edge's SOURCE, preflight reads its TARGET. A
// single-endpoint implementation would fail one of these two.
func TestCodingLintReadsOppositeEndpoints(t *testing.T) {
	// review: only incomingEdges carry the check; an outgoing edge is not one.
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			inEdge("e1", "child-of", "review:src"),
			outEdge("e2", "to somewhere", "review:tgt")),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:src") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:src", `"review"`, "Verifies.")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	_ = root.Execute()
	if !strings.Contains(out.String(), "review:src") {
		t.Errorf("review lint must read the incoming edge's source, got %q", out.String())
	}
	if strings.Contains(out.String(), "review:tgt") {
		t.Errorf("review lint must ignore outgoing edges, got %q", out.String())
	}

	// preflight: only outgoingEdges are routes.
	gql2 := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("preflight",
			inEdge("e3", "whatever", "findings:inbound"),
			outEdge("e4", "routes-to", "findings:route")),
		"NodeBatch": codingBatch([]string{codingBatchNode("findings:route", "", "")}, ""),
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--server", gql2.URL})
	_ = root2.Execute()
	if !strings.Contains(out2.String(), "findings:route") {
		t.Errorf("preflight lint must read the outgoing edge's target, got %q", out2.String())
	}
	if strings.Contains(out2.String(), "findings:inbound") {
		t.Errorf("preflight lint must ignore incoming edges, got %q", out2.String())
	}
}

func TestCodingPreflightLintDeadRouteExit5(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("preflight", "", outEdge("e1", "to do the thing", "findings:gone")),
		"NodeBatch": codingBatch(nil, `"t_e1"`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Errorf("a dead route should exit 5, got %d", got)
	}
	if !strings.Contains(out.String(), "route-target-resolves") {
		t.Errorf("expected route-target-resolves, got %q", out.String())
	}
}

// Route targets are read by node id. Rebuilding a URN from the router's own
// memory would look a cross-memory target up in the wrong place — reporting a
// live node as a dead route, or linting a same-loc node from the home memory.
func TestCodingPreflightReadsTargetsByID(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("preflight", "", outEdge("e1", "to do the thing", "findings:elsewhere")),
		"NodeBatch": codingBatch([]string{codingBatchNode("findings:elsewhere", "", "")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--server", gql.URL})
	_ = root.Execute()

	var got struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &got); err != nil {
		t.Fatalf("decoding NodeBatch vars: %v", err)
	}
	if len(got.Refs) != 1 || got.Refs[0] != "t_e1" {
		t.Errorf("targets must be batch-read by node id, got %v", got.Refs)
	}
	for _, r := range got.Refs {
		if strings.HasPrefix(r, "hrn:") {
			t.Errorf("a rebuilt URN (%q) resolves in the router's memory, not the target's", r)
		}
	}
}

// --fix must relabel via updateEdge. Going through updateNode(edges:) would
// replace the node's whole outgoing edge set and destroy sibling edges (#325).
func TestCodingReviewFixUsesUpdateEdgeOnly(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "child-of", "review:fixable"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:fixable") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:fixable", `"review"`, "Applies when a resolver changes. More prose.")}, ""),
		"UpdateEdge": `{"data":{"updateEdge":{"id":"e1","name":"Applies when a resolver changes","loc":"l",
			"isRunnable":false,"priority":0,"source":{"id":"s","loc":"review:fixable"},"target":{"id":"r","loc":"review"}}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--fix", "--yes", "--server", gql.URL})
	_ = root.Execute()

	vars, ok := captured["UpdateEdge"]
	if !ok {
		t.Fatal("--fix must issue UpdateEdge")
	}
	var got struct {
		EdgeID string  `json:"edgeId"`
		Name   *string `json:"name"`
	}
	if err := json.Unmarshal(vars, &got); err != nil {
		t.Fatalf("decoding UpdateEdge vars: %v", err)
	}
	if got.EdgeID != "e1" {
		t.Errorf("UpdateEdge should target the edge id, got %q", got.EdgeID)
	}
	if got.Name == nil || *got.Name != "Applies when a resolver changes" {
		t.Errorf("unexpected promoted label: %v", got.Name)
	}
	if _, bad := captured["UpdateNode"]; bad {
		t.Error("--fix must never call UpdateNode — it would replace the whole edge set")
	}
}

// A broken label whose description carries no trigger needs a human; --fix must
// leave it alone rather than invent a condition.
func TestCodingReviewFixSkipsUnfixable(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "child-of", "review:manual"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:manual") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:manual", `"review"`, "Verifies the codegen output.")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--fix", "--yes", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Conflict {
		t.Errorf("the unfixable error should still be reported, got exit %d", got)
	}
	if _, bad := captured["UpdateEdge"]; bad {
		t.Error("--fix must not write when no description carries a trigger")
	}
}

func TestCodingReviewFixRequiresYes(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "child-of", "review:fixable"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:fixable") + `]}}`,
		"NodeBatch": codingBatch([]string{codingBatchNode("review:fixable", `"review"`, "Applies when a resolver changes.")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--fix", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Error("--fix without --yes must fail non-interactively")
	}
}

func TestCodingReviewListShowsTriggers(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			inEdge("e1", "Applies when a resolver changes", "review:ok")+","+inEdge("e2", "child-of", "review:bad"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:ok") + `,` + codingListNode("review:bad") + `]}}`,
		"NodeBatch": codingBatch([]string{
			codingBatchNode("review:ok", `"review"`, "d"),
			codingBatchNode("review:bad", `"review"`, "d"),
		}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "list", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("list is read-only and must exit 0, got %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Applies when a resolver changes") {
		t.Errorf("expected the trigger column, got %q", s)
	}
	// A broken check still LISTS — the view is "what is in the checklist",
	// with the linter's verdict as a column.
	if !strings.Contains(s, "review:bad") || !strings.Contains(s, "broken") {
		t.Errorf("expected the broken check listed with its status, got %q", s)
	}
}

func TestCodingReviewListBrokenJSON(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			inEdge("e1", "Applies when a resolver changes", "review:ok")+","+inEdge("e2", "child-of", "review:bad"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:ok") + `,` + codingListNode("review:bad") + `]}}`,
		"NodeBatch": codingBatch([]string{
			codingBatchNode("review:ok", `"review"`, "d"),
			codingBatchNode("review:bad", `"review"`, "d"),
		}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "list", "-m", codingMem, "--broken", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("list must exit 0 even with broken checks, got %v", err)
	}
	var rows []struct {
		Loc     string   `json:"loc"`
		Trigger string   `json:"trigger"`
		Status  string   `json:"status"`
		Tags    []string `json:"tags"`
		EdgeID  string   `json:"edgeId"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("--json must emit a JSON array: %v (%q)", err, out.String())
	}
	if len(rows) != 1 || rows[0].Loc != "review:bad" {
		t.Fatalf("--broken should list only the broken check, got %+v", rows)
	}
	if rows[0].Status != "broken" || rows[0].EdgeID != "e2" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
	if rows[0].Tags == nil {
		t.Error("empty slices must render as [], not null")
	}
}

func TestCodingPreflightList(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("preflight", "",
			outEdge("e1", "to fix a failing build", "ops:ci")+","+outEdge("e2", "to read the dead one", "findings:gone")),
		"NodeBatch": codingBatch([]string{codingBatchNode("ops:ci", "", "")}, `"t_e2"`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("list is read-only and must exit 0 even with a dead route, got %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "to fix a failing build") || !strings.Contains(s, "ops:ci") {
		t.Errorf("expected the route label and target, got %q", s)
	}
	if !strings.Contains(s, "findings:gone") || !strings.Contains(s, "broken") {
		t.Errorf("expected the dead route listed as broken, got %q", s)
	}
}

// The whole point of `review create`: the node and the edge that makes it
// discoverable are written together, so there is no window in which the check
// exists but is invisible.
func TestCodingReviewCreateWiresParentEdge(t *testing.T) {
	newNode := `{"id":"n_new","memoryId":"mem1","loc":"review:thin-resolver","name":"thin-resolver",
		"nodeType":"info","tags":["review","review-criteria"],"seq":null,"isRunnable":false,
		"updatedAt":"2026-08-04T00:00:00Z"}`
	confirmEdge := `{"id":"e_new","name":"Applies when a resolver changes","loc":"l","isRunnable":false,
		"priority":0,"target":{"id":"root","loc":"review","memoryId":"` + codingMem + `"}}`
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("review", "", ""),                                 // the parent
			codingNodeJSON("n_new", "review:thin-resolver", "", confirmEdge), // the confirm read
		},
		"CreateNode": {`{"data":{"createNode":` + newNode + `}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "thin-resolver", "-m", codingMem,
		"--trigger", "a resolver changes", "--description", "Resolver fields stay thin.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("create should succeed, got %v", err)
	}

	var got struct {
		Input struct {
			Loc         string   `json:"loc"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Content     string   `json:"content"`
			Tags        []string `json:"tags"`
			Edges       []struct {
				TargetID string `json:"targetId"`
				Name     string `json:"name"`
			} `json:"edges"`
		} `json:"input"`
	}
	if len(captured["CreateNode"]) != 1 {
		t.Fatalf("expected exactly one CreateNode, got %d", len(captured["CreateNode"]))
	}
	if err := json.Unmarshal(captured["CreateNode"][0], &got); err != nil {
		t.Fatalf("decoding CreateNode vars: %v", err)
	}
	if got.Input.Loc != "review:thin-resolver" {
		t.Errorf("loc = %q, want the root prefix applied", got.Input.Loc)
	}
	if len(got.Input.Edges) != 1 {
		t.Fatalf("the check must be created WITH its parent edge, got %+v", got.Input.Edges)
	}
	if got.Input.Edges[0].TargetID != "root" {
		t.Errorf("the edge must target the resolved parent id, got %q", got.Input.Edges[0].TargetID)
	}
	if got.Input.Edges[0].Name != "Applies when a resolver changes" {
		t.Errorf("the edge label is the trigger with the stem applied, got %q", got.Input.Edges[0].Name)
	}
	if !strings.Contains(got.Input.Content, "> **Scope.**") {
		t.Errorf("the scaffolded body must open with a Scope blockquote, got %q", got.Input.Content)
	}
	if strings.Join(got.Input.Tags, ",") != "review,review-criteria" {
		t.Errorf("unexpected tags: %v", got.Input.Tags)
	}
	if !strings.Contains(out.String(), "review:thin-resolver") {
		t.Errorf("expected the created loc in the output, got %q", out.String())
	}
}

// A --link commonly points at the canonical convention node in ANOTHER memory.
// -m is required here (it names where the check is created), and ResolveNodeRef
// reads its ref as a bare loc whenever a memory is given — so a qualified ref
// must be resolved verbatim, not composed into the check's memory, where it
// would resolve to nothing (Codex review on #346).
func TestCodingReviewCreateCrossMemoryLink(t *testing.T) {
	const linkURN = "hrn:node:hadronmemory.com:dev:conventions:output-contract"
	newNode := `{"id":"n_new","memoryId":"mem1","loc":"review:linked","name":"linked",
		"nodeType":"info","tags":["review","review-criteria"],"seq":null,"isRunnable":false,
		"updatedAt":"2026-08-04T00:00:00Z"}`
	confirmEdge := `{"id":"e_new","name":"Applies when a thing changes","loc":"l","isRunnable":false,
		"priority":0,"target":{"id":"root","loc":"review","memoryId":"` + codingMem + `"}}`
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("review", "", ""),
			codingNodeJSON("n_new", "review:linked", "", confirmEdge),
		},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_link","kind":"node","memoryId":"mem2"}}}`},
		"CreateNode": {`{"data":{"createNode":` + newNode + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "linked", "-m", codingMem,
		"--trigger", "a thing changes", "--description", "d",
		"--link", linkURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a cross-memory --link should resolve, got %v", err)
	}

	if len(captured["ResolveUrn"]) != 1 {
		t.Fatalf("expected the link to be resolved once, got %d", len(captured["ResolveUrn"]))
	}
	var resolved struct {
		URN string `json:"urn"`
	}
	if err := json.Unmarshal(captured["ResolveUrn"][0], &resolved); err != nil {
		t.Fatalf("decoding ResolveUrn vars: %v", err)
	}
	if resolved.URN != linkURN {
		t.Errorf("the link must resolve verbatim, got %q — a re-scoped ref resolves in the wrong memory", resolved.URN)
	}

	var got struct {
		Input struct {
			Edges []struct {
				TargetID string `json:"targetId"`
			} `json:"edges"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateNode"][0], &got); err != nil {
		t.Fatalf("decoding CreateNode vars: %v", err)
	}
	if len(got.Input.Edges) != 2 || got.Input.Edges[1].TargetID != "n_link" {
		t.Errorf("the link edge should carry the resolved id, got %+v", got.Input.Edges)
	}
}

// If the edge did not land, the check exists but is invisible. That is a
// partial write: report it and exit 1, never 0.
func TestCodingReviewCreateUnwiredExits1(t *testing.T) {
	newNode := `{"id":"n_new","memoryId":"mem1","loc":"review:orphan","name":"orphan",
		"nodeType":"info","tags":["review"],"seq":null,"isRunnable":false,"updatedAt":"2026-08-04T00:00:00Z"}`
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("review", "", ""),
			codingNodeJSON("n_new", "review:orphan", "", ""), // no edge came back
		},
		"CreateNode": {`{"data":{"createNode":` + newNode + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "orphan", "-m", codingMem,
		"--trigger", "a thing changes", "--description", "d", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.Error {
		t.Errorf("an unwired check is a partial write (exit 1), got %d", got)
	}
}

// A missing parent means the check would hang off nothing — fail before
// writing rather than leaving an orphan behind.
func TestCodingReviewCreateMissingParent(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNode": {`{"data":{"node":null}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "x", "-m", codingMem,
		"--trigger", "a thing changes", "--description", "d", "--server", gql.URL})
	if got := exitcode.FromError(root.Execute()); got != exitcode.NotFound {
		t.Errorf("a missing review parent should exit 4, got %d", got)
	}
	if _, wrote := captured["CreateNode"]; wrote {
		t.Error("nothing may be written when the parent does not resolve")
	}
}

// Local validation runs before any round trip: a label the linter would reject
// must never reach the server.
func TestCodingReviewCreateRejectsBadInput(t *testing.T) {
	cases := [][]string{
		{"coding", "review", "create", "x", "-m", codingMem, "--trigger", "Applies when", "--description", "d"},
		{"coding", "review", "create", "x", "-m", codingMem, "--trigger", "a thing changes"},
		{"coding", "review", "create", "graphql:x", "-m", codingMem, "--trigger", "a thing changes", "--description", "d"},
	}
	for _, args := range cases {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
			t.Errorf("%v should be a usage error (2), got %d", args[3:], got)
		}
	}
}

func TestCodingLintRequiresMemory(t *testing.T) {
	for _, args := range [][]string{
		{"coding", "review", "lint"},
		{"coding", "preflight", "lint"},
		{"coding", "review", "list"},
		{"coding", "preflight", "list"},
		{"coding", "review", "create", "x", "--trigger", "a thing changes", "--description", "d"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if got := exitcode.FromError(root.Execute()); got != exitcode.Usage {
			t.Errorf("%v without -m should be a usage error, got %d", args, got)
		}
	}
}

// review lint needs node bodies to quote a check's scope; preflight lint never
// reads them, so it must not retain a body per route target.
func TestCodingFetchNodesContentIsOptIn(t *testing.T) {
	body := "> **Scope.** Adding an argument that identifies an entity."

	// review lint: the body reaches the finding.
	gql := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("review", inEdge("e1", "child-of", "review:x"), ""),
		"FindNodes": `{"data":{"nodes":[` + codingListNode("review:x") + `]}}`,
		"NodeBatch": codingBatchWithContent("review:x", `"review"`, "d", body),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	_ = root.Execute()
	if !strings.Contains(out.String(), "Adding an argument that identifies") {
		t.Errorf("review lint should quote the body scope, got %q", out.String())
	}

	// preflight lint: same payload, but nothing derived from the body.
	gql2 := fakeGraphQL(t, map[string]string{
		"GetNode":   codingRootJSON("preflight", "", outEdge("e2", "to do the thing", "findings:r")),
		"NodeBatch": codingBatchWithContent("findings:r", "", "d", body),
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--server", gql2.URL})
	_ = root2.Execute()
	if strings.Contains(out2.String(), "Adding an argument") {
		t.Errorf("preflight lint must not surface node bodies, got %q", out2.String())
	}
}
