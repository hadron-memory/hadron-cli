package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureGraphQL is like fakeGraphQL but also records request bodies
// per operation so tests can assert on variables.
func captureGraphQL(t *testing.T, responses map[string]string) (*httptest.Server, map[string]json.RawMessage) {
	t.Helper()
	captured := map[string]json.RawMessage{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured[body.OperationName] = body.Variables
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

const nodeJSON = `{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
	"nodeType":"finding","tags":["ci"],"updatedAt":"2026-06-11T00:00:00Z"}`

const nodeDetailJSON = `{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI",
	"description":null,"abstract":null,"nodeType":"finding","tags":["ci"],
	"content":"The CI is flaky because...","seq":null,
	"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
	"outgoingEdges":[{"id":"e1","label":"routes-to","priority":0,
		"target":{"id":"n2","loc":"start-here","memoryId":"mem1"}}],
	"incomingEdges":[]}`

const resolveNodeJSON = `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`

const nodeURN = "acme.com:kb:findings:flaky-ci"

func TestNodeLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Nodes": `{"data":{"nodes":[` + nodeJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "--memory", "acme.com:kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "findings:flaky-ci") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		Memory string `json:"memory"`
	}
	_ = json.Unmarshal(captured["Nodes"], &vars)
	if vars.Memory != "acme.com:kb" {
		t.Errorf("--memory should map to the memory arg, got %q", vars.Memory)
	}
}

func TestNodeGet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"GetNodeById": `{"data":{"nodeById":` + nodeDetailJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "The CI is flaky") {
		t.Errorf("content missing from output: %s", out.String())
	}
	if !strings.Contains(out.String(), "routes-to") {
		t.Errorf("edges missing from output: %s", out.String())
	}
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if vars.Urn != "hrn:node:"+nodeURN {
		t.Errorf("resolveUrn must receive the hrn:node:-prefixed URN, got %q", vars.Urn)
	}
}

func TestNodeGetRejectsBareLoc(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "findings:flaky-ci", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified") {
		t.Fatalf("expected fully-qualified URN usage error, got %v", err)
	}
}

func TestNodeGetWrongKindIsUsageError(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"m1","kind":"memory","memoryId":null}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "acme.com:kb:whatever", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a node") {
		t.Fatalf("expected wrong-kind usage error, got %v", err)
	}
}

func TestNodeGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "acme.com:kb:nope", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestNodeAddSendsCreateOnly(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpsertNode": `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com:kb", "--loc", "findings:flaky-ci",
		"--name", "Flaky CI", "--content", "body", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &vars)
	if vars.Input["createOnly"] != true {
		t.Errorf("add must set createOnly, got %v", vars.Input["createOnly"])
	}
	if vars.Input["content"] != "body" {
		t.Errorf("content not sent: %v", vars.Input)
	}
}

func TestNodeUpdatePreservesUnsetFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"GetNodeById": `{"data":{"nodeById":` + nodeDetailJSON + `}}`,
		"UpsertNode":  `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--name", "Flaky CI (resolved)", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &vars)
	if vars.Input["name"] != "Flaky CI (resolved)" {
		t.Errorf("name not updated: %v", vars.Input)
	}
	if vars.Input["memoryId"] != "mem1" || vars.Input["loc"] != "findings:flaky-ci" {
		t.Errorf("memoryId/loc must come from the fetched node: %v", vars.Input)
	}
	// Unset optional fields must be OMITTED (server: null clears).
	for _, key := range []string{"content", "description", "abstract", "nodeType"} {
		if _, present := vars.Input[key]; present {
			t.Errorf("unset field %q must be omitted from upsert input, got %v", key, vars.Input[key])
		}
	}
}

func TestNodeUpdateClearsFieldWithEmptyString(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"GetNodeById": `{"data":{"nodeById":` + nodeDetailJSON + `}}`,
		"UpsertNode":  `{"data":{"upsertNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN,
		"--description", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpsertNode"], &vars)
	// An explicitly-passed empty string must be SENT (the server
	// normalizes it to null and clears the field), not omitted.
	if v, present := vars.Input["description"]; !present || v != "" {
		t.Errorf("explicit --description \"\" must send an empty string, got %v (present=%v)", v, present)
	}
}

func TestNodeRmRequiresYesNonInteractive(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"GetNodeById": `{"data":{"nodeById":` + nodeDetailJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "rm", nodeURN, "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestNodeRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"GetNodeById": `{"data":{"nodeById":` + nodeDetailJSON + `}}`,
		"DeleteNode":  `{"data":{"deleteNode":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "rm", nodeURN, "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Loc      string `json:"loc"`
		MemoryID string `json:"memoryId"`
	}
	_ = json.Unmarshal(captured["DeleteNode"], &vars)
	if vars.Loc != "findings:flaky-ci" || vars.MemoryID != "mem1" {
		t.Errorf("delete args must come from the fetched node: %+v", vars)
	}
}

func TestEdgeAddOmitsUnsetOptionals(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add", "--from", nodeURN, "--to", nodeURN,
		"--label", "routes-to", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateEdge"], &vars)
	if vars["label"] != "routes-to" {
		t.Errorf("label not sent: %v", vars)
	}
	// Unset optionals must be OMITTED, not sent as explicit nulls — the
	// server rejects priority: null (hadron-server#263).
	for _, key := range []string{"priority", "condition", "data"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from createEdge variables, got %v", key, v)
		}
	}
}

func TestEdgeUpdateLabelOnlyPreservesCondition(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateEdge": `{"data":{"updateEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--label", "complements", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateEdge"], &vars)
	if vars["edgeId"] != "e1" || vars["label"] != "complements" {
		t.Errorf("unexpected update vars: %v", vars)
	}
	// An explicit null clears condition/data server-side, so a
	// label-only update must omit them entirely.
	for _, key := range []string{"priority", "condition", "data"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from updateEdge variables, got %v", key, v)
		}
	}
}

func TestEdgeUpdateExplicitNullClearsCondition(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateEdge": `{"data":{"updateEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--condition", "null", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateEdge"], &vars)
	// --condition null is the explicit clear: it must be SENT as a
	// JSON null, not dropped by omitempty.
	if v, present := vars["condition"]; !present || v != nil {
		t.Errorf("explicit --condition null must send null, got %v (present=%v)", v, present)
	}
}

