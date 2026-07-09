package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// movedNodeJSON / clonedNodeJSON are the mutation payloads the server returns
// for the relocated / new node.
const movedNodeJSON = `{"data":{"moveNode":{"id":"n1","urn":"hrn:node:acme.com::archive::archive:flaky-ci",
	"memoryId":"mem2","loc":"archive:flaky-ci",
	"name":"Flaky CI","nodeType":"task","tags":[],"isRunnable":false,"updatedAt":"2026-07-08T00:00:00Z"}}}`
const clonedNodeJSON = `{"data":{"cloneNode":{"id":"n9","urn":"hrn:node:acme.com::kb::findings:new",
	"memoryId":"mem1","loc":"findings:new",
	"name":"Flaky CI","nodeType":"task","tags":[],"isRunnable":false,"updatedAt":"2026-07-08T00:00:00Z"}}}`

func TestNodeMoveToURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MoveNode":   movedNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", nodeURN, "--to-urn", "acme.com::archive::archive:flaky-ci", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["MoveNode"], &vars)
	// The source ref is resolved to a node id before the mutation.
	if vars["sourceRef"] != "n1" {
		t.Errorf("sourceRef = %v, want n1", vars["sourceRef"])
	}
	// A bare --to-urn is normalized to the canonical hrn:node: form.
	if vars["targetUrn"] != "hrn:node:acme.com::archive::archive:flaky-ci" {
		t.Errorf("targetUrn = %v", vars["targetUrn"])
	}
	// targetMemoryRef is omitted (not sent as null) so the server sees exactly one.
	if _, ok := vars["targetMemoryRef"]; ok {
		t.Errorf("targetMemoryRef should be omitted, got %v", vars["targetMemoryRef"])
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["loc"] != "archive:flaky-ci" || dto["memoryId"] != "mem2" {
		t.Errorf("unexpected dto: %v", dto)
	}
	if dto["urn"] != "hrn:node:acme.com::archive::archive:flaky-ci" {
		t.Errorf("--json should carry the destination urn, got %v", dto["urn"])
	}
}

// The human (non-JSON) confirmation identifies the node by its destination URN,
// not by loc + display name (#197).
func TestNodeMoveHumanOutputShowsURN(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MoveNode":   movedNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", nodeURN, "--to-urn", "acme.com::archive::archive:flaky-ci", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "to: hrn:node:acme.com::archive::archive:flaky-ci") {
		t.Errorf("output should show the destination URN, got:\n%s", got)
	}
	if strings.Contains(got, "Flaky CI") {
		t.Errorf("output should not build the summary from the display name, got:\n%s", got)
	}
}

func TestNodeCloneHumanOutputShowsURN(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"CloneNode":  clonedNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "clone", nodeURN, "--to-urn", "acme.com::kb::findings:new", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "to: hrn:node:acme.com::kb::findings:new") {
		t.Errorf("output should show the new node's URN, got:\n%s", got)
	}
}

func TestNodeMoveToMemory(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MoveNode":   movedNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", nodeURN, "--to-memory", "acme.com::archive", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["MoveNode"], &vars)
	// A bare org::memory is canonicalized to the hrn:memory: form.
	if vars["targetMemoryRef"] != "hrn:memory:acme.com::archive" {
		t.Errorf("targetMemoryRef = %v", vars["targetMemoryRef"])
	}
	if _, ok := vars["targetUrn"]; ok {
		t.Errorf("targetUrn should be omitted, got %v", vars["targetUrn"])
	}
}

func TestNodeMoveRequiresExactlyOneDestination(t *testing.T) {
	// Neither destination flag.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", nodeURN})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error with no destination")
	}

	// Both destination flags.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "move", nodeURN, "--to-urn", "acme.com::kb::x", "--to-memory", "acme.com::archive"})
	if err := root2.Execute(); err == nil {
		t.Fatal("expected an error with both destinations")
	}

	// A whitespace-only destination is treated as unset, not smuggled through as
	// an empty ref — so this is still "no destination", a usage error.
	f3, _ := testFactory(t)
	root3 := NewRootCmd(f3)
	root3.SetArgs([]string{"node", "move", nodeURN, "--to-memory", "   "})
	if err := root3.Execute(); err == nil {
		t.Fatal("expected an error with a whitespace-only destination")
	}
}

func TestNodeCloneToURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"CloneNode":  clonedNodeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "clone", nodeURN, "--to-urn", "acme.com::kb::findings:new", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CloneNode"], &vars)
	if vars["sourceRef"] != "n1" {
		t.Errorf("sourceRef = %v, want n1", vars["sourceRef"])
	}
	if vars["targetUrn"] != "hrn:node:acme.com::kb::findings:new" {
		t.Errorf("targetUrn = %v", vars["targetUrn"])
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	// The clone is a NEW node — fresh id.
	if dto["id"] != "n9" || dto["loc"] != "findings:new" {
		t.Errorf("unexpected dto: %v", dto)
	}
}

// A bare loc source resolves against -m/--memory (the URN is joined and
// resolved before the mutation).
func TestNodeMoveBareLocWithMemory(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"MoveNode":   movedNodeJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", "findings:flaky-ci", "-m", "acme.com::kb", "--to-memory", "acme.com::archive", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resolveVars map[string]any
	_ = json.Unmarshal(captured["ResolveUrn"], &resolveVars)
	if resolveVars["urn"] != "hrn:node:acme.com::kb::findings:flaky-ci" {
		t.Errorf("ResolveUrn urn = %v", resolveVars["urn"])
	}
}
