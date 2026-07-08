package cmd

import (
	"encoding/json"
	"testing"
)

const mergedNodeJSON = `{"data":{"mergeNodes":{"id":"n1","memoryId":"mem1","loc":"findings:canonical",
	"name":"Canonical","nodeType":"task","tags":["a","b"],"isRunnable":false,"updatedAt":"2026-07-08T00:00:00Z"}}}`

// mergeInput pulls the MergeNodesInput object out of the captured $input var.
func mergeInput(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	return vars.Input
}

func TestNodeMergeAllFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MergeNodes": mergedNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "merge", nodeURN, "--into", "acme.com::kb::findings:canonical", "--yes", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := mergeInput(t, captured["MergeNodes"])
	// Source + target are resolved to node ids before the mutation.
	if in["source"] != "n1" || in["target"] != "n1" {
		t.Errorf("source/target = %v/%v", in["source"], in["target"])
	}
	// No --field → include omitted so the server folds every mergeable field.
	if _, ok := in["include"]; ok {
		t.Errorf("include should be omitted, got %v", in["include"])
	}
	// No --delete-source → deleteSource omitted (server default: keep source).
	if _, ok := in["deleteSource"]; ok {
		t.Errorf("deleteSource should be omitted, got %v", in["deleteSource"])
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["loc"] != "findings:canonical" {
		t.Errorf("unexpected dto: %v", dto)
	}
}

func TestNodeMergeSelectedFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MergeNodes": mergedNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// Case-insensitive, order-independent, de-duplicated → sorted set on the wire.
	root.SetArgs([]string{"node", "merge", nodeURN, "--into", "acme.com::kb::x",
		"--field", "edges", "--field", "CONTENT", "--field", "content", "--yes", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := mergeInput(t, captured["MergeNodes"])
	got, _ := json.Marshal(in["include"])
	if string(got) != `["CONTENT","EDGES"]` {
		t.Errorf("include = %s, want [\"CONTENT\",\"EDGES\"]", got)
	}
}

func TestNodeMergeDeleteSource(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MergeNodes": mergedNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "merge", nodeURN, "--into", "acme.com::kb::x", "--delete-source", "--yes", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := mergeInput(t, captured["MergeNodes"])
	if in["deleteSource"] != true {
		t.Errorf("deleteSource = %v, want true", in["deleteSource"])
	}
}

func TestNodeMergeRequiresInto(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "merge", nodeURN, "--yes"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when --into is missing")
	}
}

func TestNodeMergeInvalidField(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "merge", nodeURN, "--into", "acme.com::kb::x", "--field", "bogus", "--yes"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for an unknown --field")
	}
}

// Without --yes and no interactive terminal (the test IOStreams), the
// confirmation gate refuses — merge is a destructive/bulk-write command. The
// refs resolve first (fake server), so the gate is what stops the merge:
// MergeNodes must never be reached.
func TestNodeMergeRequiresYesNonInteractive(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MergeNodes": mergedNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "merge", nodeURN, "--into", "acme.com::kb::x", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a refusal without --yes in non-interactive mode")
	}
	if _, ok := captured["MergeNodes"]; ok {
		t.Error("MergeNodes should not be called when the confirmation gate refuses")
	}
}
