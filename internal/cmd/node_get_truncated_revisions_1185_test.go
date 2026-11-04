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

// #1185: the server caps a nodeBatch response by content bytes whatever the
// query selects, so a revision-only read of several large nodes comes back
// `truncated` with the rest in `omitted`. `node get` must follow `omitted`
// until none remain; a truncated read is never a change of the node.

// truncatingRevisionServer holds nodes n0..n<total-1> (loc dense:<i>, revision
// i+1, never written). NodeLiveRevisions answers at most capPerCall nodes per
// call, the rest in omitted. stuck makes it answer no node at all while still
// listing every requested ref as omitted (no progress).
func truncatingRevisionServer(t *testing.T, total, capPerCall int, stuck bool) (*httptest.Server, func() int) {
	t.Helper()
	all := make([]string, total)
	content := make([]string, total)
	for i := range all {
		all[i] = fmt.Sprintf("n%d", i)
		content[i] = batchNodeJSON(all[i], fmt.Sprintf("dense:%d", i))
	}
	var mu sync.Mutex
	revCalls := 0
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
			_, _ = w.Write([]byte(nodeBatchResult(content, "")))
		case "NodeLiveRevisions":
			mu.Lock()
			revCalls++
			mu.Unlock()
			asked := body.Variables.Refs
			if len(asked) == 0 { // the prefix-mode read
				asked = all
			}
			served, omitted := asked, []string{}
			if stuck {
				served, omitted = nil, asked
			} else if len(asked) > capPerCall {
				served, omitted = asked[:capPerCall], asked[capPerCall:]
			}
			nodes := []string{}
			for _, id := range served {
				// A ref is the id or the node's URN (the before-probe asks by
				// what the user typed); the answer always carries the id.
				var k int
				if i := strings.LastIndex(id, "dense:"); i >= 0 {
					_, _ = fmt.Sscanf(id[i:], "dense:%d", &k)
				} else {
					_, _ = fmt.Sscanf(id, "n%d", &k)
				}
				nodes = append(nodes, fmt.Sprintf(`{"id":"n%d","revision":%d}`, k, k+1))
			}
			quoted := []string{}
			for _, id := range omitted {
				quoted = append(quoted, fmt.Sprintf("%q", id))
			}
			_, _ = fmt.Fprintf(w, `{"data":{"nodeBatch":{"truncated":%t,"omitted":[%s],"unavailable":[],"nodes":[%s]}}}`,
				len(omitted) > 0, strings.Join(quoted, ","), strings.Join(nodes, ","))
		default:
			if resp, ok := unstubbedDefault(body.OperationName); ok {
				_, _ = w.Write([]byte(resp))
				return
			}
			t.Errorf("unexpected operation %q", body.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return revCalls }
}

func runTruncatedGet(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "get"}, args...), "-m", "acme.com::kb", "--json", "--server", srv.URL))
	err := root.Execute()
	return out.String(), err
}

func assertAllRevisionsPaired(t *testing.T, out string, total int) {
	t.Helper()
	var dto struct {
		Nodes []struct {
			ID       string `json:"id"`
			Revision *int   `json:"revision"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(dto.Nodes) != total {
		t.Fatalf("%d nodes, want %d:\n%s", len(dto.Nodes), total, out)
	}
	for _, nd := range dto.Nodes {
		var k int
		_, _ = fmt.Sscanf(nd.ID, "n%d", &k)
		if nd.Revision == nil || *nd.Revision != k+1 {
			t.Errorf("%s: revision %v, want %d", nd.ID, nd.Revision, k+1)
		}
	}
}

// Explicit refs: the chunk's truncated answer drops its omitted refs.
func TestNodeGetFollowsOmittedRevisionsForExplicitRefs1185(t *testing.T) {
	srv, _ := truncatingRevisionServer(t, 4, 2, false)
	out, err := runTruncatedGet(t, srv, "dense:0", "dense:1", "dense:2", "dense:3")
	if err != nil {
		t.Fatalf("a truncated revision read is not a conflict: %v", err)
	}
	assertAllRevisionsPaired(t, out, 4)
}

// Prefix: the spillover read is itself truncated (cap 1 of 3 omitted).
func TestNodeGetFollowsOmittedRevisionsForPrefixWhenSpilloverTruncates1185(t *testing.T) {
	srv, _ := truncatingRevisionServer(t, 4, 1, false)
	out, err := runTruncatedGet(t, srv, "--prefix", "dense:")
	if err != nil {
		t.Fatalf("a truncated revision read is not a conflict: %v", err)
	}
	assertAllRevisionsPaired(t, out, 4)
}

// A server that keeps answering the same omitted refs with no node ends in a
// clear error, not a loop and not the "changed while being read" conflict.
func TestNodeGetOmittedRevisionsWithoutProgressIsAClearError1185(t *testing.T) {
	srv, calls := truncatingRevisionServer(t, 4, 2, true)
	_, err := runTruncatedGet(t, srv, "dense:0", "dense:1", "dense:2", "dense:3")
	if err == nil {
		t.Fatal("a server that makes no progress must be an error")
	}
	if got := exitCodeFor(err); got == 0 || got == 5 {
		t.Errorf("exit = %d, want a failure that is not the change conflict (5)", got)
	}
	if strings.Contains(err.Error(), "changed while") {
		t.Errorf("no progress is not a node change: %v", err)
	}
	if !strings.Contains(err.Error(), "progress") {
		t.Errorf("the error must name the lack of progress: %v", err)
	}
	if n := calls(); n > 20 {
		t.Errorf("%d revision calls: the follow-through must stop, not loop", n)
	}
}
