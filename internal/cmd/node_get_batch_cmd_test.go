package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// batchNodeJSON is one node in the nodeBatch projection.
func batchNodeJSON(id, loc string) string {
	return `{"id":"` + id + `","memoryId":"mem1","loc":"` + loc + `","name":"` + loc + `",
		"alias":null,"nodeType":"info","objectType":null,"isRunnable":false,"description":null,
		"abstract":null,"abstractOriginHash":null,"tags":[],"seq":null,"data":null,"properties":null,
		"content":"body of ` + loc + `","createdAt":"2026-07-27T00:00:00Z","updatedAt":"2026-07-27T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
}

func nodeBatchResult(nodes []string, unavailable string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[` + unavailable + `],
		"nodes":[` + strings.Join(nodes, ",") + `]}}}`
}

// resolvingGraphQL answers ResolveUrn per-ref (so distinct refs get distinct
// ids, which the shared operation-keyed fake cannot do) and NodeBatch with a
// fixed body. Needed to exercise the id → caller-ref mapping.
func resolvingGraphQL(t *testing.T, idByURNSuffix map[string]string, batch string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Urn string `json:"urn"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "ResolveUrn":
			for suffix, id := range idByURNSuffix {
				if strings.HasSuffix(body.Variables.Urn, suffix) {
					_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"` + id + `","kind":"node","memoryId":"mem1"}}}`))
					return
				}
			}
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":null}}`))
		case "NodeBatch":
			_, _ = w.Write([]byte(batch))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Several refs read together: each is resolved, then the bodies come back in
// ONE nodeBatch call rather than a GetNode per node.
func TestNodeGetBatch(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeBatch":  nodeBatchResult([]string{batchNodeJSON("n1", "alpha"), batchNodeJSON("n2", "beta")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["NodeBatch"]; !called {
		t.Fatal("multiple refs must go through NodeBatch, not per-node reads")
	}
	if _, called := captured["GetNode"]; called {
		t.Error("the single-node read must not be used for a multi-ref get")
	}

	var dto struct {
		Nodes []struct {
			ID, Loc, Content string
		} `json:"nodes"`
		Unavailable []string `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if len(dto.Nodes) != 2 || dto.Nodes[0].Loc != "alpha" || dto.Nodes[1].Loc != "beta" {
		t.Errorf("nodes = %+v", dto.Nodes)
	}
	// The batch projection must carry full content, not just metadata —
	// that is what distinguishes it from `node list`.
	if dto.Nodes[0].Content != "body of alpha" {
		t.Errorf("batch must return content, got %q", dto.Nodes[0].Content)
	}
	if dto.Unavailable == nil {
		t.Error("unavailable must render as [] not null")
	}
}

// A single ref keeps the bare node object: that shape is a stable contract and
// must not change just because a batch form now exists.
func TestNodeGetSingleRefShapeUnchanged(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "acme.com::kb::alpha", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["NodeBatch"]; called {
		t.Error("a single ref must not take the batch path")
	}
	// Top-level object with an id — not an envelope.
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if _, isEnvelope := dto["nodes"]; isEnvelope {
		t.Errorf("single-ref output must be the node object, got an envelope: %s", out.String())
	}
	if dto["id"] == nil {
		t.Errorf("single-ref output should be the node itself: %s", out.String())
	}
}

// --prefix is the real batch: ONE call, no per-node resolution.
func TestNodeGetPrefixIsOneCall(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "findings:a"), batchNodeJSON("n2", "findings:b")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--prefix", "findings:", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// No ResolveUrn at all — that is the point of the prefix form.
	if _, called := captured["ResolveUrn"]; called {
		t.Error("--prefix must not resolve refs one by one")
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["NodeBatch"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	if vars["locPrefix"] != "findings:" {
		t.Errorf("locPrefix = %v", vars["locPrefix"])
	}
	if vars["memory"] != "hrn:mem:acme.com:kb" {
		t.Errorf("memory = %v, want the canonical memory ref", vars["memory"])
	}
	// ids must be ABSENT, not null — the server rejects "both forms provided".
	if _, present := vars["ids"]; present {
		t.Errorf("ids must be omitted in prefix mode, got %v", vars["ids"])
	}
	var dto struct {
		Nodes []struct{ Loc string } `json:"nodes"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if len(dto.Nodes) != 2 {
		t.Errorf("nodes = %+v", dto.Nodes)
	}
}

