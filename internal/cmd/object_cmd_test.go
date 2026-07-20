package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #272: the `hadron object` group — the object store CLI surface (server #745).
// An object is a flat record { id, type, ...fields }.

func TestObjectCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateObject": `{"data":{"createObject":{"id":"o1","type":"competitor","name":"Letta","stage":"series-a"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "create", "-m", "acme.com::market", "--type", "competitor",
		"--fields", `{"name":"Letta","stage":"series-a"}`, "--key", "letta", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		MemoryRef  string         `json:"memoryRef"`
		ObjectType string         `json:"objectType"`
		Fields     map[string]any `json:"fields"`
		Key        *string        `json:"key"`
		Name       *string        `json:"name"`
	}
	if err := json.Unmarshal(captured["CreateObject"], &vars); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if vars.ObjectType != "competitor" {
		t.Errorf("type must map to the objectType var, got %q", vars.ObjectType)
	}
	if vars.MemoryRef != cmdutil.CanonicalMemoryRef("acme.com::market") {
		t.Errorf("memoryRef should be canonicalized, got %q", vars.MemoryRef)
	}
	if vars.Fields["stage"] != "series-a" {
		t.Errorf("fields not sent: %v", vars.Fields)
	}
	if vars.Key == nil || *vars.Key != "letta" {
		t.Errorf("--key not sent: %v", vars.Key)
	}
	// An unset --name must be omitted, not sent as null.
	if vars.Name != nil {
		t.Errorf("omitted --name must be omitted from the wire, got %v", *vars.Name)
	}
	// The flat object is printed (id + type + fields).
	if !strings.Contains(out.String(), `"id": "o1"`) || !strings.Contains(out.String(), "competitor") {
		t.Errorf("output should be the flat object, got %s", out.String())
	}
}

// --fields is required, and --fields + --fields-file are mutually exclusive;
// both are usage errors before any round-trip.
func TestObjectCreateFieldValidation(t *testing.T) {
	for _, args := range [][]string{
		{"object", "create", "-m", "acme.com::m", "--type", "t"},                     // no fields
		{"object", "create", "-m", "acme.com::m", "--type", "t", "--fields", `{bad`}, // invalid JSON
		{"object", "create", "-m", "acme.com::m", "--type", "t", "--fields", `{}`, "--fields-file", "/tmp/x.json"},
		{"object", "create", "-m", "acme.com::m", "--fields", `{}`}, // no --type
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		err := root.Execute()
		if err == nil || exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("%v should be a usage error, got %v", args[2:], err)
		}
	}
}

func TestObjectGet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetObject": `{"data":{"object":{"id":"o1","type":"competitor","stage":"series-a"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "get", "acme.com::market::competitor:letta", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(captured["GetObject"], &vars)
	// A node URN is canonicalized (hrn:node:) and passed through — no resolveUrn.
	if vars.Ref != "hrn:node:acme.com::market::competitor:letta" {
		t.Errorf("ref should be canonicalized, got %q", vars.Ref)
	}
	if !strings.Contains(out.String(), "series-a") {
		t.Errorf("output should be the object, got %s", out.String())
	}
}

// object(ref:) returns JSON null (not an error) when absent — the CLI maps that
// to exit 4 (not found).
func TestObjectGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetObject": `{"data":{"object":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "get", "01missing", "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitcode.FromError(err) != exitcode.NotFound {
		t.Fatalf("a null object should be exit 4 not-found, got %v", err)
	}
}

func TestObjectUpdateMerges(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateObject": `{"data":{"updateObject":{"id":"o1","type":"competitor","stage":"series-b"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "update", "o1", "--fields", `{"stage":"series-b"}`, "--reason", "round closed", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Ref    string         `json:"ref"`
		Fields map[string]any `json:"fields"`
		Reason *string        `json:"reason"`
	}
	if err := json.Unmarshal(captured["UpdateObject"], &vars); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// A raw id passes through un-prefixed (not a URN).
	if vars.Ref != "o1" {
		t.Errorf("raw id ref should pass through, got %q", vars.Ref)
	}
	if vars.Fields["stage"] != "series-b" || vars.Reason == nil || *vars.Reason != "round closed" {
		t.Errorf("fields/reason not sent: %+v", vars)
	}
}

func TestObjectDelete(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteObject": `{"data":{"deleteObject":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "delete", "o1", "--hard", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteObject"], &vars)
	if vars["hard"] != true {
		t.Errorf("--hard must send hard:true, got %v", vars["hard"])
	}
	if !strings.Contains(out.String(), "Hard-deleted") {
		t.Errorf("output should note hard delete, got %s", out.String())
	}
}

func TestObjectDeleteRequiresYes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"DeleteObject": `{"data":{"deleteObject":true}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "delete", "o1", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-interactive delete should require --yes, got %v", err)
	}
}

func TestObjectFind(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindObjects": `{"data":{"findObjects":{"objects":[{"id":"o1","type":"competitor"},{"id":"o2","type":"competitor"}],"total":2}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "find", "-m", "acme.com::market", "--type", "competitor",
		"--match", `{"stage":"series-a"}`,
		"--where", `{"path":["fundingUsd"],"as":"number","gt":10000000}`,
		"--sort", `{"fundingUsd":"desc"}`, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		ObjectType string         `json:"objectType"`
		Match      map[string]any `json:"match"`
		Where      map[string]any `json:"where"`
		Sort       map[string]any `json:"sort"`
	}
	if err := json.Unmarshal(captured["FindObjects"], &vars); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if vars.ObjectType != "competitor" || vars.Match["stage"] != "series-a" {
		t.Errorf("type/match not sent: %+v", vars)
	}
	if vars.Where["as"] != "number" || vars.Sort["fundingUsd"] != "desc" {
		t.Errorf("where/sort not sent: %+v", vars)
	}
	var result findEnvelope
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("--json output not the {objects,total} envelope: %v\n%s", err, out.String())
	}
	if len(result.Objects) != 2 || result.Total == nil || *result.Total != 2 {
		t.Errorf("envelope unexpected: %s", out.String())
	}
}

type findEnvelope struct {
	Objects []json.RawMessage `json:"objects"`
	Total   *int              `json:"total"`
}

// A schema-conformance / reserved-field violation is the server's call — its
// BAD_USER_INPUT message surfaces verbatim.
func TestObjectCreateConformanceErrorSurfaced(t *testing.T) {
	const msg = "field 'id' is reserved and cannot be an object field"
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateObject": `{"errors":[{"message":"` + msg + `","extensions":{"code":"BAD_USER_INPUT"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "create", "-m", "acme.com::market", "--type", "competitor",
		"--fields", `{"id":"nope"}`, "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Errorf("server BAD_USER_INPUT should surface verbatim, got %v", err)
	}
	if exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("BAD_USER_INPUT should be a usage error, got exit %d", exitcode.FromError(err))
	}
}
