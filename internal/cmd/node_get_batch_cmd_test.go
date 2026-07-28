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

// Several refs read together come back in ONE nodeBatch call — no GetNode per
// node, and (since hadron-server#813) no ResolveUrn per ref either.
func TestNodeGetBatch(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha"), batchNodeJSON("n2", "beta")}, ""),
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
	// The whole point of #813: nodeBatch(refs:) takes URNs, so N refs cost ONE
	// call, not N resolves plus a batch.
	if _, called := captured["ResolveUrn"]; called {
		t.Error("refs must go straight to nodeBatch — no per-ref resolve round trip")
	}
	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	want := []string{"hrn:node:acme.com:kb:alpha", "hrn:node:acme.com:kb:beta"}
	if len(vars.Refs) != 2 || vars.Refs[0] != want[0] || vars.Refs[1] != want[1] {
		t.Errorf("refs = %v, want the canonical node URNs %v", vars.Refs, want)
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
	// refs must be ABSENT, not null — the server rejects "both forms provided".
	if _, present := vars["refs"]; present {
		t.Errorf("refs must be omitted in prefix mode, got %v", vars["refs"])
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
	// alpha comes back; beta is refused — echoed by the server as the ref that
	// was sent (hadron-server#813), so no client-side id → ref mapping is left.
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, `"hrn:node:acme.com:kb:beta"`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "alpha", "beta", "-m", "acme.com::kb", "--json", "--server", gql.URL})
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
	// Named by the ref that was SENT — actionable, and re-runnable as-is. The
	// server used to answer with a primary key the caller never supplied and
	// could not act on, which the CLI had to map back (#303 review); #813
	// made it echo the ref instead.
	if dto.Unavailable[0] != "hrn:node:acme.com:kb:beta" {
		t.Errorf("unavailable = %v, want the ref as sent", dto.Unavailable)
	}
}

// An error that is NOT a bad ref — expired credentials, a dead transport, a
// cancelled context — must propagate, not be laundered into `unavailable`.
// Otherwise the command emits a plausible partial result and exits 4, hiding
// an auth or operational failure behind a not-found (#303 review).
func TestNodeGetBatchPropagatesNonRefErrors(t *testing.T) {
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

// A malformed ref fails the WHOLE call, matching the server's rule for
// nodeBatch(refs:). Filing it under `unavailable` would render a typo as
// "not found, or not readable by you", hiding a caller mistake among
// denials — the conflation that contract exists to prevent.
func TestNodeGetBatchMalformedRefIsLoud(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// The second ref is a bare loc with no -m: unqualified, so shape-wrong.
	root.SetArgs([]string{"node", "get", "acme.com::kb::alpha", "bare-loc", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a malformed ref must fail the call, not be reported as unavailable")
	}
	// Exit 2 (caller mistake), NOT 4 (absent/denied) — that distinction is the
	// whole point.
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (usage) — a typo must not read as not-found", got, exitcode.Usage)
	}
	if !strings.Contains(err.Error(), "bare-loc") {
		t.Errorf("the error must name the offending ref, got %v", err)
	}
	// Nothing is read: a caller mistake is caught before any node is fetched,
	// so there is no half-answer to misread.
	if _, called := captured["NodeBatch"]; called {
		t.Error("a malformed ref must be rejected before the batch read")
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
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "alpha", "alpha", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	if len(vars.Refs) != 1 {
		t.Errorf("refs = %v, want the repeated ref collapsed to one", vars.Refs)
	}
}
