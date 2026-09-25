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

func liveRevisionsAt(_ string, pairs map[string]int) string {
	nodes := []string{}
	for id, rev := range pairs {
		nodes = append(nodes, fmt.Sprintf(`{"id":%q,"revision":%d}`, id, rev))
	}
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + strings.Join(nodes, ",") + `]}}}`
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
			if len(body.Variables.Refs) == 0 { // the prefix-mode "before" read
				pairs := map[string]int{}
				for i := 0; i < n; i++ {
					pairs[fmt.Sprintf("n%d", i)] = i + 1
				}
				_, _ = w.Write([]byte(liveRevisionsAt(batchUpdatedAt, pairs)))
				return
			}
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
	// Before: one prefix-mode call (no refs). After: by id, capped at 200.
	if len(calls) != 3 || len(calls[0]) != 0 || len(calls[1]) != 200 || len(calls[2]) != 1 {
		sizes := []int{}
		for _, c := range calls {
			sizes = append(sizes, len(c))
		}
		t.Fatalf("revision calls = %v, want [0 200 1]", sizes)
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

// revisionSequence answers NodeLiveRevisions for n1 with the next value of
// revs on each call (the last one repeats), and counts content reads.
func revisionSequence(t *testing.T, revs ...int) (*httptest.Server, func() (reads, revCalls int)) {
	t.Helper()
	var mu sync.Mutex
	reads, calls := 0, 0
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
			rev := revs[min(calls, len(revs)-1)]
			calls++
			// updatedAt is deliberately the SAME throughout: the Codex case,
			// two writes sharing one timestamp. Only the revision tells.
			return liveRevisions(map[string]int{"n1": rev})
		}
		return ""
	})
	return gql, func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return reads, calls
	}
}

// An edit lands between the revision read before the content and the one
// after it: the brackets disagree, so the whole read repeats, and the answer
// printed pairs content and revision from the same moment (@codex on #724).
// updatedAt never changes here, so a timestamp check could not have caught it.
func TestNodeGetRereadsWhenTheNodeChangesBetweenReads(t *testing.T) {
	gql, counts := revisionSequence(t, 12, 13, 13, 13)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	reads, calls := counts()
	if reads != 2 || calls != 4 || !strings.Contains(out.String(), `"revision": 13`) {
		t.Errorf("reads=%d revision calls=%d; want a full re-read and the revision bracketing the content read:\n%s", reads, calls, out.String())
	}
}

func TestNodeGetFailsWhenTheNodeKeepsChanging(t *testing.T) {
	gql, counts := revisionSequence(t, 1, 2, 3, 4, 5, 6, 7)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--server", gql.URL})
	err := root.Execute()
	if got := exitCodeFor(err); got != 5 {
		t.Fatalf("exit = %d (err %v), want 5: a pairing it could not verify is not printed", got, err)
	}
	if reads, _ := counts(); reads != 3 {
		t.Errorf("content reads = %d, want 3 attempts", reads)
	}
}

// A node that turned unavailable between the reads is a change like an edit,
// never "the server predates revisions".
func TestNodeGetTreatsANodeGoneUnavailableAsAChange(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisions":
			calls++
			if calls%2 == 1 { // before: readable
				return liveRevisions(map[string]int{"n1": 12})
			}
			return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":["n1"],"nodes":[]}}}`
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != 5 || strings.Contains(out.String(), "predates") {
		t.Errorf("err %v; output must not blame the server's age:\n%s", err, out.String())
	}
}

func TestNodeGetNullRevisionEnvelopeIsAnError(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = `{"data":{"nodeBatch":null}}`
	_, _, err := runNodeGet(t, stubs, testNodeURN)
	if exitCodeFor(err) != 1 || err == nil || !strings.Contains(err.Error(), "no result") {
		t.Errorf("err = %v; a null envelope is its own failure (exit 1), not an older server and not a retried race", err)
	}
}

// Absent from BOTH revision reads (unreadable to them, though the content
// read returned it): the two absences must not pair as revision 0 == 0. A
// revision is never fabricated.
func TestNodeGetNeverPairsTwoAbsences(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":["n1"],"nodes":[]}}}`
	out, _, err := runNodeGet(t, stubs, testNodeURN, "--json")
	if strings.Contains(out, `"revision": 0`) {
		t.Fatalf("a revision was fabricated from two absences:\n%s", out)
	}
	if exitCodeFor(err) != 5 {
		t.Errorf("err = %v, want exit 5: the revision could not be paired", err)
	}
}

// Mixed deployment: the before-read reaches a server with revisions and the
// after-read one without. That is not "the server predates revisions"; it is
// a change under the read, retried and then refused (@copilot on #724).
func TestNodeGetDoesNotDowngradeWhenTheAfterReadLosesRevisions(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisions":
			calls++
			if calls%2 == 1 {
				return liveRevisions(map[string]int{"n1": 12})
			}
			v, _ := unstubbedDefault("NodeLiveRevisions") // an older server's refusal
			return v
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	err := root.Execute()
	if strings.Contains(out.String(), `"revision": null`) || exitCodeFor(err) != 5 {
		t.Errorf("err %v; a server changing under the read must not be reported as an older server:\n%s", err, out.String())
	}
}

