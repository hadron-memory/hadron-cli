package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #268 — the write/authoring half of structured-storage CLI parity: memory set
// --schema, node create/update --object-type + --properties (REPLACE). The
// read/query half is #265 (--where / --object-type / --sort-property).

// node create --object-type + --properties land on the CreateNode input as the
// collection discriminator and the typed properties bag (distinct from data).
func TestNodeAddObjectTypeAndProperties(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "competitors:acme",
		"--name", "Acme", "--object-type", "competitor",
		"--properties", `{"tier":"enterprise","seats":50}`,
		"--data", `{"scratch":true}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateNode"], &vars); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if vars.Input["objectType"] != "competitor" {
		t.Errorf("objectType not sent: %v", vars.Input)
	}
	props, ok := vars.Input["properties"].(map[string]any)
	if !ok || props["tier"] != "enterprise" || props["seats"] != float64(50) {
		t.Errorf("properties not sent as an object: %v", vars.Input["properties"])
	}
	// properties and data are distinct columns — data must not absorb properties.
	data, ok := vars.Input["data"].(map[string]any)
	if !ok || data["scratch"] != true {
		t.Errorf("data should stay separate from properties: %v", vars.Input["data"])
	}
}

// node update --object-type + --properties REPLACE on the UpdateNode input;
// unset fields stay omitted (preserve).
func TestNodeUpdateObjectTypeAndProperties(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--object-type", "competitor", "--properties", `{"tier":"pro"}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	if vars.Input["objectType"] != "competitor" {
		t.Errorf("objectType not sent: %v", vars.Input)
	}
	props, _ := vars.Input["properties"].(map[string]any)
	if props["tier"] != "pro" {
		t.Errorf("properties not sent: %v", vars.Input["properties"])
	}
	// A field the user didn't touch must be omitted (preserve), not nulled.
	if _, present := vars.Input["data"]; present {
		t.Errorf("unset data must be omitted, got %v", vars.Input["data"])
	}
}

// --object-type "" sends an explicit empty string, which the server normalizes
// to null (clear → ordinary node). The flag is tri-state: omitted = preserve.
func TestNodeUpdateObjectTypeClear(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--object-type", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	v, present := vars.Input["objectType"]
	if !present || v != "" {
		t.Errorf("--object-type \"\" should send an explicit empty string, got %v (present=%v)", v, present)
	}
}

// Malformed --properties is a client-side usage error (exit 2) before any
// round-trip — CreateNode must never be reached.
func TestNodeAddPropertiesInvalidJSONIsUsageError(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x", "--name", "X",
		"--properties", `{bad json`, "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("malformed --properties should be a usage error, got %v", err)
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("CreateNode must not be called when --properties is invalid")
	}
}

// --properties and --properties-file are mutually exclusive — and (PR #269
// review) the guard is Changed()-based, so an explicit empty inline value still
// trips it rather than letting the file silently win.
func TestNodeAddPropertiesFlagsMutuallyExclusive(t *testing.T) {
	for _, inline := range []string{`{}`, ``} { // "" is the review's bypass case
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x", "--name", "X",
			"--properties", inline, "--properties-file", "/tmp/x.json"})
		err := root.Execute()
		if err == nil || exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("--properties %q + --properties-file should be a usage error, got %v", inline, err)
		}
	}
}

// (PR #269 review) An explicitly-empty --properties "" on create must reach
// validation and fail as invalid JSON — consistent with node update, not a
// silent no-op. CreateNode must never be reached.
func TestNodeAddEmptyPropertiesIsUsageError(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x", "--name", "X",
		"--properties", "", "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("--properties \"\" should be a usage error (invalid JSON), got %v", err)
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("CreateNode must not be called when --properties is an empty string")
	}
}

