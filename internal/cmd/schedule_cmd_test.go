package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const scheduleJSON = `{"id":"sch1","organizationId":"org1","appId":"app1","agentId":null,
	"name":"nightly-digest","cron":"0 6 * * *","timezone":"America/New_York","enabled":true,
	"entryNodeUrn":"hrn:node:acme.com::ops::tasks:digest","aiConfigName":null,
	"userId":"u1","createdBy":"u1","eventData":{"topic":"sec"},"policy":null,
	"lastRunAt":null,"nextRunAt":"2026-07-07T10:00:00Z","createdAt":"2026-07-05T00:00:00Z"}`

func TestScheduleCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgentSchedule": `{"data":{"createAgentSchedule":` + scheduleJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "create", "--app", "acme.com:ops", "--name", "nightly-digest",
		"--cron", "0 6 * * *", "--tz", "America/New_York",
		"--entry", "acme.com::ops::tasks:digest", "--arg", "topic=sec",
		"--policy", `{"allow":["comm.outbound"]}`, "--as-self", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateAgentSchedule"], &vars)
	in := vars.Input
	if in["appRef"] != "acme.com:ops" || in["name"] != "nightly-digest" || in["cron"] != "0 6 * * *" {
		t.Errorf("core fields wrong: %v", in)
	}
	if in["timezone"] != "America/New_York" {
		t.Errorf("--tz should map to timezone, got %v", in["timezone"])
	}
	if in["entryNodeUrn"] != "hrn:node:acme.com::ops::tasks:digest" {
		t.Errorf("entry must be canonicalized, got %v", in["entryNodeUrn"])
	}
	if in["enabled"] != true {
		t.Errorf("enabled should default true, got %v", in["enabled"])
	}
	if in["runAsSelf"] != true {
		t.Errorf("--as-self should send runAsSelf, got %v", in["runAsSelf"])
	}
	pol, _ := in["policy"].(map[string]any)
	if pol == nil || pol["allow"] == nil {
		t.Errorf("--policy should forward parsed JSON, got %v", in["policy"])
	}
	if !strings.Contains(out.String(), "sch1") {
		t.Errorf("output missing schedule id:\n%s", out.String())
	}
}

func TestScheduleCreateDisabledOmitsOptionals(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgentSchedule": `{"data":{"createAgentSchedule":` + scheduleJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "create", "--app", "app1", "--name", "n", "--cron", "* * * * *",
		"--entry", "hrn:node:acme.com::ops::x", "--disabled", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateAgentSchedule"], &vars)
	if vars.Input["enabled"] != false {
		t.Errorf("--disabled should send enabled:false, got %v", vars.Input["enabled"])
	}
	for _, k := range []string{"timezone", "aiConfigName", "agentRef", "runAsSelf", "eventData", "policy"} {
		if v, present := vars.Input[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, v)
		}
	}
}

func TestScheduleLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AgentSchedules": `{"data":{"agentSchedules":{"total":1,"items":[` + scheduleJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "ls", "--app", "acme.com:ops", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AgentSchedules"], &vars)
	if vars["appRef"] != "acme.com:ops" {
		t.Errorf("--app should map to appRef, got %v", vars["appRef"])
	}
	if !strings.Contains(out.String(), "nightly-digest") {
		t.Errorf("output missing schedule: %s", out.String())
	}
}

func TestScheduleUpdatePreservesUnsetFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgentSchedule": `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "update", "sch1", "--cron", "0 7 * * *", "--enabled=false", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Id    string         `json:"id"`
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateAgentSchedule"], &vars)
	if vars.Id != "sch1" {
		t.Errorf("update must target the id, got %v", vars.Id)
	}
	if vars.Input["cron"] != "0 7 * * *" {
		t.Errorf("--cron not sent: %v", vars.Input["cron"])
	}
	// --enabled=false must be SENT (tri-state), not dropped.
	if v, present := vars.Input["enabled"]; !present || v != false {
		t.Errorf("--enabled=false must send enabled:false, got %v (present=%v)", v, present)
	}
	// Unset fields must be OMITTED (omitted = preserve; null = clear).
	for _, k := range []string{"name", "timezone", "entryNodeUrn", "aiConfigName", "eventData", "policy", "runAsSelf"} {
		if v, present := vars.Input[k]; present {
			t.Errorf("unset %q must be omitted from update, got %v", k, v)
		}
	}
}

// --policy "" clears the policy: it must send an explicit null (not be silently
// omitted, which was the pre-fix no-op).
func TestScheduleUpdatePolicyEmptyClears(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgentSchedule": `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "update", "sch1", "--policy", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateAgentSchedule"], &vars)
	// An explicit null must be present (the clear signal), not omitted.
	if v, present := vars.Input["policy"]; !present || v != nil {
		t.Errorf(`--policy "" must send policy:null, got %v (present=%v)`, v, present)
	}
}

func TestScheduleUpdateNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "update", "sch1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update error, got %v", err)
	}
}

func TestScheduleRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "rm", "sch1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestScheduleRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteAgentSchedule": `{"data":{"deleteAgentSchedule":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "rm", "sch1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteAgentSchedule"], &vars)
	if vars["id"] != "sch1" {
		t.Errorf("delete must forward the id, got %v", vars["id"])
	}
}
