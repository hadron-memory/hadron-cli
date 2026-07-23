package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// createNodeInput is the decoded CreateNode input the recursive importer sends.
type createNodeInput struct {
	Loc     string  `json:"loc"`
	Name    string  `json:"name"`
	Content *string `json:"content"`
	Edges   []struct {
		Name     *string `json:"name"`
		TargetId string  `json:"targetId"`
	} `json:"edges"`
}

// recordingCreateServer records every CreateNode input (in call order) and
// replies with an id derived from the loc, so a parent's `contains` edge can be
// checked against its child's returned id. Any other op fails the test unless
// listed in extra.
func recordingCreateServer(t *testing.T, extra map[string]string) (*httptest.Server, *[]createNodeInput) {
	t.Helper()
	var mu sync.Mutex
	creates := &[]createNodeInput{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "CreateNode" {
			var v struct {
				Input createNodeInput `json:"input"`
			}
			_ = json.Unmarshal(body.Variables, &v)
			mu.Lock()
			*creates = append(*creates, v.Input)
			mu.Unlock()
			fmt.Fprintf(w, `{"data":{"createNode":{"id":"nid:%s","memoryId":"mem1","loc":%q,"name":%q,"nodeType":null,"tags":[],"seq":null,"isRunnable":null,"updatedAt":"2026-01-01T00:00:00Z"}}}`,
				v.Input.Loc, v.Input.Loc, v.Input.Name)
			return
		}
		if resp, ok := extra[body.OperationName]; ok {
			_, _ = w.Write([]byte(resp))
			return
		}
		t.Errorf("unexpected operation %q", body.OperationName)
		_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected op"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, creates
}

func writeTree(t *testing.T, base string) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "root landing")
	write("how-to/setup.md", "the setup")
	write("how-to/setup.txt", "also setup") // collides → setup-2
	if err := os.WriteFile(filepath.Join(base, "logo.png"), []byte{0x89, 0x00, 0x01}, 0o600); err != nil {
		t.Fatal(err) // binary → skipped
	}
	write(".secret", "nope") // hidden → skipped
}

// The recursive importer creates nodes bottom-up (leaves before their branch),
// folds README into the branch, wires parent→child `contains` edges to the
// children's returned ids, and skips binaries/dotfiles.
func TestNodeImportTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)

	gql, creates := recordingCreateServer(t, nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Post-order: both leaves, then how-to, then docs. Binary + dotfile skipped.
	gotLocs := make([]string, len(*creates))
	for i, c := range *creates {
		gotLocs[i] = c.Loc
	}
	wantLocs := []string{"docs:how-to:setup", "docs:how-to:setup-2", "docs:how-to", "docs"}
	if strings.Join(gotLocs, ",") != strings.Join(wantLocs, ",") {
		t.Fatalf("create order = %v, want %v", gotLocs, wantLocs)
	}

	byLoc := map[string]createNodeInput{}
	for _, c := range *creates {
		byLoc[c.Loc] = c
	}
	// README folded into the branch, not a separate child.
	if c := byLoc["docs"]; c.Content == nil || *c.Content != "root landing" {
		t.Errorf("docs branch should carry the folded README content, got %+v", c.Content)
	}
	// docs → how-to contains edge, targeting how-to's returned id.
	docs := byLoc["docs"]
	if len(docs.Edges) != 1 || docs.Edges[0].TargetId != "nid:docs:how-to" || *docs.Edges[0].Name != "contains" {
		t.Errorf("docs should have one contains edge to how-to's id, got %+v", docs.Edges)
	}
	// how-to → both leaves.
	howto := byLoc["docs:how-to"]
	if len(howto.Edges) != 2 {
		t.Fatalf("how-to should wire 2 contains edges, got %+v", howto.Edges)
	}
	targets := map[string]bool{howto.Edges[0].TargetId: true, howto.Edges[1].TargetId: true}
	if !targets["nid:docs:how-to:setup"] || !targets["nid:docs:how-to:setup-2"] {
		t.Errorf("how-to edges should target the two leaf ids, got %+v", howto.Edges)
	}
	// Leaves are edge-free.
	if len(byLoc["docs:how-to:setup"].Edges) != 0 {
		t.Errorf("a leaf must not carry edges, got %+v", byLoc["docs:how-to:setup"].Edges)
	}

	if !strings.Contains(out.String(), "4 node(s), 3 edge(s)") {
		t.Errorf("summary should report 4 nodes / 3 edges, got %s", out.String())
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("summary should note skipped files, got %s", out.String())
	}
}

