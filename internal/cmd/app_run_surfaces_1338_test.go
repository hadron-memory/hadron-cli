package cmd

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const grant1338 = `{"id":"g1","appId":"a1","memoryUrn":"hrn:mem:acme.com:notes","prefix":"reports","registerNodeUrn":null,"grantedBy":"u1","createdAt":"2026-10-31T00:00:00Z","revokedAt":null,"revokedBy":null}`
const app1338 = `{"id":"a1","urn":"hrn:app:acme.com:team","name":"Team","appType":"TEAM","description":null}`

type surfaceCase1338 struct {
	name, op, response string
	args               []string
	vars               map[string]any
	absent             []string
}

func surfaceCases1338() []surfaceCase1338 {
	return []surfaceCase1338{
		{"grant add", "SetAppMemoryWriteGrant", `{"setAppMemoryWriteGrant":` + grant1338 + `}`, []string{"app", "memory-grant", "add", "acme.com:team", "--memory", "acme.com:notes", "--prefix", "reports", "--yes"}, map[string]any{"appRef": "hrn:app:acme.com:team", "memoryRef": "hrn:mem:acme.com:notes", "prefix": "reports"}, []string{"registerNodeUrn"}},
		{"grant register", "SetAppMemoryWriteGrant", `{"setAppMemoryWriteGrant":` + grant1338 + `}`, []string{"app", "memory-grant", "add", "acme.com:team", "--memory", "acme.com:notes", "--prefix", "reports", "--register-node", "hrn:node:acme.com:notes:register:1", "--yes"}, map[string]any{"appRef": "hrn:app:acme.com:team", "memoryRef": "hrn:mem:acme.com:notes", "prefix": "reports", "registerNodeUrn": "hrn:node:acme.com:notes:register:1"}, nil},
		{"grant list", "AppMemoryWriteGrants", `{"appMemoryWriteGrants":[` + grant1338 + `]}`, []string{"app", "memory-grant", "list", "acme.com:team"}, map[string]any{"appRef": "hrn:app:acme.com:team"}, []string{"includeRevoked"}},
		{"revoked true", "AppMemoryWriteGrants", `{"appMemoryWriteGrants":[]}`, []string{"app", "memory-grant", "list", "acme.com:team", "--include-revoked"}, map[string]any{"appRef": "hrn:app:acme.com:team", "includeRevoked": true}, nil},
		{"revoked false", "AppMemoryWriteGrants", `{"appMemoryWriteGrants":[]}`, []string{"app", "memory-grant", "list", "acme.com:team", "--include-revoked=false"}, map[string]any{"appRef": "hrn:app:acme.com:team", "includeRevoked": false}, nil},
		{"grant rm", "RevokeAppMemoryWriteGrant", `{"revokeAppMemoryWriteGrant":` + strings.Replace(grant1338, `"revokedAt":null`, `"revokedAt":"2026-10-31T01:00:00Z"`, 1) + `}`, []string{"app", "memory-grant", "rm", "g1", "--yes"}, map[string]any{"id": "g1"}, nil},
		{"latest", "LatestAppRun", `{"latestAppRun":` + appRunJSON + `}`, []string{"run", "latest", "acme.com:team"}, map[string]any{"appRef": "hrn:app:acme.com:team"}, []string{"previous"}},
		{"previous", "LatestAppRun", `{"latestAppRun":` + appRunJSON + `}`, []string{"run", "latest", "acme.com:team", "--previous"}, map[string]any{"appRef": "hrn:app:acme.com:team", "previous": true}, nil},
		{"previous false", "LatestAppRun", `{"latestAppRun":` + appRunJSON + `}`, []string{"run", "latest", "acme.com:team", "--previous=false"}, map[string]any{"appRef": "hrn:app:acme.com:team", "previous": false}, nil},
		{"whoami", "MyApp", `{"myApp":` + app1338 + `}`, []string{"app", "whoami"}, map[string]any{}, nil},
		{"enable", "SetAgentAssetUploadEnabled", `{"setAgentAssetUploadEnabled":{"id":"a1","urn":"hrn:app:acme.com:team","agentTools":["asset:upload"]}}`, []string{"app", "asset-upload", "enable", "hrn:agent:acme.com:bot", "--yes"}, map[string]any{"input": map[string]any{"agentUrn": "hrn:agent:acme.com:bot", "enabled": true}}, nil},
		{"disable", "SetAgentAssetUploadEnabled", `{"setAgentAssetUploadEnabled":{"id":"a1","urn":"hrn:app:acme.com:team","agentTools":[]}}`, []string{"app", "asset-upload", "disable", "hrn:agent:acme.com:bot", "--yes"}, map[string]any{"input": map[string]any{"agentUrn": "hrn:agent:acme.com:bot", "enabled": false}}, nil},
	}
}

