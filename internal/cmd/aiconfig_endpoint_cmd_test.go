package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func operationSelects(op, field string) bool {
	for _, line := range strings.Split(op, "\n") {
		if strings.TrimSpace(line) == field {
			return true
		}
	}
	return false
}

func TestAiConfigEndpointOperationsKeepLegacySelections(t *testing.T) {
	for _, op := range []string{
		gen.CreateAiServiceConfig_Operation,
		gen.UpdateAiServiceConfig_Operation,
		gen.ResolveAiServiceConfigs_Operation,
	} {
		for _, field := range []string{"endpoint", "effectiveEndpoint"} {
			if operationSelects(op, field) {
				t.Fatalf("legacy operation selects %s and would fail on older servers:\n%s", field, op)
			}
		}
	}
	for _, op := range []string{
		gen.CreateAiServiceConfigWithEndpoint_Operation,
		gen.UpdateAiServiceConfigWithEndpoint_Operation,
		gen.AiServiceConfigWithEndpoint_Operation,
		gen.ResolveAiServiceConfigsWithEndpoint_Operation,
	} {
		for _, field := range []string{"endpoint", "effectiveEndpoint"} {
			if !operationSelects(op, field) {
				t.Fatalf("endpoint operation does not select %s:\n%s", field, op)
			}
		}
	}
}

const aiEndpointCfgJSON = `{"id":"cfg1","name":"default","ownerType":"APP","ownerId":"app1",
  "provider":"glm","model":"glm-4","hasApiKey":true,"apiKeyPreview":"…1234",
  "params":null,"endpoint":"https://api.z.ai/api/coding/paas/v4",
  "effectiveEndpoint":"https://api.z.ai/api/coding/paas/v4",
  "enabled":true,"createdAt":"2026-09-27T00:00:00Z","updatedAt":null}`

const aiDefaultCfgJSON = `{"id":"cfg1","name":"default","ownerType":"APP","ownerId":"app1",
  "provider":"glm","model":"glm-4","hasApiKey":true,"apiKeyPreview":"…1234",
  "params":null,"endpoint":null,"effectiveEndpoint":"https://api.z.ai/api/paas/v4",
  "enabled":true,"createdAt":"2026-09-27T00:00:00Z","updatedAt":null}`

func TestAiConfigCreateEndpointFromFlagAndFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flag", []string{"ai-config", "create", "--org", "acme.com", "--name", "default", "--provider", "glm", "--model", "glm-4", "--endpoint", "https://api.z.ai/api/coding/paas/v4"}},
		{"file", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"CreateAiServiceConfigWithEndpoint": `{"data":{"createAiServiceConfig":` + aiEndpointCfgJSON + `}}`,
			})
			args := tc.args
			if tc.name == "file" {
				path := filepath.Join(t.TempDir(), "config.json")
				body := `{"org":"acme.com","name":"default","provider":"glm","model":"glm-4","endpoint":"https://api.z.ai/api/coding/paas/v4"}`
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				args = []string{"ai-config", "create", "--file", path}
			}
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--json", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			vars := unmarshalVars(t, captured["CreateAiServiceConfigWithEndpoint"])
			if vars["endpoint"] != "https://api.z.ai/api/coding/paas/v4" {
				t.Fatalf("endpoint was not sent: %+v", vars)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatal(err)
			}
			if got["endpoint"] != vars["endpoint"] || got["effectiveEndpoint"] != vars["endpoint"] {
				t.Fatalf("configured/effective endpoint missing: %+v", got)
			}
			if _, ok := got["apiKey"]; ok {
				t.Fatal("masked response exposed apiKey")
			}
		})
	}
}

func TestAiConfigCreateEndpointFlagOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"org":"acme.com","name":"default","provider":"glm","model":"glm-4","endpoint":"https://api.z.ai/api/paas/v4"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAiServiceConfigWithEndpoint": `{"data":{"createAiServiceConfig":` + aiEndpointCfgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--file", path, "--endpoint", "https://api.z.ai/api/coding/paas/v4", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	vars := unmarshalVars(t, captured["CreateAiServiceConfigWithEndpoint"])
	if vars["endpoint"] != "https://api.z.ai/api/coding/paas/v4" {
		t.Fatalf("flag did not override file endpoint: %+v", vars)
	}
}

func TestAiConfigCreateExplicitEmptyEndpointUsesEndpointContract(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAiServiceConfigWithEndpoint": `{"data":{"createAiServiceConfig":` + aiDefaultCfgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--org", "acme.com", "--name", "default", "--provider", "glm", "--model", "glm-4", "--endpoint", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	vars := unmarshalVars(t, captured["CreateAiServiceConfigWithEndpoint"])
	if value, present := vars["endpoint"]; !present || value != "" {
		t.Fatalf("explicit empty endpoint was not sent through endpoint operation: %+v", vars)
	}
}

