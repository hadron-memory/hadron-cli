package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// appRunJSON is an AppRun as the trigger/get/cancel ops return it — a terminal
// COMPLETED run so a --wait test needs no polling.
const appRunJSON = `{"id":"run1","organizationId":"org1","appId":"app1","agentId":"agent1",
	"status":"COMPLETED","triggerKind":"MANUAL","triggerId":null,
	"entryNodeUrn":"hrn:node:acme.com::ops::tasks:digest","curNodeUrn":null,
	"userId":"u1","createdBy":"u1","parentRunId":null,"attempts":1,
	"budgetActions":50,"budgetTokens":10000,"timeoutMs":60000,"policy":null,
	"eventData":{"topic":"security"},"failure":null,
	"createdAt":"2026-07-05T00:00:00Z","startedAt":"2026-07-05T00:00:01Z","finishedAt":"2026-07-05T00:00:09Z"}`

func TestRunTrigger(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"TriggerAppRun": `{"data":{"triggerAppRun":` + appRunJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "trigger", "--app", "acme.com:ops",
		"--entry", "acme.com::ops::tasks:digest", "--arg", "topic=security",
		"--as-self", "--ai-config", "fast", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["TriggerAppRun"], &vars)
	if vars.Input["appId"] != "acme.com:ops" {
		t.Errorf("appId should map from --app: %v", vars.Input["appId"])
	}
	// A bare <org>::<memory>::<loc> entry must be normalized to the hrn:node: form.
	if vars.Input["entryNodeUrn"] != "hrn:node:acme.com::ops::tasks:digest" {
		t.Errorf("entry must be canonicalized, got %v", vars.Input["entryNodeUrn"])
	}
	if vars.Input["runAsSelf"] != true {
		t.Errorf("--as-self should send runAsSelf:true, got %v", vars.Input["runAsSelf"])
	}
	if vars.Input["aiConfigName"] != "fast" {
		t.Errorf("--ai-config should map to aiConfigName, got %v", vars.Input["aiConfigName"])
	}
	ev, _ := vars.Input["eventData"].(map[string]any)
	if ev["topic"] != "security" {
		t.Errorf("--arg should build eventData, got %v", vars.Input["eventData"])
	}
	// The run id must reach the JSON output.
	if !strings.Contains(out.String(), "run1") {
		t.Errorf("output missing run id:\n%s", out.String())
	}
}

// Unset --as-self / --ai-config must be OMITTED from the wire, not sent as null.
func TestRunTriggerOmitsUnsetOptionals(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"TriggerAppRun": `{"data":{"triggerAppRun":` + appRunJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "trigger", "--app", "app1",
		"--entry", "hrn:node:acme.com::ops::tasks:digest", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["TriggerAppRun"], &vars)
	for _, k := range []string{"runAsSelf", "aiConfigName", "eventData"} {
		if v, present := vars.Input[k]; present {
			t.Errorf("unset %q must be omitted from the trigger input, got %v", k, v)
		}
	}
	// A scheme-prefixed entry passes through verbatim.
	if vars.Input["entryNodeUrn"] != "hrn:node:acme.com::ops::tasks:digest" {
		t.Errorf("prefixed entry must pass through, got %v", vars.Input["entryNodeUrn"])
	}
}

func TestRunTriggerRejectsBareLocEntry(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "trigger", "--app", "app1", "--entry", "tasks:digest", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fully-qualified") {
		t.Fatalf("expected fully-qualified entry usage error, got %v", err)
	}
}

