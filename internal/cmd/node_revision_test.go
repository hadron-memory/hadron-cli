package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const nodeRevisionsJSON = `{"data":{"nodeRevisions":[
	{"id":"v2","nodeId":"n1","loc":"findings:flaky-ci","name":"Flaky CI",
	 "description":null,"tags":["ci"],"createdAt":"2026-06-11T00:00:00Z",
	 "editedBy":"u1","editedByInfo":null,"editedByUser":{"handle":"alice","urn":"hrn:user:acme.com::alice"},
	 "revLabel":"before refactor","changes":["content","tags"]},
	{"id":"v1","nodeId":"n1","loc":"findings:flaky-ci","name":"Flaky CI",
	 "description":null,"tags":[],"createdAt":"2026-06-10T00:00:00Z",
	 "editedBy":null,"editedByInfo":"github:octocat","editedByUser":null,"revLabel":null,"changes":[]}
]}}`

// A bare node id passes straight through as the server nodeRef (no resolveUrn),
// which is the only way to reach a soft-deleted node's history. The public
// handle renders as @handle; a snapshot whose editor didn't resolve to a user
// falls back to the raw editedByInfo identity string. The label is shown.
func TestNodeRevisionListBareIDPassthrough(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeRevisions": nodeRevisionsJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", "n1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"v2", "@alice", "github:octocat", "before refactor"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
		Limit   *int   `json:"limit"`
	}
	_ = json.Unmarshal(captured["NodeRevisions"], &vars)
	if vars.NodeRef != "n1" {
		t.Errorf("bare id should pass through as nodeRef, got %q", vars.NodeRef)
	}
	if vars.Limit != nil {
		t.Errorf("unset --limit must be omitted, got %v", *vars.Limit)
	}
}

// A fully-qualified URN is resolved client-side to the node PK before the
// nodeRef call, consistent with the other node commands. --limit is forwarded.
func TestNodeRevisionListURNResolvesAndLimit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":    resolveNodeJSON,
		"NodeRevisions": nodeRevisionsJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", nodeURN, "--limit", "5", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
		Limit   *int   `json:"limit"`
	}
	_ = json.Unmarshal(captured["NodeRevisions"], &vars)
	if vars.NodeRef != "n1" {
		t.Errorf("URN should resolve to the node PK, got %q", vars.NodeRef)
	}
	if vars.Limit == nil || *vars.Limit != 5 {
		t.Errorf("--limit should forward as 5, got %v", vars.Limit)
	}
}

// A negative --limit is a usage error (matching `hadron search`), rejected
// before any server call.
func TestNodeRevisionListRejectsNegativeLimit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeRevisions": nodeRevisionsJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", "n1", "--limit", "-1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for a negative --limit")
	}
	if _, called := captured["NodeRevisions"]; called {
		t.Error("a negative --limit must not reach the server")
	}
}

// A namespaced bare loc without -m is the ambiguous form: reject it as a usage
// error rather than passing it to the server as a bogus id that misses.
func TestNodeRevisionListRejectsBareLoc(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeRevisions": nodeRevisionsJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", "findings:flaky-ci", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for a bare namespaced loc without -m")
	}
	if _, called := captured["NodeRevisions"]; called {
		t.Error("a rejected bare loc must not reach the server")
	}
}

func TestNodeRevisionGet(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeRevision": `{"data":{"nodeRevision":{"id":"v2","nodeId":"n1","loc":"findings:flaky-ci",
			"name":"Flaky CI","description":"why it flakes","content":"snapshot body here","tags":["ci"],
			"createdAt":"2026-06-11T00:00:00Z","editedBy":"u1","editedByInfo":null,
			"editedByUser":{"handle":"alice","urn":"hrn:user:x"},"revLabel":"tagged","changes":["content"]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "get", "v2", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"snapshot body here", "@alice", "v2", "tagged"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// A null snapshot (unreadable / soft-deleted) is a NotFound, never a crash.
func TestNodeRevisionGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeRevision": `{"data":{"nodeRevision":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "get", "missing", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error for a null snapshot")
	}
}

const restoreNodeJSON = `{"data":{"restoreNodeRevision":{"id":"n1","memoryId":"mem1",
	"loc":"findings:flaky-ci","name":"Flaky CI","nodeType":"finding","updatedAt":"2026-06-12T00:00:00Z"}}}`