// The reverse routing: the before-read reaches an older instance, the after
// read a revision-aware one. Also a change under the read, not "predates".
func TestNodeGetDoesNotDowngradeWhenOnlyTheBeforeReadLacksRevisions(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisions":
			calls++
			if calls%2 == 1 {
				v, _ := unstubbedDefault("NodeLiveRevisions") // an older instance
				return v
			}
			return liveRevisions(map[string]int{"n1": 12})
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	err := root.Execute()
	if strings.Contains(out.String(), `"revision": null`) || exitCodeFor(err) != 5 {
		t.Errorf("err %v; support gained during the read must not print null:\n%s", err, out.String())
	}
}

// An older server and a content read that returns NO nodes: the after-probe
// makes no request, so it must not be read as "revision support appeared"
// (@copilot, @codex on #724). The ordinary empty result comes back.
func TestNodeGetEmptyResultOnAnOlderServerIsNotARace(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult(nil, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--prefix", "nothing:", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an empty prefix on an older server must succeed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"nodes": []`) {
		t.Errorf("want the ordinary empty result:\n%s", out.String())
	}
}

func TestNodeGetAllUnavailableOnAnOlderServerIsNotARace(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult(nil, `"hrn:node:acme.com:kb:a","hrn:node:acme.com:kb:b"`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "a", "b", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) == 5 {
		t.Fatalf("every ref unavailable on an older server is not a race (exit 5): %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"unavailable"`) {
		t.Errorf("want the ordinary unavailable report:\n%s", out.String())
	}
}

// One probe answered by both versions (a capped multi-call probe split across
// instances) is a deployment change, never "predates revisions".
func TestNodeGetSplitProbeIsAChangeNotAnOlderServer(t *testing.T) {
	const n = 201
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = batchNodeJSON(fmt.Sprintf("n%d", i), fmt.Sprintf("findings:x%d", i))
	}
	refusal, _ := unstubbedDefault("NodeLiveRevisions")
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
			// The prefix probe and the first 200-id call reach a new
			// instance; the 1-id remainder reaches an old one.
			if len(body.Variables.Refs) == 1 {
				_, _ = w.Write([]byte(refusal))
				return
			}
			pairs := map[string]int{}
			for i := 0; i < n; i++ {
				pairs[fmt.Sprintf("n%d", i)] = 1
			}
			_, _ = w.Write([]byte(liveRevisionsAt(batchUpdatedAt, pairs)))
		}
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--prefix", "findings:", "-m", "acme.com::kb", "--json", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != 5 || strings.Contains(out.String(), `"revision": null`) {
		t.Errorf("err %v; a split probe must not be reported as an older server:\n%s", err, out.String())
	}
}

// Both probes split the same way (explicit refs, so the before-probe is also
// two capped calls): mapping "mixed" to "no" would print null for a server
// that has revisions. It must retry and refuse instead (@codex on #724).
func TestNodeGetSplitInBothProbesIsNotAnOlderServer(t *testing.T) {
	const n = 201
	refs := make([]string, n)
	byRef := map[string]string{}
	for i := range refs {
		loc := fmt.Sprintf("findings:x%d", i)
		refs[i] = loc
		byRef["hrn:node:acme.com:kb:"+loc] = batchNodeJSON(fmt.Sprintf("n%d", i), loc)
	}
	refusal, _ := unstubbedDefault("NodeLiveRevisions")
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
			nodes := []string{}
			for _, ref := range body.Variables.Refs {
				if nd, ok := byRef[ref]; ok {
					nodes = append(nodes, nd)
				}
			}
			_, _ = w.Write([]byte(nodeBatchResult(nodes, "")))
		case "NodeLiveRevisions":
			if len(body.Variables.Refs) == 1 { // the remainder reaches an old instance
				_, _ = w.Write([]byte(refusal))
				return
			}
			pairs := map[string]int{}
			for i := 0; i < n; i++ {
				pairs[fmt.Sprintf("n%d", i)] = 1
			}
			_, _ = w.Write([]byte(liveRevisionsAt(batchUpdatedAt, pairs)))
		}
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "get"}, refs...), "-m", "acme.com::kb", "--json", "--server", srv.URL))
	err := root.Execute()
	if strings.Contains(out.String(), `"revision": null`) || exitCodeFor(err) != 5 {
		t.Errorf("err %v; a probe split in both brackets must not print null:\n%.400s", err, out.String())
	}
}

// A server older than nodeBatch itself: the probe is refused for nodeBatch,
// not revision. Single-ref `node get` reads through GetNode and must keep
// working there, with the revision unknown (@copilot on #724).
func TestNodeGetOnAServerWithoutNodeBatchStillReads(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	stubs["NodeLiveRevisions"] = `{"errors":[{"message":"Cannot query field \"nodeBatch\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	out, _, err := runNodeGet(t, stubs, testNodeURN, "--json")
	if err != nil {
		t.Fatalf("a server without nodeBatch must still serve node get: %v", err)
	}
	if !strings.Contains(out, `"revision": null`) {
		t.Errorf("want the revision unknown:\n%s", out)
	}
}