// A schema-conformance violation is the server's call: its BAD_USER_INPUT
// message must surface verbatim (routed through api.MapError), not be masked.
func TestNodeAddConformanceViolationSurfacedVerbatim(t *testing.T) {
	const serverMsg = "property 'tier' must be one of [free, pro, enterprise]"
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateNode": `{"errors":[{"message":"` + serverMsg + `","extensions":{"code":"BAD_USER_INPUT"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "competitors:x",
		"--name", "X", "--object-type", "competitor", "--properties", `{"tier":"unlimited"}`,
		"--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected the server conformance error")
	}
	if !strings.Contains(err.Error(), serverMsg) {
		t.Errorf("server BAD_USER_INPUT message must surface verbatim, got %q", err.Error())
	}
}

// memory set --schema threads the parsed JSON into updateMemory(schema:). A bare
// id positional skips the GetMemory round-trip (resolveMemoryID short-circuits).
func TestMemorySetSchema(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "m1",
		"--schema", `{"objectTypes":{"competitor":{"fields":{"tier":{"type":"text"}}}}}`,
		"--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	sch, ok := vars["schema"].(map[string]any)
	if !ok {
		t.Fatalf("schema not sent as an object: %v", vars["schema"])
	}
	ot, _ := sch["objectTypes"].(map[string]any)
	if _, hasComp := ot["competitor"]; !hasComp {
		t.Errorf("schema payload not threaded through: %v", vars["schema"])
	}
}

// --schema "null" (and "") clears the schema — an explicit JSON null on the wire,
// distinct from an omitted (preserve) field.
func TestMemorySetSchemaClear(t *testing.T) {
	for _, clear := range []string{"null", ""} {
		gql, captured := captureGraphQL(t, map[string]string{
			"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "set", "m1", "--schema", clear, "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("clear %q execute: %v", clear, err)
		}
		// The raw wire must carry an explicit null (not omit it) so the server clears.
		raw := string(captured["UpdateMemory"])
		if !strings.Contains(raw, `"schema":null`) {
			t.Errorf("--schema %q should send explicit schema:null, got %s", clear, raw)
		}
	}
}

// A malformed --schema is a client-side usage error (exit 2) before any network.
func TestMemorySetSchemaMalformedIsUsageError(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "m1", "--schema", `{"objectTypes":`, "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("malformed --schema should be a usage error, got %v", err)
	}
	if _, ok := captured["UpdateMemory"]; ok {
		t.Error("UpdateMemory must not be called when --schema is malformed")
	}
}

// createMemory takes no schema, so a create with --schema applies it in a
// follow-up updateMemory against the just-created memory.
func TestMemorySetCreateWithSchema(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "Research",
		"--schema", `{"objectTypes":{"insight":{"fields":{}}}}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, created := captured["CreateMemory"]; !created {
		t.Fatal("createMemory should run first")
	}
	var uvars map[string]any
	if err := json.Unmarshal(captured["UpdateMemory"], &uvars); err != nil {
		t.Fatalf("the follow-up updateMemory should carry the schema: %v", err)
	}
	if uvars["id"] != "m1" {
		t.Errorf("follow-up should target the created memory id, got %v", uvars["id"])
	}
	if _, ok := uvars["schema"].(map[string]any); !ok {
		t.Errorf("follow-up updateMemory should carry the schema, got %v", uvars["schema"])
	}
}

// A malformed schema the server rejects (well-formed JSON, bad shape) surfaces
// the server's BAD_USER_INPUT message verbatim.
func TestMemorySetSchemaServerRejectionSurfaced(t *testing.T) {
	const serverMsg = "schema.objectTypes.competitor.fields.tier.type must be one of text|number|datetime|boolean|enum"
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateMemory": `{"errors":[{"message":"` + serverMsg + `","extensions":{"code":"BAD_USER_INPUT"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "m1", "--schema", `{"objectTypes":{"competitor":{"fields":{"tier":{"type":"bogus"}}}}}`, "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), serverMsg) {
		t.Errorf("server schema rejection must surface verbatim, got %v", err)
	}
}