const memoryJSON = `{"id":"m1","urn":"acme.com:kb","name":"KB","shortDescription":null,
	"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
	"isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}`

func TestMemorySetCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--org", "acme.com", "--name", "KB", "--class", "knowledge", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateMemory"], &vars)
	if vars["orgId"] != "acme.com" || vars["name"] != "KB" {
		t.Errorf("unexpected create vars: %v", vars)
	}
}

func TestMemorySetUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MyMemories":   `{"data":{"myMemories":[` + memoryJSON + `]}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "acme.com:kb", "--short", "Project knowledge", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	// The URN is resolved to the PK via myMemories before updateMemory.
	if vars["id"] != "m1" || vars["shortDescription"] != "Project knowledge" {
		t.Errorf("unexpected update vars: %v", vars)
	}
	// Unset optionals must be OMITTED, not sent as explicit nulls —
	// the server treats omitted as "preserve".
	for _, key := range []string{"name", "description", "tags", "visibility"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from updateMemory variables, got %v", key, v)
		}
	}
}

func TestMemorySetUpdateSendsTags(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MyMemories":   `{"data":{"myMemories":[` + memoryJSON + `]}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "acme.com:kb", "--tag", "go", "--tag", "cli", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Tags []string `json:"tags"`
	}
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	if len(vars.Tags) != 2 || vars.Tags[0] != "go" || vars.Tags[1] != "cli" {
		t.Errorf("tags not sent: %s", captured["UpdateMemory"])
	}
}

func TestMemorySetCreateRequiresOrgAndName(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--name", "KB", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--org") {
		t.Fatalf("expected org/name usage error, got %v", err)
	}
}

func TestMemoryRm(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteMemory": `{"data":{"deleteMemory":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "rm", "acme.com:scratch", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteMemory"], &vars)
	if vars["id"] != "acme.com:scratch" {
		t.Errorf("unexpected delete vars: %v", vars)
	}
}

func TestMemoryClone(t *testing.T) {
	cloneJSON := `{"id":"m2","urn":"acme.com:kb-fork","name":"kb-fork","shortDescription":null,
		"class":"knowledge","visibility":"ORGANIZATION","organizationId":"org1",
		"isEncrypted":false,"updatedAt":"2026-06-12T00:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"CloneMemory": `{"data":{"cloneMemory":` + cloneJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "clone", "acme.com:kb", "--name", "kb-fork", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CloneMemory"], &vars)
	if vars["id"] != "acme.com:kb" || vars["name"] != "kb-fork" {
		t.Errorf("unexpected clone vars: %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["urn"] != "acme.com:kb-fork" {
		t.Errorf("unexpected output dto: %v", dto)
	}
}

func TestMemoryCloneRequiresName(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "clone", "acme.com:kb"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when --name is missing")
	}
}

const appJSON = `{"id":"app1","urn":"urn:agent:acme.com::bot::acme.com:helper","name":"Bot",
	"appType":"CHATBOT","agentId":"agent1","memberCount":2,"createdAt":"2026-06-11T00:00:00Z"}`

func TestAppLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Apps": `{"data":{"apps":[` + appJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "ls", "--org", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "CHATBOT") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["Apps"], &vars)
	if vars["orgId"] != "acme.com" {
		t.Errorf("unexpected vars: %v", vars)
	}
}

func TestAppInstall(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateApp": `{"data":{"createApp":` + appJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--org", "acme.com", "--agent", "agent1",
		"--name", "Bot", "--type", "CHATBOT", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateApp"], &vars)
	if vars["orgId"] != "acme.com" || vars["agentId"] != "agent1" || vars["appType"] != "CHATBOT" {
		t.Errorf("unexpected vars: %v", vars)
	}
	// Unset optionals must be OMITTED, not sent as explicit nulls.
	for _, key := range []string{"urn", "description"} {
		if v, present := vars[key]; present {
			t.Errorf("unset %q must be omitted from createApp variables, got %v", key, v)
		}
	}
}

func TestAppUninstall(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteApp": `{"data":{"deleteApp":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "uninstall", "app1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteApp"], &vars)
	if vars["id"] != "app1" {
		t.Errorf("unexpected vars: %v", vars)
	}
}