// --dry-run plans the tree and prints it without a single mutation.
func TestNodeImportTreeDryRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)

	gql, creates := recordingCreateServer(t, nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*creates) != 0 {
		t.Fatalf("--dry-run must not create anything, got %d creates", len(*creates))
	}
	if !strings.Contains(out.String(), "[dry-run]") || !strings.Contains(out.String(), "docs:how-to:setup-2") {
		t.Errorf("dry-run should print the planned tree, got %s", out.String())
	}
}

// The --json summary is a stable shape: arrays render as [] (never null), and
// the created/edges/collisions/skipped facts are present.
func TestNodeImportTreeJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)

	gql, _ := recordingCreateServer(t, nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Mode         string           `json:"mode"`
		Root         string           `json:"root"`
		Created      []map[string]any `json:"created"`
		Existing     []string         `json:"existing"`
		Skipped      []map[string]any `json:"skipped"`
		Collisions   []map[string]any `json:"collisions"`
		EdgesWired   int              `json:"edgesWired"`
		NodesCreated int              `json:"nodesCreated"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if dto.Mode != "tree" || dto.Root != "docs" {
		t.Errorf("mode/root wrong: %+v", dto)
	}
	if dto.NodesCreated != 4 || dto.EdgesWired != 3 {
		t.Errorf("want 4 nodes / 3 edges, got %d / %d", dto.NodesCreated, dto.EdgesWired)
	}
	if len(dto.Collisions) != 1 || len(dto.Skipped) != 2 {
		t.Errorf("want 1 collision + 2 skips, got %+v %+v", dto.Collisions, dto.Skipped)
	}
	// Arrays must serialize even when empty (existing/unresolved have no entries
	// here) — [], never null (the output is indented, so match with the space).
	for _, key := range []string{`"existing": []`, `"unresolved": []`} {
		if !strings.Contains(out.String(), key) {
			t.Errorf("empty arrays must render as [] (%s), got %s", key, out.String())
		}
	}
}

// --on-conflict skip: an existing loc is left in place (resolved for its
// parent's edge) instead of aborting; the summary reports it under `existing`.
func TestNodeImportTreeOnConflictSkip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)

	// The first leaf (docs:how-to:setup) already exists; every other create
	// succeeds. ResolveUrn returns its id so the parent edge still wires.
	var mu sync.Mutex
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "CreateNode":
			var v struct {
				Input createNodeInput `json:"input"`
			}
			_ = json.Unmarshal(body.Variables, &v)
			if v.Input.Loc == "docs:how-to:setup" {
				_, _ = w.Write([]byte(`{"errors":[{"message":"exists","extensions":{"code":"NodeLocConflictError"}}]}`))
				return
			}
			mu.Lock()
			creates++
			mu.Unlock()
			fmt.Fprintf(w, `{"data":{"createNode":{"id":"nid:%s","memoryId":"mem1","loc":%q,"name":%q,"nodeType":null,"tags":[],"seq":null,"isRunnable":null,"updatedAt":"2026-01-01T00:00:00Z"}}}`,
				v.Input.Loc, v.Input.Loc, v.Input.Name)
		case "ResolveUrn":
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"existing-setup","kind":"node","memoryId":"mem1"}}}`))
		default:
			t.Errorf("unexpected op %q", body.OperationName)
		}
	}))
	t.Cleanup(srv.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--on-conflict", "skip", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Existing     []string `json:"existing"`
		NodesCreated int      `json:"nodesCreated"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(dto.Existing) != 1 || dto.Existing[0] != "docs:how-to:setup" {
		t.Errorf("the conflicting loc should be reported existing, got %+v", dto.Existing)
	}
	if dto.NodesCreated != 3 {
		t.Errorf("3 nodes should be created (one skipped), got %d", dto.NodesCreated)
	}
}

// Default (--on-conflict error): the first conflict aborts with an actionable
// message naming the loc and the skip flag.
func TestNodeImportTreeOnConflictError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		// Every create conflicts.
		_, _ = w.Write([]byte(`{"errors":[{"message":"exists","extensions":{"code":"NodeLocConflictError"}}]}`))
	}))
	t.Cleanup(srv.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--on-conflict skip") {
		t.Fatalf("expected an actionable conflict error, got %v", err)
	}
}

// An invalid --include/--exclude glob is a usage error, before any walk/write.
func TestNodeImportTreeRejectsBadGlob(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--include", "[", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid glob") {
		t.Fatalf("expected an invalid-glob usage error, got %v", err)
	}
}

// --on-conflict skip on a pre-existing BRANCH still wires `contains` edges from
// the existing branch to the children created this run, so they aren't orphaned.
func TestNodeImportTreeSkipBranchWiresChildEdges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	writeTree(t, dir)

	type edge struct{ from, to, name string }
	var mu sync.Mutex
	var edges []edge
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "CreateNode":
			var v struct {
				Input createNodeInput `json:"input"`
			}
			_ = json.Unmarshal(body.Variables, &v)
			if v.Input.Loc == "docs:how-to" { // the branch already exists
				_, _ = w.Write([]byte(`{"errors":[{"message":"exists","extensions":{"code":"NodeLocConflictError"}}]}`))
				return
			}
			fmt.Fprintf(w, `{"data":{"createNode":{"id":"nid:%s","memoryId":"mem1","loc":%q,"name":%q,"nodeType":null,"tags":[],"seq":null,"isRunnable":null,"updatedAt":"2026-01-01T00:00:00Z"}}}`,
				v.Input.Loc, v.Input.Loc, v.Input.Name)
		case "ResolveUrn":
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"existing-howto","kind":"node","memoryId":"mem1"}}}`))
		case "CreateEdge":
			var v struct {
				SourceRef string `json:"sourceRef"`
				TargetRef string `json:"targetRef"`
				Name      string `json:"name"`
			}
			_ = json.Unmarshal(body.Variables, &v)
			mu.Lock()
			edges = append(edges, edge{v.SourceRef, v.TargetRef, v.Name})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":{"createEdge":{"id":"e1"}}}`))
		default:
			t.Errorf("unexpected op %q", body.OperationName)
		}
	}))
	t.Cleanup(srv.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", dir, "-m", "acme.com::kb", "--on-conflict", "skip", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The pre-existing branch is wired to both just-created leaves.
	if len(edges) != 2 {
		t.Fatalf("expected 2 contains edges from the existing branch, got %+v", edges)
	}
	for _, e := range edges {
		if e.from != "existing-howto" || e.name != "contains" {
			t.Errorf("edge should originate from existing-howto as contains, got %+v", e)
		}
	}
	targets := map[string]bool{edges[0].to: true, edges[1].to: true}
	if !targets["nid:docs:how-to:setup"] || !targets["nid:docs:how-to:setup-2"] {
		t.Errorf("edges should target the two leaf ids, got %+v", edges)
	}
}

// -r with a file (not a directory) is a usage error.
func TestNodeImportTreeRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-r", file, "-m", "acme.com::kb", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected a not-a-directory usage error, got %v", err)
	}
}
