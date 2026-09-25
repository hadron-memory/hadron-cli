package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// #715: `node get` shows Node.revision (hadron-server#1339), read in its own
// small query so every other command keeps working against a server that
// predates it. Against such a server the revision is null — "unknown" — and
// never a guess; any other failure of that read is the command's error.

// nodeGetJSON's and batchNodeJSON's updatedAt: a revision pairs with the
// content only when it was read at the same updatedAt.
const (
	getUpdatedAt   = "2026-08-24T00:00:00Z"
	batchUpdatedAt = "2026-07-27T00:00:00Z"
)

func liveRevisionsAt(updatedAt string, pairs map[string]int) string {
	nodes := []string{}
	for id, rev := range pairs {
		nodes = append(nodes, fmt.Sprintf(`{"id":%q,"revision":%d,"updatedAt":%q}`, id, rev, updatedAt))
	}
	return `{"data":{"nodeBatch":{"unavailable":[],"nodes":[` + strings.Join(nodes, ",") + `]}}}`
}

func liveRevisions(pairs map[string]int) string { return liveRevisionsAt(getUpdatedAt, pairs) }

func runNodeGet(t *testing.T, stubs map[string]string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "get"}, args...), "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

func TestNodeGetShowsTheLiveRevision(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = liveRevisions(map[string]int{"n1": 12})

	out, captured, err := runNodeGet(t, stubs, testNodeURN)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  revision: 12\n") {
		t.Errorf("human output lacks the revision:\n%s", out)
	}
	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeLiveRevisions"], &vars); err != nil || len(vars.Refs) != 1 || vars.Refs[0] != "n1" {
		t.Errorf("the revision read must ask for the node's id: %s (%v)", captured["NodeLiveRevisions"], err)
	}

	out, _, err = runNodeGet(t, stubs, testNodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"revision": 12`) {
		t.Errorf("--json lacks the revision:\n%s", out)
	}
}

// The unstubbed default is exactly an older server's refusal of the field.
func TestNodeGetOnAServerWithoutRevisionsSaysUnknown(t *testing.T) {
	out, _, err := runNodeGet(t, nodeGetStubs(nodeGetJSON(testNodeURL)), testNodeURN)
	if err != nil {
		t.Fatalf("an older server must not fail the read: %v", err)
	}
	if !strings.Contains(out, "revision: unknown (the server predates node revisions)") {
		t.Errorf("an unknown revision must say so, not vanish or guess:\n%s", out)
	}
	out, _, err = runNodeGet(t, nodeGetStubs(nodeGetJSON(testNodeURL)), testNodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"revision": null`) {
		t.Errorf("--json must carry revision:null, never 0 or an absent key:\n%s", out)
	}
}

func TestNodeGetRevisionReadFailureIsTheCommandsError(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`
	if _, _, err := runNodeGet(t, stubs, testNodeURN); err == nil {
		t.Error("a revision read that fails for any reason but an older schema must not be swallowed")
	}
}

// A batch asks for all its nodes' revisions together, in calls no larger than
// nodeBatch's 200, and matches each answer to its node by id.
func TestNodeGetBatchReadsRevisionsInCappedCalls(t *testing.T) {
	const n = 201
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = batchNodeJSON(fmt.Sprintf("n%d", i), fmt.Sprintf("findings:x%d", i))
	}
	var mu sync.Mutex
	var calls [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Refs []string `json:"refs"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "NodeBatch":
			_, _ = w.Write([]byte(nodeBatchResult(nodes, "")))
		case "NodeLiveRevisions":
			mu.Lock()
			calls = append(calls, body.Variables.Refs)
			mu.Unlock()
			pairs := map[string]int{}
			for _, id := range body.Variables.Refs {
				var k int
				_, _ = fmt.Sscanf(id, "n%d", &k)
				pairs[id] = k + 1
			}
			_, _ = w.Write([]byte(liveRevisionsAt(batchUpdatedAt, pairs)))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
		}
	}))
	t.Cleanup(srv.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--prefix", "findings:", "-m", "acme.com::kb", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || len(calls[0]) != 200 || len(calls[1]) != 1 {
		sizes := []int{}
		for _, c := range calls {
			sizes = append(sizes, len(c))
		}
		t.Fatalf("revision calls = %v, want [200 1]", sizes)
	}
	var dto struct {
		Nodes []struct {
			ID       string `json:"id"`
			Revision *int   `json:"revision"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Nodes) != n {
		t.Fatalf("%d nodes, want %d", len(dto.Nodes), n)
	}
	for _, nd := range dto.Nodes {
		var want int
		_, _ = fmt.Sscanf(nd.ID, "n%d", &want)
		if nd.Revision == nil || *nd.Revision != want+1 {
			t.Fatalf("%s: revision %v, want %d (matched by id)", nd.ID, nd.Revision, want+1)
		}
	}
}

// An edit between the content read and the revision read: the revision's
// updatedAt no longer matches, so the whole read repeats, and the answer
// printed pairs content and revision from the same moment (@codex on #724).
func TestNodeGetRereadsWhenTheNodeChangesBetweenReads(t *testing.T) {
	var mu sync.Mutex
	reads := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			reads++
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisions":
			if reads == 1 { // edited after the first content read
				return liveRevisionsAt("2026-08-25T00:00:00Z", map[string]int{"n1": 13})
			}
			return liveRevisions(map[string]int{"n1": 12})
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if reads != 2 || !strings.Contains(out.String(), `"revision": 12`) {
		t.Errorf("reads = %d; want a re-read and the revision matching the content read:\n%s", reads, out.String())
	}
}

func TestNodeGetFailsWhenTheNodeKeepsChanging(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = liveRevisionsAt("2026-09-01T00:00:00Z", map[string]int{"n1": 99})
	_, _, err := runNodeGet(t, stubs, testNodeURN)
	if got := exitCodeFor(err); got != 5 {
		t.Fatalf("exit = %d (err %v), want 5: a pairing it could not verify is not printed", got, err)
	}
}

// A node that turned unavailable between the reads is a change like an edit,
// never "the server predates revisions".
func TestNodeGetTreatsANodeGoneUnavailableAsAChange(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = `{"data":{"nodeBatch":{"unavailable":["n1"],"nodes":[]}}}`
	out, _, err := runNodeGet(t, stubs, testNodeURN)
	if exitCodeFor(err) != 5 || strings.Contains(out, "predates") {
		t.Errorf("err %v; output must not blame the server's age:\n%s", err, out)
	}
}

func TestNodeGetNullRevisionEnvelopeIsAnError(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = `{"data":{"nodeBatch":null}}`
	if _, _, err := runNodeGet(t, stubs, testNodeURN); err == nil {
		t.Error("a null nodeBatch envelope must fail, not read as an older server")
	}
}
