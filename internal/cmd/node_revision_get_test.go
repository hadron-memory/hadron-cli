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

func liveRevisions(pairs map[string]int) string {
	nodes := []string{}
	for id, rev := range pairs {
		nodes = append(nodes, fmt.Sprintf(`{"id":%q,"revision":%d}`, id, rev))
	}
	return `{"data":{"nodeBatch":{"unavailable":[],"nodes":[` + strings.Join(nodes, ",") + `]}}}`
}

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
			_, _ = w.Write([]byte(liveRevisions(pairs)))
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
