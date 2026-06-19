package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// A masked AiServiceConfig as the create/update mutations return it.
const aiCfgJSON = `{"id":"cfg1","name":"default","ownerType":"APP","ownerId":"app1",
	"provider":"anthropic","model":"claude-opus-4-8","hasApiKey":true,"apiKeyPreview":"abcd",
	"params":{"maxTokens":4096},"enabled":true,"createdAt":"2026-06-19T00:00:00Z","updatedAt":null}`

func TestAiConfigCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAiServiceConfig": `{"data":{"createAiServiceConfig":` + aiCfgJSON + `}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("sk-secret-123\n") // --api-key - reads this
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--app", "acme.com:juno-app",
		"--name", "default", "--provider", "anthropic", "--model", "claude-opus-4-8",
		"--api-key", "-", "--param", "maxTokens=4096", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAiServiceConfig"], &vars)
	if vars["name"] != "default" || vars["provider"] != "anthropic" || vars["model"] != "claude-opus-4-8" {
		t.Errorf("core fields wrong: %v", vars)
	}
	if vars["ownerType"] != "APP" || vars["ownerId"] != "acme.com:juno-app" {
		t.Errorf("--app should map to ownerType APP + ownerId: %v", vars)
	}
	if vars["apiKey"] != "sk-secret-123" {
		t.Errorf("--api-key - should read+trim stdin, got %v", vars["apiKey"])
	}
	if vars["enabled"] != true {
		t.Errorf("enabled should default true: %v", vars["enabled"])
	}
	if p, _ := vars["params"].(map[string]any); p["maxTokens"] != float64(4096) {
		t.Errorf("--param should build the params object: %v", vars["params"])
	}
	// The key must never be echoed — output is masked to the preview.
	if strings.Contains(out.String(), "sk-secret-123") {
		t.Errorf("create output leaked the API key:\n%s", out.String())
	}
}

func TestAiConfigCreateOmitsEmptyApiKey(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAiServiceConfig": `{"data":{"createAiServiceConfig":` + aiCfgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--org", "acme.com", "--name", "fast",
		"--provider", "openai", "--model", "gpt-4o-mini", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAiServiceConfig"], &vars)
	if _, present := vars["apiKey"]; present {
		t.Errorf("apiKey must be omitted when --api-key isn't passed, got %v", vars["apiKey"])
	}
	if vars["ownerType"] != "ORGANIZATION" {
		t.Errorf("--org should map to ownerType ORGANIZATION, got %v", vars["ownerType"])
	}
}

func TestAiConfigCreateRequiresOwner(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--name", "x", "--provider", "anthropic",
		"--model", "m", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "owner is required") {
		t.Fatalf("expected owner-required usage error, got %v", err)
	}
}

func TestAiConfigCreateRejectsMultipleOwners(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--app", "a", "--org", "o",
		"--name", "x", "--provider", "p", "--model", "m", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestAiConfigUpdatePreservesUnsetFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAiServiceConfig": `{"data":{"updateAiServiceConfig":` + aiCfgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "update", "cfg1", "--model", "claude-opus-4-8", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAiServiceConfig"], &vars)
	if vars["id"] != "cfg1" || vars["model"] != "claude-opus-4-8" {
		t.Errorf("id/model wrong: %v", vars)
	}
	// Unset fields must be OMITTED (preserve), especially apiKey.
	for _, k := range []string{"name", "provider", "apiKey", "enabled", "params"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted from update, got %v", k, vars[k])
		}
	}
}

func TestAiConfigUpdateClearsApiKey(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAiServiceConfig": `{"data":{"updateAiServiceConfig":` + aiCfgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "update", "cfg1", "--api-key", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAiServiceConfig"], &vars)
	// An explicit empty --api-key must be SENT as "" (the server's clear signal),
	// not dropped by omitempty.
	if v, present := vars["apiKey"]; !present || v != "" {
		t.Errorf("--api-key \"\" must send an empty string to clear, got %v (present=%v)", v, present)
	}
}

func TestAiConfigUpdateNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "update", "cfg1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update error, got %v", err)
	}
}

func TestAiConfigRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "rm", "cfg1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestAiConfigRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteAiServiceConfig": `{"data":{"deleteAiServiceConfig":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "rm", "cfg1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteAiServiceConfig"], &vars)
	if vars["id"] != "cfg1" {
		t.Errorf("delete id should be forwarded, got %v", vars)
	}
}
