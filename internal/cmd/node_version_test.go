package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const nodeVersionsJSON = `{"data":{"nodeVersions":[
	{"id":"v2","nodeId":"n1","loc":"findings:flaky-ci","name":"Flaky CI",
	 "description":null,"tags":["ci"],"createdAt":"2026-06-11T00:00:00Z",
	 "editedBy":"u1","editedByUser":{"handle":"alice","urn":"hrn:user:acme.com::alice"}},
	{"id":"v1","nodeId":"n1","loc":"findings:flaky-ci","name":"Flaky CI",
	 "description":null,"tags":[],"createdAt":"2026-06-10T00:00:00Z",
	 "editedBy":"app-secureid","editedByUser":null}
]}}`

// A bare node id passes straight through as the server nodeRef (no resolveUrn),
// which is the only way to reach a soft-deleted node's history. The public
// handle renders as @handle; a snapshot whose editor didn't resolve to a user
// falls back to the raw editedBy id.
func TestNodeVersionListBareIDPassthrough(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeVersions": nodeVersionsJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "list", "n1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"v2", "@alice", "app-secureid"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
		Limit   *int   `json:"limit"`
	}
	_ = json.Unmarshal(captured["NodeVersions"], &vars)
	if vars.NodeRef != "n1" {
		t.Errorf("bare id should pass through as nodeRef, got %q", vars.NodeRef)
	}
	if vars.Limit != nil {
		t.Errorf("unset --limit must be omitted, got %v", *vars.Limit)
	}
}

// A fully-qualified URN is resolved client-side to the node PK before the
// nodeRef call, consistent with the other node commands. --limit is forwarded.
func TestNodeVersionListURNResolvesAndLimit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":   resolveNodeJSON,
		"NodeVersions": nodeVersionsJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "list", nodeURN, "--limit", "5", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
		Limit   *int   `json:"limit"`
	}
	_ = json.Unmarshal(captured["NodeVersions"], &vars)
	if vars.NodeRef != "n1" {
		t.Errorf("URN should resolve to the node PK, got %q", vars.NodeRef)
	}
	if vars.Limit == nil || *vars.Limit != 5 {
		t.Errorf("--limit should forward as 5, got %v", vars.Limit)
	}
}

// A namespaced bare loc without -m is the ambiguous form: reject it as a usage
// error (matching the other node commands) rather than passing it to the server
// as a bogus id that misses.
func TestNodeVersionListRejectsBareLoc(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeVersions": nodeVersionsJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "list", "findings:flaky-ci", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for a bare namespaced loc without -m")
	}
	if _, called := captured["NodeVersions"]; called {
		t.Error("a rejected bare loc must not reach the server")
	}
}

func TestNodeVersionShow(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeVersion": `{"data":{"nodeVersion":{"id":"v2","nodeId":"n1","loc":"findings:flaky-ci",
			"name":"Flaky CI","description":"why it flakes","content":"snapshot body here","tags":["ci"],
			"createdAt":"2026-06-11T00:00:00Z","editedBy":"u1","editedByUser":{"handle":"alice","urn":"hrn:user:x"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "get", "v2", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"snapshot body here", "@alice", "v2"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// A null snapshot (unreadable / soft-deleted) is a NotFound, never a crash.
func TestNodeVersionShowNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeVersion": `{"data":{"nodeVersion":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "get", "missing", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error for a null snapshot")
	}
}

const restoreNodeJSON = `{"data":{"restoreNodeVersion":{"id":"n1","memoryId":"mem1",
	"loc":"findings:flaky-ci","name":"Flaky CI","nodeType":"finding","updatedAt":"2026-06-12T00:00:00Z"}}}`

// A plain restore is undoable, needs no --yes, and omits truncate so the server
// keeps its default (false).
func TestNodeVersionRestoreDefaultOmitsTruncate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RestoreNodeVersion": restoreNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "restore", "v2", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Restored node findings:flaky-ci") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		VersionId string `json:"versionId"`
		Truncate  *bool  `json:"truncate"`
	}
	_ = json.Unmarshal(captured["RestoreNodeVersion"], &vars)
	if vars.VersionId != "v2" {
		t.Errorf("versionId should be v2, got %q", vars.VersionId)
	}
	if vars.Truncate != nil {
		t.Errorf("truncate must be omitted by default, got %v", *vars.Truncate)
	}
}

// --truncate is destructive; --yes gates it non-interactively and sends
// truncate:true.
func TestNodeVersionRestoreTruncate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RestoreNodeVersion": restoreNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "restore", "v2", "--truncate", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "newer snapshots deleted") {
		t.Errorf("truncate output should note deletion: %s", out.String())
	}
	var vars struct {
		Truncate *bool `json:"truncate"`
	}
	_ = json.Unmarshal(captured["RestoreNodeVersion"], &vars)
	if vars.Truncate == nil || !*vars.Truncate {
		t.Errorf("truncate should be true, got %v", vars.Truncate)
	}
}

// --truncate without --yes is refused non-interactively (no mutation sent).
func TestNodeVersionRestoreTruncateRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RestoreNodeVersion": restoreNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "restore", "v2", "--truncate", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected refusal without --yes")
	}
	if _, called := captured["RestoreNodeVersion"]; called {
		t.Error("mutation must not be sent when confirmation is refused")
	}
}

func TestNodeVersionRm(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteNodeVersion": `{"data":{"deleteNodeVersion":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "delete", "v2", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Deleted version v2") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		VersionId string `json:"versionId"`
	}
	_ = json.Unmarshal(captured["DeleteNodeVersion"], &vars)
	if vars.VersionId != "v2" {
		t.Errorf("versionId should be v2, got %q", vars.VersionId)
	}
}

// A false return (should be NOT_FOUND per contract) is surfaced as an error, so
// the human line never claims success while --json would say deleted:false.
func TestNodeVersionDeleteFalseIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"DeleteNodeVersion": `{"data":{"deleteNodeVersion":false}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "delete", "gone", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error when the server returns deleted:false")
	}
}

func TestNodeVersionRmRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteNodeVersion": `{"data":{"deleteNodeVersion":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "delete", "v2", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected refusal without --yes")
	}
	if _, called := captured["DeleteNodeVersion"]; called {
		t.Error("mutation must not be sent when confirmation is refused")
	}
}

// clear takes a bare node id (soft-deleted cleanup path) and prints the count.
func TestNodeVersionClear(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ClearNodeHistory": `{"data":{"clearNodeHistory":3}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "version", "clear", "n1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Deleted 3 snapshot(s)") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		NodeRef string `json:"nodeRef"`
	}
	_ = json.Unmarshal(captured["ClearNodeHistory"], &vars)
	if vars.NodeRef != "n1" {
		t.Errorf("bare id should pass through as nodeRef, got %q", vars.NodeRef)
	}
}
