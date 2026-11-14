package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

const aiURNCfgJSON = `{"id":"cfg1","name":"default","ownerType":"ORGANIZATION","ownerId":"org1",
  "provider":"glm","model":"glm-4","hasApiKey":false,"apiKeyPreview":null,
  "params":null,"endpoint":null,"effectiveEndpoint":"https://api.z.ai/api/paas/v4",
  "enabled":true,"createdAt":"2026-09-27T00:00:00Z","updatedAt":null,
  "urn":"hrn:aiconf:acme.com:org:default"}`

const aiURNMissing = `{"errors":[{"message":"Cannot query field \"urn\" on type \"AiServiceConfig\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

func TestAiConfigUrnOperationsAreSeparateDocuments(t *testing.T) {
	for _, op := range []string{
		gen.AiServiceConfigWithEndpoint_Operation,
		gen.ResolveAiServiceConfigsWithEndpoint_Operation,
		gen.ResolveAiServiceConfigs_Operation,
		gen.CreateAiServiceConfigWithEndpoint_Operation,
		gen.UpdateAiServiceConfigWithEndpoint_Operation,
	} {
		if operationSelects(op, "urn") {
			t.Fatalf("an older-server operation selects urn:\n%s", op)
		}
	}
	for _, op := range []string{
		gen.AiServiceConfigWithUrn_Operation,
		gen.ResolveAiServiceConfigsWithEndpointAndUrn_Operation,
		gen.ResolveAiServiceConfigsWithUrn_Operation,
	} {
		if !operationSelects(op, "urn") {
			t.Fatalf("urn operation does not select urn:\n%s", op)
		}
	}
}

func TestAiConfigGetShowsUrnAndPassesRefThrough(t *testing.T) {
	const ref = "hrn:aiconf:acme.com:org:default"
	gql, captured := captureGraphQL(t, map[string]string{"AiServiceConfigWithUrn": `{"data":{"aiServiceConfig":` + aiURNCfgJSON + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "get", ref, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "urn "+ref) {
		t.Fatalf("text output lacks the urn:\n%s", out.String())
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AiServiceConfigWithUrn"], &vars)
	if vars["ref"] != ref {
		t.Fatalf("ref not passed through untouched: %v", vars)
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"ai-config", "get", "cfg1", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out2.String()), &got); err != nil || got["urn"] != ref {
		t.Fatalf("--json urn = %v (%v)\n%s", got["urn"], err, out2.String())
	}
}

func TestAiConfigNullUrnIsJSONNullAndTextSilent(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AiServiceConfigWithUrn": `{"data":{"aiServiceConfig":` + aiDefaultCfgJSON + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "get", "cfg1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "urn") {
		t.Fatalf("a null urn must print nothing:\n%s", out.String())
	}
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"ai-config", "get", "cfg1", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out2.String()), &got)
	if v, ok := got["urn"]; !ok || string(v) != "null" {
		t.Fatalf("--json must carry urn:null, got %s", out2.String())
	}
}

func TestAiConfigGetUrnMissIsNotFoundWithoutForbidden(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AiServiceConfigWithUrn": `{"data":{"aiServiceConfig":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "get", "hrn:aiconf:acme.com:org:nope", "--server", gql.URL})
	err := root.Execute()
	wantExit(t, err, 4)
	if strings.Contains(strings.ToLower(err.Error()), "forbidden") {
		t.Fatalf("a URN miss must never mention forbidden: %v", err)
	}
}

func TestAiConfigGetIdPathForbiddenIsShownUnchanged(t *testing.T) {
	forbidden := `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}],"data":{"aiServiceConfig":null}}`
	gql, _ := captureGraphQL(t, map[string]string{"AiServiceConfigWithUrn": forbidden})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "get", "cfg1", "--server", gql.URL})
	err := root.Execute()
	if err == nil || strings.Contains(err.Error(), "was not found") {
		t.Fatalf("an id-path Forbidden must not become not-found: %v", err)
	}
	wantExit(t, err, 8)
}

func TestAiConfigOlderServerWithoutUrn(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AiServiceConfigWithUrn":                    aiURNMissing,
		"AiServiceConfigWithEndpoint":               `{"data":{"aiServiceConfig":` + aiDefaultCfgJSON + `}}`,
		"ResolveAiServiceConfigsWithUrn":            aiURNMissing,
		"ResolveAiServiceConfigs":                   aiConfigsJSON,
		"ResolveAiServiceConfigsWithEndpointAndUrn": aiURNMissing,
		"ResolveAiServiceConfigsWithEndpoint":       `{"data":{"resolveAiServiceConfigs":[` + aiDefaultCfgJSON + `]}}`,
	})
	for _, args := range [][]string{
		{"ai-config", "get", "cfg1"},
		{"ai-config", "list", "--app", "acme.com:juno-app"},
		{"ai-config", "list", "--app", "acme.com:juno-app", "--with-endpoints"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, args...), "--json", "--server", gql.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out.String(), `"urn": null`) && !strings.Contains(out.String(), `"urn":null`) {
			t.Fatalf("%v: older server must show urn:null:\n%s", args, out.String())
		}
	}
}

func TestAiConfigOlderServerUrnRefIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AiServiceConfigWithUrn":      aiURNMissing,
		"AiServiceConfigWithEndpoint": `{"data":{"aiServiceConfig":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "get", "hrn:aiconf:acme.com:org:default", "--server", gql.URL})
	wantExit(t, root.Execute(), 4)
}

func TestAiConfigListShowsUrnColumnOnlyWhenPresent(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"ResolveAiServiceConfigsWithEndpointAndUrn": `{"data":{"resolveAiServiceConfigs":[` + aiURNCfgJSON + `]}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "list", "--app", "acme.com:juno-app", "--with-endpoints", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "URN") || !strings.Contains(out.String(), "hrn:aiconf:acme.com:org:default") {
		t.Fatalf("list lacks the urn:\n%s", out.String())
	}
}