// A plain restore is undoable, needs no --yes, and omits truncate so the server
// keeps its default (false).
func TestNodeRevisionRestoreDefaultOmitsTruncate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RestoreNodeRevision": restoreNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "restore", "v2", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Restored node findings:flaky-ci") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		RevisionId string `json:"revisionId"`
		Truncate   *bool  `json:"truncate"`
	}
	_ = json.Unmarshal(captured["RestoreNodeRevision"], &vars)
	if vars.RevisionId != "v2" {
		t.Errorf("revisionId should be v2, got %q", vars.RevisionId)
	}
	if vars.Truncate != nil {
		t.Errorf("truncate must be omitted by default, got %v", *vars.Truncate)
	}
}

// --truncate is destructive; --yes gates it non-interactively and sends
// truncate:true.
func TestNodeRevisionRestoreTruncate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RestoreNodeRevision": restoreNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "restore", "v2", "--truncate", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "newer revisions deleted") {
		t.Errorf("truncate output should note deletion: %s", out.String())
	}
	var vars struct {
		Truncate *bool `json:"truncate"`
	}
	_ = json.Unmarshal(captured["RestoreNodeRevision"], &vars)
	if vars.Truncate == nil || !*vars.Truncate {
		t.Errorf("truncate should be true, got %v", vars.Truncate)
	}
}

// --truncate without --yes is refused non-interactively (no mutation sent).
func TestNodeRevisionRestoreTruncateRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RestoreNodeRevision": restoreNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "restore", "v2", "--truncate", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected refusal without --yes")
	}
	if _, called := captured["RestoreNodeRevision"]; called {
		t.Error("mutation must not be sent when confirmation is refused")
	}
}

func TestNodeRevisionLabel(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateNodeRevision": `{"data":{"updateNodeRevision":{"id":"v2","nodeId":"n1","loc":"findings:flaky-ci",
			"name":"Flaky CI","description":null,"tags":[],"createdAt":"2026-06-11T00:00:00Z",
			"editedBy":"u1","editedByInfo":null,"editedByUser":null,"revLabel":"pinned baseline","changes":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "label", "v2", "--label", "pinned baseline", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "pinned baseline") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		RevisionId string `json:"revisionId"`
		RevLabel   string `json:"revLabel"`
	}
	_ = json.Unmarshal(captured["UpdateNodeRevision"], &vars)
	if vars.RevisionId != "v2" || vars.RevLabel != "pinned baseline" {
		t.Errorf("unexpected label vars: %+v", vars)
	}
}

// --label is required for the label command.
func TestNodeRevisionLabelRequiresFlag(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateNodeRevision": `{"data":{"updateNodeRevision":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "label", "v2", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when --label is omitted")
	}
}

func TestNodeRevisionDelete(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteNodeRevision": `{"data":{"deleteNodeRevision":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "delete", "v2", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Deleted revision v2") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		RevisionId string `json:"revisionId"`
	}
	_ = json.Unmarshal(captured["DeleteNodeRevision"], &vars)
	if vars.RevisionId != "v2" {
		t.Errorf("revisionId should be v2, got %q", vars.RevisionId)
	}
}

// A false return (should be NOT_FOUND per contract) is surfaced as an error, so
// the human line never claims success while --json would say deleted:false.
func TestNodeRevisionDeleteFalseIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"DeleteNodeRevision": `{"data":{"deleteNodeRevision":false}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "delete", "gone", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error when the server returns deleted:false")
	}
}

func TestNodeRevisionDeleteRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteNodeRevision": `{"data":{"deleteNodeRevision":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "delete", "v2", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected refusal without --yes")
	}
	if _, called := captured["DeleteNodeRevision"]; called {
		t.Error("mutation must not be sent when confirmation is refused")
	}
}

// clear takes a bare node id (soft-deleted cleanup path) and prints the count.
func TestNodeRevisionClear(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ClearNodeHistory": `{"data":{"clearNodeHistory":3}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "clear", "n1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		NodeRef      string `json:"nodeRef"`
		DeletedCount int    `json:"deletedCount"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("--json is not valid: %v\n%s", err, out.String())
	}
	if got.DeletedCount != 3 {
		t.Errorf("deletedCount should be 3, got %d", got.DeletedCount)
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
	}
	_ = json.Unmarshal(captured["ClearNodeHistory"], &vars)
	if vars.NodeRef != "n1" {
		t.Errorf("bare id should pass through as nodeRef, got %q", vars.NodeRef)
	}
}