func TestAiConfigUpdateEndpointSetClearAndOmit(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		args        []string
		operation   string
		response    string
	}{
		{"set", "https://api.z.ai/api/coding/paas/v4", []string{"--endpoint", "https://api.z.ai/api/coding/paas/v4"}, "UpdateAiServiceConfigWithEndpoint", aiEndpointCfgJSON},
		{"clear", "", []string{"--endpoint", ""}, "UpdateAiServiceConfigWithEndpoint", aiDefaultCfgJSON},
		{"omit", "", []string{"--model", "glm-4"}, "UpdateAiServiceConfig", aiCfgJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				tc.operation: `{"data":{"updateAiServiceConfig":` + tc.response + `}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"ai-config", "update", "cfg1"}, tc.args...), "--json", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			vars := unmarshalVars(t, captured[tc.operation])
			if tc.name == "omit" {
				if _, ok := vars["endpoint"]; ok {
					t.Fatalf("omitted endpoint became null: %+v", vars)
				}
				return
			}
			if v, ok := vars["endpoint"]; !ok || v != tc.value {
				t.Fatalf("endpoint = %v, present=%v; want %q", v, ok, tc.value)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatal(err)
			}
			if tc.name == "clear" && (got["endpoint"] != nil || got["effectiveEndpoint"] != "https://api.z.ai/api/paas/v4") {
				t.Fatalf("clear did not distinguish stored from effective: %+v", got)
			}
		})
	}
}

func TestAiConfigInspectEndpointsAndSuggestions(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AiServiceConfigWithEndpoint":         `{"data":{"aiServiceConfig":` + aiDefaultCfgJSON + `}}`,
		"ResolveAiServiceConfigsWithEndpoint": `{"data":{"resolveAiServiceConfigs":[` + aiDefaultCfgJSON + `,` + aiEndpointCfgJSON + `]}}`,
		"AiProviderEndpoints":                 `{"data":{"aiProviderEndpoints":[{"url":"https://api.z.ai/api/paas/v4","label":"Z.ai general","isDefault":true,"note":"Pay as you go"},{"url":"https://api.z.ai/api/coding/paas/v4","label":"Z.ai Coding Plan","isDefault":false,"note":"Subscription terms apply"}]}}`,
	})
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"ai-config", "get", "cfg1"}, []string{"configured endpoint: provider default", "effective endpoint: https://api.z.ai/api/paas/v4"}},
		{[]string{"ai-config", "list", "--app", "acme.com:juno-app", "--with-endpoints"}, []string{"STORED ENDPOINT", "EFFECTIVE ENDPOINT", "provider default", "https://api.z.ai/api/coding/paas/v4"}},
		{[]string{"ai-config", "endpoints", "glm"}, []string{"Z.ai Coding Plan", "Subscription terms apply", "Pay as you go"}},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(tc.args, "--server", gql.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v output missing %q: %s", tc.args, want, out.String())
			}
		}
	}
}

func TestAiConfigEndpointJSONShapes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveAiServiceConfigsWithEndpoint": `{"data":{"resolveAiServiceConfigs":[` + aiDefaultCfgJSON + `]}}`,
		"AiProviderEndpoints":                 `{"data":{"aiProviderEndpoints":[]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "list", "--app", "acme.com:juno-app", "--with-endpoints", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["endpoint"] != nil || list[0]["effectiveEndpoint"] != "https://api.z.ai/api/paas/v4" {
		t.Fatalf("configured/effective endpoint JSON: %s", out.String())
	}
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"ai-config", "endpoints", "bedrock", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out2.String()) != "[]" {
		t.Fatalf("empty suggestions must be []: %s", out2.String())
	}
}

func TestAiConfigEndpointCommandsReportOldServer(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"endpoint\" on type \"AiServiceConfig\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	gql, _ := captureGraphQL(t, map[string]string{"AiServiceConfigWithEndpoint": old})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "get", "cfg1", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not support AI config endpoints") {
		t.Fatalf("old-server error = %v", err)
	}
}

func TestAiConfigEndpointValidationKeepsTypedServerMessage(t *testing.T) {
	refusal := `{"errors":[{"message":"endpoint must use https","extensions":{"code":"AiConfigValidationError"}}]}`
	gql, _ := captureGraphQL(t, map[string]string{"UpdateAiServiceConfigWithEndpoint": refusal})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "update", "cfg1", "--endpoint", "http://example.com/v1", "--server", gql.URL})
	err := root.Execute()
	wantExit(t, err, 2)
	if !strings.Contains(err.Error(), "endpoint must use https") {
		t.Fatalf("typed server refusal lost: %v", err)
	}
}