func TestAppRunSurfaces1338WireAndDTO(t *testing.T) {
	for _, tc := range surfaceCases1338() {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := captureGraphQL(t, map[string]string{tc.op: `{"data":` + tc.response + `}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, tc.args...), "--json", "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			raw, ok := captured[tc.op]
			if !ok {
				t.Fatalf("missing %s", tc.op)
			}
			vars := map[string]any{}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &vars); err != nil {
					t.Fatal(err)
				}
			}
			for k, want := range tc.vars {
				if !reflect.DeepEqual(vars[k], want) {
					t.Errorf("%s=%v want%v", k, vars[k], want)
				}
			}
			for _, k := range append(tc.absent, "session", "sessionId") {
				if _, ok := vars[k]; ok {
					t.Errorf("unexpected %s: %s", k, raw)
				}
			}
			var value any
			if err := json.Unmarshal([]byte(out.String()), &value); err != nil {
				t.Fatal(err)
			}
			switch tc.op {
			case "AppMemoryWriteGrants":
				if value == nil {
					t.Fatal("null list")
				}
				if _, ok := value.([]any); !ok {
					t.Fatalf("list DTO=%v", value)
				}
			case "MyApp":
				if value.(map[string]any)["app"].(map[string]any)["id"] != "a1" {
					t.Fatal(value)
				}
			case "LatestAppRun":
				if value.(map[string]any)["id"] != "run1" {
					t.Fatal(value)
				}
			case "SetAgentAssetUploadEnabled":
				d := value.(map[string]any)
				if d["appId"] != "a1" || d["enabled"] != (tc.name == "enable") {
					t.Fatal(value)
				}
			default:
				d := value.(map[string]any)
				if d["id"] != "g1" || d["prefix"] != "reports" {
					t.Fatal(value)
				}
				if _, ok := d["memoryUrn"]; !ok {
					t.Fatal(value)
				}
			}
		})
	}
}

func TestAppRunSurfaces1338NullAndRefusal(t *testing.T) {
	for _, tc := range surfaceCases1338() {
		t.Run(tc.name, func(t *testing.T) {
			for _, refusal := range []struct {
				code string
				exit int
			}{{"FORBIDDEN", 8}, {"UNAUTHENTICATED", 3}, {"AVAILABILITY_NOT_PERMITTED", 8}} {
				calls, out, err := runTeamTask(t, map[string]string{tc.op: `{"errors":[{"message":"refused","extensions":{"code":"` + refusal.code + `"}}]}`}, append(tc.args, "--json")...)
				var coded interface{ ServerCode() string }
				if exitCodeFor(err) != refusal.exit || !errors.As(err, &coded) || coded.ServerCode() != refusal.code || out != "" || len(*calls) != 1 || (*calls)[0].Op != tc.op {
					t.Fatalf("code=%s err=%v out=%s calls=%v", refusal.code, err, out, *calls)
				}
			}
		})
	}
	calls, out, err := runTeamTask(t, map[string]string{"MyApp": `{"data":{"myApp":null}}`}, "app", "whoami", "--json", "--app", "acme.com:other")
	if err != nil || strings.TrimSpace(out) != `{"app":null}` && strings.Join(strings.Fields(out), "") != `{"app":null}` || len(*calls) != 1 {
		t.Fatalf("null must not fall back: %v %s %v", err, out, *calls)
	}
	_, out, err = runTeamTask(t, map[string]string{"LatestAppRun": `{"data":{"latestAppRun":null}}`}, "run", "latest", "acme.com:team", "--json")
	if exitCodeFor(err) != 4 || out != "" {
		t.Fatalf("latest null: %v %s", err, out)
	}
}

func TestAppRunSurfaces1338UsageAndConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"app", "memory-grant", "add", "acme.com:team", "--memory", "acme.com:notes", "--prefix", "reports"},
		{"app", "memory-grant", "rm", "g1"},
		{"app", "asset-upload", "enable", "hrn:agent:acme.com:bot"},
		{"app", "asset-upload", "disable", "hrn:agent:acme.com:bot"},
		{"app", "memory-grant", "add", "acme.com:team", "--memory=", "--prefix", "reports", "--yes"},
		{"app", "memory-grant", "add", "acme.com:team", "--memory", "acme.com:notes", "--prefix=", "--yes"},
		{"app", "memory-grant", "add", "acme.com:team", "--memory", "acme.com:notes", "--prefix", "reports", "--register-node=", "--yes"},
		{"app", "memory-grant", "rm", "", "--yes"},
		{"app", "asset-upload", "enable", "", "--yes"},
	} {
		calls, _, err := runTeamTask(t, nil, args...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("args=%v err=%v calls=%v", args, err, *calls)
		}
	}
}

func TestAppRunSurfaces1338MalformedAndNullableResults(t *testing.T) {
	cases := surfaceCases1338()
	for _, tc := range cases {
		if tc.name != "grant add" && tc.name != "grant list" && tc.name != "grant rm" && tc.name != "enable" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			var original map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.response), &original); err != nil {
				t.Fatal(err)
			}
			for key := range original {
				original[key] = json.RawMessage(`null`)
			}
			response, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			calls, out, err := runTeamTask(t, map[string]string{tc.op: `{"data":` + string(response) + `}`}, append(tc.args, "--json")...)
			if exitCodeFor(err) != 1 || out != "" || len(*calls) != 1 {
				t.Fatalf("malformed accepted: %v %s %v", err, out, *calls)
			}
		})
	}
	calls, out, err := runTeamTask(t, map[string]string{"AppMemoryWriteGrants": `{"data":{"appMemoryWriteGrants":[` + strings.Replace(grant1338, `"memoryUrn":"hrn:mem:acme.com:notes"`, `"memoryUrn":null`, 1) + `]}}`}, "app", "memory-grant", "list", "acme.com:team", "--json")
	if err != nil || len(*calls) != 1 {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["memoryUrn"] != nil {
		t.Fatalf("nullable memory backfilled: %s", out)
	}
	for _, enabled := range []bool{false, true} {
		tools := `[]`
		verb := "enable"
		if enabled {
			tools = `["asset:upload"]`
			verb = "disable"
		}
		_, out, err := runTeamTask(t, map[string]string{"SetAgentAssetUploadEnabled": `{"data":{"setAgentAssetUploadEnabled":{"id":"a1","urn":"hrn:app:acme.com:team","agentTools":` + tools + `}}}`}, "app", "asset-upload", verb, "hrn:agent:acme.com:bot", "--yes", "--json")
		if exitCodeFor(err) != 1 || out != "" {
			t.Fatalf("setting mismatch forged success: %v %s", err, out)
		}
	}
}