// The visibility gap: a node can be listed yet be unreadable, and the server
// reports denied and missing identically. A fan-out that returned fewer nodes
// than asked for without saying so would hide that — so unavailable is
// reported AND the exit code is non-zero.
func TestNodeGetBatchSurfacesUnavailable(t *testing.T) {
	// alpha → n1 (returned), beta → n2 (refused by the batch).
	srv := resolvingGraphQL(t, map[string]string{"alpha": "n1", "beta": "n2"},
		nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, `"n2"`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("an unavailable ref must not exit 0 — a partial read is not a complete one")
	}
	if got := exitcode.FromError(err); got != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (not found)", got, exitcode.NotFound)
	}
	var dto struct {
		Nodes       []struct{ Loc string } `json:"nodes"`
		Unavailable []string               `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if len(dto.Nodes) != 1 || len(dto.Unavailable) != 1 {
		t.Fatalf("dto = %+v", dto)
	}
	// Named as the CALLER typed it. The server answers with a primary key
	// ("n2"), which the caller never supplied and cannot act on (#303 review).
	if dto.Unavailable[0] != "beta" {
		t.Errorf("unavailable = %v, want the caller's ref \"beta\", not the server id", dto.Unavailable)
	}
}

// An error that is NOT absence — expired credentials, a dead transport, a
// cancelled context — must propagate, not be laundered into `unavailable`.
// Otherwise the command emits a plausible partial result and exits 4, hiding
// an auth or operational failure behind a not-found (#303 review).
func TestNodeGetBatchPropagatesNonNotFoundResolveErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The shape api.MapError classifies as an auth failure.
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Unauthorized"}]}`))
	}))
	t.Cleanup(srv.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("an auth failure must not be reported as a partial read")
	}
	if got := exitcode.FromError(err); got == exitcode.NotFound {
		t.Errorf("exit code = %d — an auth failure must not be laundered into not-found", got)
	}
}

// A ref that cannot even be resolved is reported, not fatal — one bad ref in
// twenty must not cost the other nineteen.
func TestNodeGetBatchUnresolvableRefIsReportedNotFatal(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeBatch":  nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// The second ref is a bare loc with no -m, which cannot be resolved.
	root.SetArgs([]string{"node", "get", "acme.com::kb::alpha", "bare-loc", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a non-zero exit for the unresolvable ref")
	}
	var dto struct {
		Nodes       []struct{ Loc string } `json:"nodes"`
		Unavailable []string               `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if len(dto.Nodes) != 1 {
		t.Errorf("the resolvable ref must still be returned, got %+v", dto.Nodes)
	}
	if len(dto.Unavailable) != 1 || dto.Unavailable[0] != "bare-loc" {
		t.Errorf("the unresolvable ref must be reported by the name the caller typed, got %v", dto.Unavailable)
	}
}

func TestNodeGetBatchRejectsBadCombinations(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"prefix with explicit refs", []string{"node", "get", "--prefix", "x:", "-m", "acme.com::kb", "alpha"}},
		{"prefix without memory", []string{"node", "get", "--prefix", "x:"}},
		{"no refs and no prefix", []string{"node", "get"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tt.args, "--server", "http://127.0.0.1:1"))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a usage error")
			}
			if got := exitcode.FromError(err); got != exitcode.Usage {
				t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
			}
		})
	}
}

// A repeated ref must not yield the node twice — the single read can't
// duplicate, so neither should the batch.
func TestNodeGetBatchDedupesRefs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeBatch":  nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "alpha", "alpha", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	if len(vars.IDs) != 1 {
		t.Errorf("ids = %v, want the repeated ref collapsed to one", vars.IDs)
	}
}
