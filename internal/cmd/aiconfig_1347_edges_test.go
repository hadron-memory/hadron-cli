package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestAiConfig1347MaskedItemDoesNotExposeKey(t *testing.T) {
	item := strings.TrimSuffix(aiDefaultCfgJSON, "}") + `,"apiKey":"unselected-secret-1347","apiStyle":"RESPONSES","effectiveApiStyle":"RESPONSES"}`
	gql, _ := captureGraphQL(t, map[string]string{"AiServiceConfigsMasked": `{"data":{"aiServiceConfigs":{"items":[` + item + `],"total":1}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "list", "--owner-type", "HADRON_SERVER", "--owner-id", "hs1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out.String()), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0]["apiStyle"] != "RESPONSES" || page.Items[0]["apiKeyPreview"] != "…1234" {
		t.Fatalf("masked page: %s", out.String())
	}
	if strings.Contains(out.String(), "unselected-secret-1347") {
		t.Fatal("key echoed")
	}
	for _, op := range []string{gen.AiServiceConfigsMasked_Operation, gen.TestAiServiceConfig_Operation} {
		if operationSelects(op, "apiKey") {
			t.Fatal("operation selects raw key")
		}
	}
}

func TestAiConfig1347ProviderOutcomesDoNotRetry(t *testing.T) {
	for _, code := range []string{"NO_API_KEY", "KEY_UNREADABLE", "LLM_CALL_FAILED"} {
		t.Run(code, func(t *testing.T) {
			var calls atomic.Int32
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				if op != "TestAiServiceConfig" {
					return `{"data":{}}`
				}
				calls.Add(1)
				return `{"data":{"testAiServiceConfig":` + strings.ReplaceAll(aiTest1347, "NO_API_KEY", code) + `}}`
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"ai-config", "test", "cfg1", "--server", gql.URL, "--json"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || !strings.Contains(out.String(), code) {
				t.Fatalf("calls=%d output=%s", calls.Load(), out.String())
			}
		})
	}
}

func TestAiConfig1347RefusalsMap(t *testing.T) {
	cases := []struct {
		op   string
		args []string
	}{
		{"AiServiceConfigsMasked", []string{"ai-config", "list", "--all-owners"}},
		{"TestAiServiceConfig", []string{"ai-config", "test", "cfg1"}},
		{"CreateAiServiceConfigWithApiStyle", []string{"ai-config", "create", "--org", "acme.com", "--name", "x", "--provider", "openai", "--model", "m", "--api-style", "RESPONSES"}},
		{"UpdateAiServiceConfigWithApiStyle", []string{"ai-config", "update", "cfg1", "--api-style", "CHAT_COMPLETIONS"}},
		{"ClearAiServiceConfigApiStyle", []string{"ai-config", "update", "cfg1", "--clear-api-style"}},
	}
	for _, tc := range cases {
		for _, ref := range []struct {
			code string
			exit int
		}{{"FORBIDDEN", 8}, {"UNAUTHENTICATED", 3}} {
			t.Run(tc.op+ref.code, func(t *testing.T) {
				gql, _ := captureGraphQL(t, map[string]string{tc.op: fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, ref.code)})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{}, tc.args...), "--server", gql.URL))
				err := root.Execute()
				if err == nil || exitCodeFor(err) != ref.exit {
					t.Fatalf("want exit%d, got %v", ref.exit, err)
				}
			})
		}
	}
}

func TestAiConfig1347OverridePipePreservesNullAndOmitsUnset(t *testing.T) {
	gql, vars := captureGraphQL(t, map[string]string{"TestAiServiceConfig": `{"data":{"testAiServiceConfig":` + aiTest1347 + `}}`})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(`{"apiKey":null,"endpoint":"","params":{"temperature":0}}`)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "test", "cfg1", "--override-file", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	v := unmarshalVars(t, vars["TestAiServiceConfig"])["override"].(map[string]any)
	if val, ok := v["apiKey"]; !ok || val != nil {
		t.Fatal("null key lost")
	}
	if v["endpoint"] != "" {
		t.Fatal("empty endpoint lost")
	}
	if _, ok := v["model"]; ok {
		t.Fatal("omitted model sent")
	}
	if v["params"].(map[string]any)["temperature"] != float64(0) {
		t.Fatal("zero knob lost")
	}
}

func TestAiConfig1347InputErrorsAreLocalAndSecretSafe(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"apiKey":12345}`, `{"apiKey":"secret-1347","wat":true}`, `{"apiKey":"secret-1347","apiStyle":"bad"}`, `{"apiKey":"secret-1347","params":[]}`, `{"apiKey":"secret-1347"}{}`} {
		t.Run(body, func(t *testing.T) {
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader(body)
			root := NewRootCmd(f)
			root.SetArgs([]string{"ai-config", "test", "cfg1", "--override-file", "-", "--server", "http://127.0.0.1:1"})
			err := root.Execute()
			if err == nil || exitCodeFor(err) != 2 {
				t.Fatalf("want local usage, got %v", err)
			}
			if strings.Contains(err.Error()+out.String(), "secret-1347") {
				t.Fatal("invalid document leaked key")
			}
		})
	}
	for _, args := range [][]string{
		{"ai-config", "list", "--owner-id", "app1"}, {"ai-config", "list", "--owner-type", "APP", "--owner-id", ""},
		{"ai-config", "list", "--all-owners", "--owner-type", "APP", "--owner-id", "app1"}, {"ai-config", "list", "--all-owners", "--agent", "agent1"},
		{"ai-config", "update", "cfg1", "--api-style", "bad"}, {"ai-config", "update", "cfg1", "--api-style", "RESPONSES", "--clear-api-style"}, {"ai-config", "update", "cfg1", "--clear-api-style=false"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		err := root.Execute()
		if err == nil || exitCodeFor(err) != 2 {
			t.Fatalf("%v: want usage, got %v", args, err)
		}
	}
}

func TestAiConfig1347StyleFilePrecedenceAndClearWire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, []byte(`{"org":"acme.com","name":"default","provider":"openai","model":"m","apiStyle":"CHAT_COMPLETIONS","endpoint":"https://example.test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{"CreateAiServiceConfigWithApiStyle": `{"data":{"createAiServiceConfig":` + aiDefaultCfgJSON + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--file", path, "--api-style", "RESPONSES", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	v := unmarshalVars(t, captured["CreateAiServiceConfigWithApiStyle"])
	if v["apiStyle"] != "RESPONSES" || v["endpoint"] != "https://example.test" {
		t.Fatalf("flag/file variables: %+v", v)
	}
	if !strings.Contains(gen.ClearAiServiceConfigApiStyle_Operation, "apiStyle: null") {
		t.Fatal("style clear must send literal null")
	}
	for _, op := range []string{gen.CreateAiServiceConfig_Operation, gen.UpdateAiServiceConfig_Operation, gen.ResolveAiServiceConfigs_Operation} {
		if operationSelects(op, "apiStyle") {
			t.Fatal("legacy invocation now needs style fields")
		}
	}
}

func TestAiConfig1347StyleFromFileOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte(`{"org":"acme.com","name":"x","provider":"openai","model":"m","apiStyle":"CHAT_COMPLETIONS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{"CreateAiServiceConfigWithApiStyle": `{"data":{"createAiServiceConfig":` + aiDefaultCfgJSON + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--file", p, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if unmarshalVars(t, captured["CreateAiServiceConfigWithApiStyle"])["apiStyle"] != "CHAT_COMPLETIONS" {
		t.Fatal("file apiStyle ignored")
	}
}

func TestAiConfig1347StyleCombinesOneMutation(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"UpdateAiServiceConfigWithApiStyle": `{"data":{"updateAiServiceConfig":` + aiDefaultCfgJSON + `}}`})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("secret-combined-1347")
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "update", "cfg1", "--api-style", "RESPONSES", "--endpoint", "", "--enabled=false", "--param", "temperature=0", "--api-key", "-", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	v := unmarshalVars(t, captured["UpdateAiServiceConfigWithApiStyle"])
	if v["endpoint"] != "" || v["enabled"] != false || v["apiKey"] != "secret-combined-1347" || v["params"].(map[string]any)["temperature"] != float64(0) {
		t.Fatal("combined style edit lost a field")
	}
	for _, field := range []string{"name", "provider", "model"} {
		if _, ok := v[field]; ok {
			t.Fatalf("unset %s sent", field)
		}
	}
	if strings.Contains(out.String(), "secret-combined-1347") {
		t.Fatal("key echoed")
	}
}