func TestRunTriggerWaitSkipsPollWhenTerminal(t *testing.T) {
	// triggerAppRun already returns COMPLETED, so --wait must NOT poll appRun
	// (captureGraphQL errors on any unexpected operation).
	gql, _ := captureGraphQL(t, map[string]string{
		"TriggerAppRun": `{"data":{"triggerAppRun":` + appRunJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "trigger", "--app", "app1",
		"--entry", "hrn:node:acme.com::ops::tasks:digest", "--wait", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "COMPLETED") {
		t.Errorf("wait output missing terminal status:\n%s", out.String())
	}
}

// A --wait that observes a non-COMPLETED terminal status must exit non-zero so
// a script can branch on the run's outcome — while still printing the run.
func TestRunTriggerWaitFailedExitsNonZero(t *testing.T) {
	failed := strings.Replace(appRunJSON, `"status":"COMPLETED"`, `"status":"FAILED"`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"TriggerAppRun": `{"data":{"triggerAppRun":` + failed + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "trigger", "--app", "app1",
		"--entry", "hrn:node:acme.com::ops::tasks:digest", "--wait", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a FAILED run under --wait must return a non-nil (non-zero exit) error")
	}
	// The run — including its FAILED status — must still be printed.
	if !strings.Contains(out.String(), "FAILED") {
		t.Errorf("the failed run must still be printed:\n%s", out.String())
	}
}

// A --wait whose poll fails (or times out) after the run was created must still
// print the run record — the run exists, so a --json caller needs its id/status
// to inspect — while returning the wait error (Codex PR #153 review).
func TestRunTriggerWaitPollErrorStillPrintsRun(t *testing.T) {
	pending := strings.Replace(appRunJSON, `"status":"COMPLETED"`, `"status":"PENDING"`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"TriggerAppRun": `{"data":{"triggerAppRun":` + pending + `}}`,
		"AppRun":        `{"errors":[{"message":"transient poll failure"}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "trigger", "--app", "app1",
		"--entry", "hrn:node:acme.com::ops::tasks:digest", "--wait", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a failed --wait poll must return a non-nil error")
	}
	// The created run (from the trigger response) must still be printed.
	if !strings.Contains(out.String(), "run1") || !strings.Contains(out.String(), "PENDING") {
		t.Errorf("the created run must still be printed on a wait error:\n%s", out.String())
	}
}

func TestRunLsRejectsInvalidStatus(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--app", "app1", "--status", "bogus", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --status") {
		t.Fatalf("expected invalid-status usage error, got %v", err)
	}
}

func TestRunLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AppRuns": `{"data":{"appRuns":{"total":1,"items":[` + appRunJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--org", "acme.com", "--status", "completed", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AppRuns"], &vars)
	if vars["orgId"] != "acme.com" {
		t.Errorf("--org should map to orgId, got %v", vars["orgId"])
	}
	// --status is upper-cased into the enum.
	if vars["status"] != "COMPLETED" {
		t.Errorf("--status should be upper-cased, got %v", vars["status"])
	}
	if v, present := vars["appId"]; present {
		t.Errorf("appId must be omitted when scoping by --org, got %v", v)
	}
	if !strings.Contains(out.String(), "run1") {
		t.Errorf("output missing run id:\n%s", out.String())
	}
}

func TestRunLsRejectsAppAndOrg(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--app", "a", "--org", "o", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestRunGet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AppRun": `{"data":{"appRun":` + appRunJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "get", "run1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AppRun"], &vars)
	if vars["ref"] != "run1" {
		t.Errorf("get must forward the ref, got %v", vars["ref"])
	}
	if !strings.Contains(out.String(), "tasks:digest") {
		t.Errorf("detail missing entry: %s", out.String())
	}
}

func TestRunGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AppRun": `{"data":{"appRun":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "get", "nope", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestRunCancelRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "cancel", "run1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestRunCancelWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CancelAppRun": `{"data":{"cancelAppRun":` + strings.Replace(appRunJSON, `"status":"COMPLETED"`, `"status":"CANCELLED"`, 1) + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "cancel", "run1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CancelAppRun"], &vars)
	if vars["id"] != "run1" {
		t.Errorf("cancel must forward the id, got %v", vars["id"])
	}
	if !strings.Contains(out.String(), "CANCELLED") {
		t.Errorf("cancel output missing status: %s", out.String())
	}
}
