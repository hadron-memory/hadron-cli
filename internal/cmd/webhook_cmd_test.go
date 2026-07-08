package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const webhookJSON = `{"id":"wh1","organizationId":"org1","appId":"app1","agentId":null,
	"name":"deploy-notify","enabled":true,"entryNodeUrn":"hrn:node:acme.com::ops::tasks:on-deploy",
	"aiConfigName":null,"userId":null,"createdBy":"u1","argsSchema":null,"eventData":null,
	"policy":null,"lastCalledAt":null,"createdAt":"2026-07-05T00:00:00Z"}`

// The create/rotate credentials envelope — path + shown-once token + webhook.
const webhookCredsJSON = `{"path":"/hooks/s3cr3t/deploy-notify","token":"hpt-abc123",
	"webhook":` + webhookJSON + `}`

func TestWebhookCreatePrintsShownOnceSecret(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgentWebhook": `{"data":{"createAgentWebhook":` + webhookCredsJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "create", "--app", "acme.com:ops", "--name", "deploy-notify",
		"--entry", "acme.com::ops::tasks:on-deploy", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateAgentWebhook"], &vars)
	if vars.Input["appRef"] != "acme.com:ops" || vars.Input["name"] != "deploy-notify" {
		t.Errorf("core fields wrong: %v", vars.Input)
	}
	if vars.Input["entryNodeUrn"] != "hrn:node:acme.com::ops::tasks:on-deploy" {
		t.Errorf("entry must be canonicalized, got %v", vars.Input["entryNodeUrn"])
	}
	// The secret path + token + shown-once warning must be printed.
	got := out.String()
	for _, want := range []string{"/hooks/s3cr3t/deploy-notify", "hpt-abc123", "Shown once"} {
		if !strings.Contains(got, want) {
			t.Errorf("create output missing %q:\n%s", want, got)
		}
	}
}

func TestWebhookCreateJSONCarriesSecret(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgentWebhook": `{"data":{"createAgentWebhook":` + webhookCredsJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "create", "--app", "app1", "--name", "deploy-notify",
		"--entry", "hrn:node:acme.com::ops::x", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Path    string `json:"path"`
		Token   string `json:"token"`
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if dto.Path == "" || dto.Token == "" || dto.Webhook.ID != "wh1" {
		t.Errorf("--json must carry path/token/webhook, got %+v", dto)
	}
}

func TestWebhookRotateRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "rotate", "wh1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestWebhookRotateWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RotateAgentWebhook": `{"data":{"rotateAgentWebhook":` + webhookCredsJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "rotate", "wh1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RotateAgentWebhook"], &vars)
	if vars["id"] != "wh1" {
		t.Errorf("rotate must forward the id, got %v", vars["id"])
	}
	if !strings.Contains(out.String(), "hpt-abc123") {
		t.Errorf("rotate output missing new token: %s", out.String())
	}
}

func TestWebhookLsHidesSecret(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AgentWebhooks": `{"data":{"agentWebhooks":{"total":1,"items":[` + webhookJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "ls", "--app", "acme.com:ops", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AgentWebhooks"], &vars)
	if vars["appRef"] != "acme.com:ops" {
		t.Errorf("--app should map to appRef, got %v", vars["appRef"])
	}
	got := out.String()
	if !strings.Contains(got, "deploy-notify") {
		t.Errorf("output missing webhook: %s", got)
	}
	// The listing must never carry a secret token or path field.
	for _, leak := range []string{`"token"`, `"path"`} {
		if strings.Contains(got, leak) {
			t.Errorf("webhook ls --json leaked %s:\n%s", leak, got)
		}
	}
}

func TestWebhookRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "rm", "wh1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestWebhookRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteAgentWebhook": `{"data":{"deleteAgentWebhook":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"webhook", "rm", "wh1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteAgentWebhook"], &vars)
	if vars["id"] != "wh1" {
		t.Errorf("delete must forward the id, got %v", vars["id"])
	}
}
