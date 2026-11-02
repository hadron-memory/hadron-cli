package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAiConfig1347UniformList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"AiServiceConfigsMasked": `{"data":{"aiServiceConfigs":{"items":[],"total":7}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "list", "--owner-type", "APP", "--owner-id", "app1", "--limit", "0", "--offset", "3", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	vars := unmarshalVars(t, captured["AiServiceConfigsMasked"])
	filter, ok := vars["filter"].(map[string]any)
	if !ok || filter["ownerType"] != "APP" || filter["ownerId"] != "app1" || vars["limit"] != float64(0) || vars["offset"] != float64(3) {
		t.Fatalf("variables: %+v", vars)
	}
	var dto struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Items == nil || len(dto.Items) != 0 || dto.Total != 7 {
		t.Fatalf("page: %s", out.String())
	}
}

func TestAiConfig1347AllOwnersOmitsFilter(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"AiServiceConfigsMasked": `{"data":{"aiServiceConfigs":{"items":[],"total":0}}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "list", "--all-owners", "--app", "acme.com:context", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	v := unmarshalVars(t, captured["AiServiceConfigsMasked"])
	if _, ok := v["filter"]; ok {
		t.Fatalf("all-owner view gained filter: %+v", v)
	}
}

const aiTest1347 = `{"ok":false,"provider":"openai","model":"m","endpoint":null,"apiStyle":"RESPONSES","latencyMs":0,"reply":null,"error":{"code":"NO_API_KEY","message":"No API key","transient":false},"used":{"provider":"SAVED","model":"OVERRIDE","endpoint":"SAVED","apiStyle":"OVERRIDE","key":"OVERRIDE","params":"OVERRIDE"},"knobs":[]}`

func TestAiConfig1347TestOverrideAndSaved(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "override"}[override], func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"TestAiServiceConfig": `{"data":{"testAiServiceConfig":` + aiTest1347 + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"ai-config", "test", "cfg1", "--json", "--server", gql.URL}
			if override {
				p := filepath.Join(t.TempDir(), "override.json")
				if err := os.WriteFile(p, []byte(`{"model":"edited","apiStyle":null,"params":null,"apiKey":"secret-1347"}`), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--override-file", p)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			v := unmarshalVars(t, captured["TestAiServiceConfig"])
			if v["id"] != "cfg1" {
				t.Fatalf("id: %+v", v)
			}
			if override {
				o, ok := v["override"].(map[string]any)
				if !ok || o["model"] != "edited" || o["apiKey"] != "secret-1347" {
					t.Fatalf("override not sent")
				}
				for _, key := range []string{"apiStyle", "params"} {
					if val, ok := o[key]; !ok || val != nil {
						t.Fatalf("%s null not preserved", key)
					}
				}
				if _, ok := o["provider"]; ok {
					t.Fatal("unset provider sent")
				}
			} else if _, ok := v["override"]; ok {
				t.Fatal("saved test sent override")
			}
			if strings.Contains(out.String(), "secret-1347") {
				t.Fatal("key echoed")
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["ok"] != false || dto["error"].(map[string]any)["code"] != "NO_API_KEY" {
				t.Fatalf("test outcome lost: %s", out.String())
			}
		})
	}
}

func TestAiConfig1347APIStyleWrites(t *testing.T) {
	for _, tc := range []struct {
		name, op, field string
		args            []string
		clear           bool
	}{
		{"create", "CreateAiServiceConfigWithApiStyle", "createAiServiceConfig", []string{"ai-config", "create", "--org", "acme.com", "--name", "default", "--provider", "openai", "--model", "m", "--api-style", "CHAT_COMPLETIONS"}, false},
		{"update", "UpdateAiServiceConfigWithApiStyle", "updateAiServiceConfig", []string{"ai-config", "update", "cfg1", "--api-style", "RESPONSES"}, false},
		{"clear", "ClearAiServiceConfigApiStyle", "updateAiServiceConfig", []string{"ai-config", "update", "cfg1", "--clear-api-style"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + aiDefaultCfgJSON + `}}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			v := unmarshalVars(t, captured[tc.op])
			if tc.clear {
				if _, ok := v["apiStyle"]; ok {
					t.Fatalf("clear operation must use literal null, not variable: %+v", v)
				}
			} else if v["apiStyle"] == nil {
				t.Fatal("style absent")
			}
			if _, ok := v["endpoint"]; ok {
				t.Fatal("omitted endpoint was sent")
			}
		})
	}
}
