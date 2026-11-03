package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

var revisionProjections1199 = []string{"NodeLiveRevisionsMinted", "NodeLiveRevisionsApproved", "NodeLiveRevisionsStamped", "NodeLiveRevisions"}

type revisionRequest1199 struct {
	operation string
	refs      []string
}

// Serve only the requested nodes. A selected revision projection truncates at
// 17 nodes; preceding projections reject their optional field, like old servers.
// Record copies of refs to pin both the before-URN and after-ID probes.
func revisionChunkServer1199(t *testing.T, projection string, stuck bool) (*httptest.Server, func() ([]revisionRequest1199, int)) {
	t.Helper()
	var mu sync.Mutex
	var requests []revisionRequest1199
	contentCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Refs []string `json:"refs"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request: %v", err)
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		op, refs := body.OperationName, body.Variables.Refs
		if strings.HasPrefix(op, "NodeLiveRevisions") {
			mu.Lock()
			requests = append(requests, revisionRequest1199{op, append([]string(nil), refs...)})
			mu.Unlock()
			if len(refs) == 0 || len(refs) > api.NodeBatchCap {
				t.Errorf("%s requested %d refs", op, len(refs))
				http.Error(w, "bad batch", 400)
				return
			}
			if op != projection {
				for i, candidate := range revisionProjections1199 {
					if candidate == op {
						target := -1
						for j, p := range revisionProjections1199 {
							if p == projection {
								target = j
							}
						}
						if i < target {
							_, _ = w.Write([]byte(missingFieldJSON([]string{"mintStatus", "approvalStatus", "authorship"}[i])))
							return
						}
					}
				}
				t.Errorf("unexpected projection %s after selecting %s", op, projection)
				http.Error(w, "unexpected projection", 400)
				return
			}
			served := min(17, len(refs))
			if stuck {
				served = 0
			}
			nodes := make([]map[string]any, 0, served)
			for _, ref := range refs[:served] {
				n, ok := revisionIndex1199(t, ref)
				if !ok {
					http.Error(w, "bad ref", 400)
					return
				}
				nodes = append(nodes, map[string]any{"id": fmt.Sprintf("n%d", n), "revision": n + 1})
			}
			omitted := append([]string{}, refs[served:]...)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"nodeBatch": map[string]any{"nodes": nodes, "omitted": omitted, "unavailable": []string{}, "truncated": len(omitted) > 0}}})
			return
		}
		if op == "NodeBatch" {
			mu.Lock()
			contentCalls++
			mu.Unlock()
			if len(refs) == 0 || len(refs) > api.NodeBatchCap {
				t.Errorf("content requested %d refs", len(refs))
				http.Error(w, "bad batch", 400)
				return
			}
			nodes := make([]string, 0, len(refs))
			for _, ref := range refs {
				n, ok := revisionIndex1199(t, ref)
				if !ok {
					http.Error(w, "bad ref", 400)
					return
				}
				nodes = append(nodes, batchNodeJSON(fmt.Sprintf("n%d", n), fmt.Sprintf("dense:%d", n)))
			}
			_, _ = w.Write([]byte(nodeBatchResult(nodes, "")))
			return
		}
		if response, ok := unstubbedDefault(op); ok {
			_, _ = w.Write([]byte(response))
			return
		}
		t.Errorf("unexpected operation %s", op)
		http.Error(w, "unexpected operation", 400)
	}))
	t.Cleanup(srv.Close)
	return srv, func() ([]revisionRequest1199, int) {
		mu.Lock()
		defer mu.Unlock()
		return append([]revisionRequest1199(nil), requests...), contentCalls
	}
}

func revisionIndex1199(t *testing.T, ref string) (int, bool) {
	t.Helper()
	value := strings.TrimPrefix(ref, "n")
	if strings.HasPrefix(ref, "hrn:node:acme.com:kb:dense:") {
		value = strings.TrimPrefix(ref, "hrn:node:acme.com:kb:dense:")
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		t.Errorf("invalid ref %q", ref)
		return 0, false
	}
	return n, true
}

func runRevisionChunks1199(t *testing.T, srv *httptest.Server, total int) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"node", "get"}
	for i := 0; i < total; i++ {
		args = append(args, fmt.Sprintf("dense:%d", i))
	}
	root.SetArgs(append(args, "-m", "acme.com::kb", "--json", "--server", srv.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := root.ExecuteContext(ctx)
	return out.String(), err
}

func TestNodeGetRevisionChunksAndProjections1199(t *testing.T) {
	const total = 2*api.NodeBatchCap + 23
	for _, projection := range revisionProjections1199 {
		t.Run(projection, func(t *testing.T) {
			srv, snapshot := revisionChunkServer1199(t, projection, false)
			out, err := runRevisionChunks1199(t, srv, total)
			if err != nil {
				t.Fatal(err)
			}
			// Identity, uniqueness and exact revisions: a duplicated early chunk cannot
			// masquerade as the expected number of nodes while omitting later nodes.
			var dto struct {
				Nodes []struct {
					ID       string `json:"id"`
					Revision *int   `json:"revision"`
				} `json:"nodes"`
			}
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto.Nodes) != total {
				t.Fatalf("nodes=%d want%d", len(dto.Nodes), total)
			}
			seen := map[string]bool{}
			for _, node := range dto.Nodes {
				n, ok := revisionIndex1199(t, node.ID)
				if !ok {
					continue
				}
				if n >= total || seen[node.ID] {
					t.Errorf("unexpected/duplicate id %s", node.ID)
				}
				seen[node.ID] = true
				if node.Revision == nil || *node.Revision != n+1 {
					t.Errorf("%s revision=%v want%d", node.ID, node.Revision, n+1)
				}
			}
			for i := 0; i < total; i++ {
				if !seen[fmt.Sprintf("n%d", i)] {
					t.Errorf("missing n%d", i)
				}
			}
			requests, contentCalls := snapshot()
			if contentCalls != 3 {
				t.Errorf("content calls=%d want3", contentCalls)
			}
			// Each earlier projection is refused only once, then support is cached.
			first := make([]string, api.NodeBatchCap)
			for i := range first {
				first[i] = fmt.Sprintf("hrn:node:acme.com:kb:dense:%d", i)
			}
			var want []revisionRequest1199
			for _, p := range revisionProjections1199 {
				if p == projection {
					break
				}
				want = append(want, revisionRequest1199{p, first})
			}
			for _, prefix := range []string{"hrn:node:acme.com:kb:dense:", "n"} {
				for start := 0; start < total; start += api.NodeBatchCap {
					end := min(start+api.NodeBatchCap, total)
					for next := start; next < end; next += 17 {
						refs := make([]string, end-next)
						for i := range refs {
							refs[i] = fmt.Sprintf("%s%d", prefix, next+i)
						}
						want = append(want, revisionRequest1199{projection, refs})
					}
				}
			}
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("revision requests differ: got %v\nwant %v", requests, want)
			}
		})
	}
}

func TestNodeGetRevisionNoProgressAllProjections1199(t *testing.T) {
	for _, projection := range revisionProjections1199 {
		t.Run(projection, func(t *testing.T) {
			srv, snapshot := revisionChunkServer1199(t, projection, true)
			out, err := runRevisionChunks1199(t, srv, api.NodeBatchCap+5)
			if err == nil || exitCodeFor(err) != 1 || !strings.Contains(err.Error(), "no progress") {
				t.Fatalf("err=%v exit=%d want no-progress exit1", err, exitCodeFor(err))
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("partial success output: %s", out)
			}
			requests, contentCalls := snapshot()
			if contentCalls != 0 {
				t.Errorf("content calls=%d want0", contentCalls)
			}
			selectedCalls := 0
			for _, request := range requests {
				if request.operation == projection {
					selectedCalls++
				}
			}
			if selectedCalls != 1 {
				t.Errorf("selected revision calls=%d want1", selectedCalls)
			}
		})
	}
}
