package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// hadron-cli#2114 items 2 and 3: the HUMAN create/update receipts say the
// lifecycle the server returned for a write that sent --lifecycle (and say
// nothing when none was sent); the JSON contract keeps its two distinct
// representations: write receipts OMIT lifecycle when not sent, `agent get`
// carries an explicit null when the server cannot report it.

func agentWithLifecycle2114(value string) string {
	return strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"lifecycle":"`+value+`",`, 1)
}

func TestAgentCreateUpdateHumanReceiptsNameTheReturnedLifecycle2114(t *testing.T) {
	for _, value := range []string{"SHORT_LIVED", "LONG_LIVED"} {
		body := agentWithLifecycle2114(value)
		for name, tc := range map[string]struct {
			op, field string
			argv      []string
			verb      string
		}{
			"create": {"CreateAgentWithLifecycle", "createAgent", []string{"agent", "create", "--name", "Engineer", "--lifecycle", value}, "✓ created agent"},
			"update": {"UpdateAgentWithLifecycle", "updateAgent", []string{"agent", "update", "agt1", "--lifecycle", value}, "✓ updated agent"},
		} {
			_, out, err := runTeamTask(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + body + `}}`}, tc.argv...)
			if err != nil {
				t.Fatalf("%s %s: %v", name, value, err)
			}
			if !strings.Contains(out, tc.verb) || !strings.Contains(out, "  lifecycle: "+value+"\n") {
				t.Errorf("%s %s: the receipt must confirm the returned lifecycle:\n%s", name, value, out)
			}
		}
	}
}

// The receipt shows what the SERVER returned, not what was asked for.
func TestAgentHumanReceiptShowsTheServersLifecycleNotTheFlag2114(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{"UpdateAgentWithLifecycle": `{"data":{"updateAgent":` + agentWithLifecycle2114("LONG_LIVED") + `}}`}, "agent", "update", "agt1", "--lifecycle", "SHORT_LIVED")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lifecycle: LONG_LIVED") || strings.Contains(out, "SHORT_LIVED") {
		t.Fatalf("the receipt must report the returned value:\n%s", out)
	}
}

func TestAgentHumanReceiptsAreSilentAboutLifecycleWhenNoneWasSent2114(t *testing.T) {
	for name, tc := range map[string]struct {
		op, field string
		argv      []string
	}{
		"create": {"CreateAgent", "createAgent", []string{"agent", "create", "--name", "Engineer"}},
		"update": {"UpdateAgent", "updateAgent", []string{"agent", "update", "agt1", "--name", "Renamed"}},
	} {
		_, out, err := runTeamTask(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + agentJSON + `}}`}, tc.argv...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(strings.ToLower(out), "lifecycle") {
			t.Errorf("%s: no --lifecycle was sent, so the receipt says nothing about it:\n%s", name, out)
		}
	}
}

// Both JSON representations, pinned side by side so they cannot drift together.
func TestAgentLifecycleJSONContracts2114(t *testing.T) {
	keys := func(out string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		return m
	}
	// Write receipt, lifecycle not sent: the key is ABSENT.
	_, out, err := runTeamTask(t, map[string]string{"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`}, "agent", "update", "agt1", "--name", "Renamed", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := keys(out)["lifecycle"]; present {
		t.Errorf("a write that sent no lifecycle must omit the key:\n%s", out)
	}
	// Write receipt, lifecycle sent: the returned value.
	_, out, err = runTeamTask(t, map[string]string{"UpdateAgentWithLifecycle": `{"data":{"updateAgent":` + agentWithLifecycle2114("SHORT_LIVED") + `}}`}, "agent", "update", "agt1", "--lifecycle", "SHORT_LIVED", "--json")
	if err != nil {
		t.Fatalf("a receipt followed by an error is not a success: %v", err)
	}
	if keys(out)["lifecycle"] != "SHORT_LIVED" {
		t.Errorf("the returned lifecycle belongs in the receipt:\n%s", out)
	}
	// get on a server that cannot report it: an explicit null, key present.
	_, out, err = runTeamTask(t, map[string]string{
		"GetAgent":          `{"data":{"agent":` + agentJSON + `}}`,
		"GetAgentLifecycle": `{"errors":[{"message":"Cannot query field \"lifecycle\" on type \"Agent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	}, "agent", "get", "agt1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v, present := keys(out)["lifecycle"]; !present || v != nil {
		t.Errorf("get must say lifecycle: null when the server cannot report it:\n%s", out)
	}
	// get on a capable server: the value, exactly once.
	_, out, err = runTeamTask(t, map[string]string{"GetAgent": `{"data":{"agent":` + agentJSON + `}}`, "GetAgentLifecycle": `{"data":{"agent":` + agentWithLifecycle2114("LONG_LIVED") + `}}`}, "agent", "get", "agt1", "--json")
	if err != nil {
		t.Fatalf("a receipt followed by an error is not a success: %v", err)
	}
	if keys(out)["lifecycle"] != "LONG_LIVED" || strings.Count(out, `"lifecycle"`) != 1 {
		t.Errorf("get carries the value once:\n%s", out)
	}
}

// The install-into create has its own emitter; its human receipt confirms the
// lifecycle too, and `install` still inlines beside it in JSON.
func TestAgentCreateInstallIntoHumanReceiptNamesTheReturnedLifecycle2114(t *testing.T) {
	r := map[string]string{
		"CreateAgentWithLifecycle": `{"data":{"createAgent":` + agentWithLifecycle2114("SHORT_LIVED") + `}}`,
		"InstallAgentIntoApp":      installResp,
	}
	_, out, err := runTeamTask(t, r, "agent", "create", "--org", "acme.com", "--name", "Engineer", "--lifecycle", "SHORT_LIVED", "--install-into", "hrn:app:acme.com:eng-team")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓ created agent") || !strings.Contains(out, "  lifecycle: SHORT_LIVED\n") {
		t.Fatalf("the install-into receipt must confirm the lifecycle:\n%s", out)
	}
	r2 := map[string]string{"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`, "InstallAgentIntoApp": installResp}
	_, out, err = runTeamTask(t, r2, "agent", "create", "--org", "acme.com", "--name", "Engineer", "--install-into", "hrn:app:acme.com:eng-team")
	if err != nil || strings.Contains(strings.ToLower(out), "lifecycle") {
		t.Fatalf("no lifecycle was sent: %v\n%s", err, out)
	}
}
