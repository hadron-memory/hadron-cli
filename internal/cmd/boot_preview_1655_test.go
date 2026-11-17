package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func bootResponses1655(t *testing.T, outcome string) map[string]string {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(appRosterResp), &p); err != nil {
		t.Fatal(err)
	}
	app := p["data"].(map[string]any)["app"].(map[string]any)
	var serve any
	if err := json.Unmarshal([]byte(outcome), &serve); err != nil {
		t.Fatal(err)
	}
	app["appAgents"] = []any{map[string]any{"agent": map[string]any{"id": "agt9"}, "installMethod": "ROW", "bootPromptRef": nil, "bootPromptTargetDeleted": nil, "bootPromptServe": serve}}
	wire, _ := json.Marshal(p)
	return map[string]string{"AppAgentInstallationRoster": string(wire), "AppAgentEffectiveRoster": string(wire), "GetWorker": `{"data":{"worker":` + profileWorkerJSON + `}}`, "GetWorkerBootPromptServe": `{"data":{"worker":{"bootPromptServe":` + outcome + `}}}`}
}

func TestEffectiveBoot1655(t *testing.T) {
	for _, surface := range []string{"app", "worker"} {
		for _, level := range []string{"AGENT", "APP_AGENT", "WORKER"} {
			if surface == "app" && level == "WORKER" {
				continue
			}
			for _, asJSON := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json%t", surface, level, asJSON), func(t *testing.T) {
					outcome := fmt.Sprintf(`{"served":{"pointerLevel":%q,"compiledNodeId":"compiled1","servedRevSeq":7},"refusal":null}`, level)
					args := []string{"app", "agent", "list", "hrn:app:acme.com:support"}
					if surface == "worker" {
						args = []string{"team", "worker", "get", profileHexID}
					}
					if asJSON {
						args = append(args, "--json")
					}
					calls, out, err := runTeamTask(t, bootResponses1655(t, outcome), args...)
					if err != nil {
						t.Fatal(err)
					}
					if asJSON {
						var row map[string]any
						if surface == "app" {
							var rows []map[string]any
							if err := json.Unmarshal([]byte(out), &rows); err != nil {
								t.Fatal(err)
							}
							if len(rows) != 1 {
								t.Fatal(out)
							}
							row = rows[0]
						} else if err := json.Unmarshal([]byte(out), &row); err != nil {
							t.Fatal(err)
						}
						if row["pointerLevel"] != level || row["compiledNodeId"] != "compiled1" || row["servedRevSeq"] != float64(7) {
							t.Fatalf("served projection missing: %s", out)
						}
						if row["bootPromptRef"] != nil {
							t.Fatalf("invented inherited pointer: %s", out)
						}
					} else if !strings.Contains(out, level) || !strings.Contains(out, "compiled1") || !strings.Contains(out, "next bind") || strings.Contains(out, "unset") {
						t.Fatalf("effective label missing: %s", out)
					}
					for _, c := range *calls {
						if c.Op == "GetNode" || c.Op == "NodeBatch" || c.Op == "GetAgent" {
							t.Fatalf("lookup beyond projection: %s", c.Op)
						}
					}
				})
			}
		}
	}
}

func TestEffectiveBootRefusals1655(t *testing.T) {
	for _, tc := range []struct{ name, outcome, want string }{
		{"none", `{"served":null,"refusal":"BOOT_PROMPT_NOT_SET"}`, "none"},
		{"unapproved", `{"served":null,"refusal":"BOOT_PROMPT_NOT_APPROVED"}`, "BOOT_PROMPT_NOT_APPROVED"},
		{"deleted", `{"served":null,"refusal":"BOOT_PROMPT_TARGET_DELETED"}`, "BOOT_PROMPT_TARGET_DELETED"},
		{"denied", `null`, "not visible"},
		{"malformed", `{"served":{"pointerLevel":"AGENT","compiledNodeId":"secret","servedRevSeq":1},"refusal":"BOOT_PROMPT_NOT_SET"}`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := runTeamTask(t, bootResponses1655(t, tc.outcome), "app", "agent", "list", "hrn:app:acme.com:support")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) || strings.Contains(out, "secret") {
				t.Fatalf("label: %s", out)
			}
			_, out, err = runTeamTask(t, bootResponses1655(t, tc.outcome), "app", "agent", "list", "hrn:app:acme.com:support", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err = json.Unmarshal([]byte(out), &rows); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"pointerLevel", "compiledNodeId", "servedRevSeq"} {
				if v, exists := rows[0][k]; !exists || v != nil {
					t.Fatalf("refusal/redaction leaks or missing null %s: %s", k, out)
				}
			}
		})
	}
}

func TestEffectiveBootFallback1655(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		fallback    bool
	}{
		{"old", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, true},
		{"auth", `{"errors":[{"message":"denied","extensions":{"code":"UNAUTHENTICATED"}}]}`, false},
		{"wrong-type", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Agent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
		{"other-field", `{"errors":[{"message":"Cannot query field \"other\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
		{"partial", `{"data":{"app":null},"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bootResponses1655(t, `null`)
			r["AppAgentEffectiveRoster"] = tc.reply
			calls, out, err := runTeamTask(t, r, "app", "agent", "list", "hrn:app:acme.com:support")
			if (err == nil) != tc.fallback {
				t.Fatalf("fallback=%t err=%v output=%s", tc.fallback, err, out)
			}
			modern, legacy := 0, 0
			for _, c := range *calls {
				if c.Op == "AppAgentEffectiveRoster" {
					modern++
				}
				if c.Op == "AppAgentInstallationRoster" {
					legacy++
				}
			}
			if modern != 1 || legacy != map[bool]int{true: 1, false: 0}[tc.fallback] {
				t.Fatalf("requests modern%d legacy%d", modern, legacy)
			}
			if tc.fallback && (!strings.Contains(out, "unset or not visible") || strings.Contains(out, "none")) {
				t.Fatalf("legacy inferred none: %s", out)
			}
		})
	}
}

func TestEffectiveBootActualPre2708Refuses1655(t *testing.T) {
	body, err := os.ReadFile("testdata/boot_preview_pre2708_1655.json")
	if err != nil {
		t.Fatal(err)
	}
	r := bootResponses1655(t, `null`)
	r["AppAgentEffectiveRoster"] = string(body)
	calls, out, err := runTeamTask(t, r, "app", "agent", "list", "hrn:app:acme.com:support", "--json")
	if err == nil || out != "" {
		t.Fatalf("old mixed schema accepted: %s %v", out, err)
	}
	for _, c := range *calls {
		if c.Op == "AppAgentInstallationRoster" {
			t.Fatal("mixed schema fallback")
		}
	}
}

func TestWorkerBootRefusalAndCompatibility1655(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		ok          bool
		refusal     string
	}{
		{"denied", `{"data":{"worker":{"bootPromptServe":null}}}`, true, ""},
		{"none", `{"data":{"worker":{"bootPromptServe":{"served":null,"refusal":"BOOT_PROMPT_NOT_SET"}}}}`, true, "BOOT_PROMPT_NOT_SET"},
		{"unapproved", `{"data":{"worker":{"bootPromptServe":{"served":null,"refusal":"BOOT_PROMPT_NOT_APPROVED"}}}}`, true, "BOOT_PROMPT_NOT_APPROVED"},
		{"old", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\". Did you mean \"bootPromptRef\"?","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, true, ""},
		{"auth", `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, false, ""},
		{"mixed", `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, false, ""},
		{"partial", `{"data":{"worker":null},"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bootResponses1655(t, `null`)
			r["GetWorkerBootPromptServe"] = tc.reply
			calls, out, err := runTeamTask(t, r, "team", "worker", "get", profileHexID, "--json")
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v out=%s", err, out)
			}
			modern := 0
			for _, c := range *calls {
				if c.Op == "GetWorkerBootPromptServe" {
					modern++
				}
				if c.Op == "GetNode" || c.Op == "Workers" || c.Op == "GetAgent" {
					t.Fatalf("alternate lookup %s", c.Op)
				}
			}
			if modern != 1 {
				t.Fatal(modern)
			}
			if !tc.ok {
				if out != "" {
					t.Fatal(out)
				}
				return
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"pointerLevel", "compiledNodeId", "servedRevSeq"} {
				if v, ok := dto[k]; !ok || v != nil {
					t.Fatalf("unknown/refusal leaks %s: %s", k, out)
				}
			}
			if tc.refusal != "" && dto["bootPromptRefusal"] != tc.refusal {
				t.Fatal(out)
			}
		})
	}
}

func TestEffectiveBootLegacyErrorNoLoop1655(t *testing.T) {
	r := bootResponses1655(t, `null`)
	r["AppAgentEffectiveRoster"] = `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	r["AppAgentInstallationRoster"] = `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`
	calls, out, err := runTeamTask(t, r, "app", "agent", "list", "hrn:app:acme.com:support", "--json")
	if err == nil || out != "" || exitCodeFor(err) != 8 {
		t.Fatalf("legacy error hidden: out%s err%v", out, err)
	}
	modern, legacy := 0, 0
	for _, c := range *calls {
		if c.Op == "AppAgentEffectiveRoster" {
			modern++
		}
		if c.Op == "AppAgentInstallationRoster" {
			legacy++
		}
	}
	if modern != 1 || legacy != 1 {
		t.Fatalf("retry loop modern%d legacy%d", modern, legacy)
	}
}

// Modern provenance pins must opt in; compatibility defaults intentionally model
// an old missing-capability provider and cannot prove this branch's join logic.
func TestEffectiveBootModernProvenance1655(t *testing.T) {
	for _, withheld := range []bool{false, true} {
		t.Run(fmt.Sprint(withheld), func(t *testing.T) {
			responses := bootResponses1655(t, `{"served":{"pointerLevel":"APP_AGENT","compiledNodeId":"n1","servedRevSeq":7},"refusal":null}`)
			var p map[string]any
			if err := json.Unmarshal([]byte(responses["AppAgentEffectiveRoster"]), &p); err != nil {
				t.Fatal(err)
			}
			app := p["data"].(map[string]any)["app"].(map[string]any)
			agents := app["agents"].([]any)
			copyAgent := map[string]any{}
			for k, v := range agents[0].(map[string]any) {
				copyAgent[k] = v
			}
			copyAgent["id"] = "agt10"
			copyAgent["urn"] = "hrn:agent:example.com:derived"
			copyAgent["name"] = "Derived"
			app["agents"] = append(agents, copyAgent)
			own := "hrn:node:example.com:install:boot"
			base := app["appAgents"].([]any)[0].(map[string]any)
			base["bootPromptRef"] = own
			importer := map[string]any{"id": "agt9", "urn": "hrn:agent:acme.com:support-bot", "name": "Support Bot"}
			second := map[string]any{"agent": map[string]any{"id": "agt10"}, "installMethod": "DERIVED", "importer": importer, "bootPromptRef": nil, "bootPromptTargetDeleted": nil, "bootPromptServe": map[string]any{"served": map[string]any{"pointerLevel": "AGENT", "compiledNodeId": "n2", "servedRevSeq": 8}, "refusal": nil}}
			rows := []any{second, base} // ordering differs from App.agents: join by id.
			if withheld {
				base["installMethod"] = nil
				base["importer"] = nil
				second["installMethod"] = nil
				second["importer"] = nil
				rows = append(rows, map[string]any{"agent": nil, "installMethod": nil, "importer": nil, "bootPromptServe": map[string]any{"served": map[string]any{"pointerLevel": "AGENT", "compiledNodeId": "HIDDEN_COMPILED", "servedRevSeq": 8}}})
			}
			app["appAgents"] = rows
			wire, _ := json.Marshal(p)
			responses["AppAgentEffectiveRoster"] = string(wire)
			calls, out, err := runTeamTask(t, responses, "app", "agent", "list", "hrn:app:acme.com:support", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got []map[string]any
			if err = json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 {
				t.Fatalf("%s %v", out, err)
			}
			if strings.Contains(out, "HIDDEN_COMPILED") {
				t.Fatal("hidden Agent row surfaced")
			}
			if got[0]["bootPromptUrn"] != own || got[0]["pointerLevel"] != "APP_AGENT" || got[1]["pointerLevel"] != "AGENT" {
				t.Fatal(out)
			}
			if _, ok := got[1]["bootPromptUrn"]; ok {
				t.Fatal("inherited URN invented")
			}
			if withheld {
				for _, row := range got {
					if row["installMethod"] != nil || row["importer"] != nil {
						t.Fatal(out)
					}
				}
			} else {
				if got[0]["installMethod"] != "ROW" || got[0]["importer"] != nil || got[1]["installMethod"] != "DERIVED" {
					t.Fatal(out)
				}
				imp, ok := got[1]["importer"].(map[string]any)
				if !ok || imp["id"] != "agt9" || imp["urn"] != importer["urn"] || imp["name"] != "Support Bot" {
					t.Fatal(out)
				}
			}
			modern, legacy := 0, 0
			for _, c := range *calls {
				if c.Op == "AppAgentEffectiveRoster" {
					modern++
				}
				if c.Op == "AppAgentInstallationRoster" {
					legacy++
				}
			}
			if modern != 1 || legacy != 0 {
				t.Fatalf("modern%d legacy%d", modern, legacy)
			}
		})
	}
}

func TestEffectiveBootModernDeletedCue1655(t *testing.T) {
	r := bootResponses1655(t, `{"served":null,"refusal":"BOOT_PROMPT_TARGET_DELETED"}`)
	var p map[string]any
	_ = json.Unmarshal([]byte(r["AppAgentEffectiveRoster"]), &p)
	row := p["data"].(map[string]any)["app"].(map[string]any)["appAgents"].([]any)[0].(map[string]any)
	row["bootPromptRef"] = "hrn:node:example.com:install:boot"
	row["bootPromptTargetDeleted"] = true
	wire, _ := json.Marshal(p)
	r["AppAgentEffectiveRoster"] = string(wire)
	_, out, err := runTeamTask(t, r, "app", "agent", "list", "hrn:app:acme.com:support")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "BOOT_PROMPT_TARGET_DELETED") || !strings.Contains(out, "deleted: clear or repoint") {
		t.Fatal(out)
	}
	_, out, err = runTeamTask(t, r, "app", "agent", "list", "hrn:app:acme.com:support", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if got[0]["bootPromptTargetDeleted"] != true || got[0]["pointerLevel"] != nil || got[0]["bootPromptRefusal"] != "BOOT_PROMPT_TARGET_DELETED" {
		t.Fatal(out)
	}
	if _, ok := got[0]["bootPromptUrn"]; ok {
		t.Fatal("refusal gained a served URN")
	}
}

func TestWorkerBootOwnPointerSameRead1655(t *testing.T) {
	r := bootResponses1655(t, `{"served":{"pointerLevel":"WORKER","compiledNodeId":"new-node","servedRevSeq":2},"refusal":null}`)
	old := "hrn:node:example.com:old:boot"
	fresh := "hrn:node:example.com:new:boot"
	r["GetWorker"] = strings.Replace(r["GetWorker"], `"bootPromptRef":null`, `"bootPromptRef":"`+old+`"`, 1)
	r["GetWorkerBootPromptServe"] = `{"data":{"worker":{"bootPromptRef":"` + fresh + `","bootPromptTargetDeleted":false,"bootPromptServe":{"served":{"pointerLevel":"WORKER","compiledNodeId":"new-node","servedRevSeq":2},"refusal":null}}}}`
	_, out, err := runTeamTask(t, r, "team", "worker", "get", profileHexID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if got["bootPromptUrn"] != fresh || got["bootPromptRef"] != fresh || strings.Contains(out, old) {
		t.Fatalf("stale own ref associated with new decision: %s", out)
	}
}

func TestWorkerBootModernDeletedCueSafe1655(t *testing.T) {
	r := bootResponses1655(t, `{"served":null,"refusal":"BOOT_PROMPT_TARGET_DELETED"}`)
	ref := "hrn:node:example.com:kb:boot\x1b[31m\nFORGED_ROW"
	encoded, _ := json.Marshal(ref)
	var base map[string]any
	_ = json.Unmarshal([]byte(r["GetWorker"]), &base)
	w := base["data"].(map[string]any)["worker"].(map[string]any)
	w["bootPromptRef"] = ref
	w["bootPromptTargetDeleted"] = true
	wire, _ := json.Marshal(base)
	r["GetWorker"] = string(wire)
	r["GetWorkerBootPromptServe"] = `{"data":{"worker":{"bootPromptRef":` + string(encoded) + `,"bootPromptTargetDeleted":true,"bootPromptServe":{"served":null,"refusal":"BOOT_PROMPT_TARGET_DELETED"}}}}`
	_, out, err := runTeamTask(t, r, "team", "worker", "get", profileHexID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\nFORGED_ROW") || !strings.Contains(out, "deleted: clear or repoint") {
		t.Fatalf("unsafe/missing cue: %q", out)
	}
	_, out, err = runTeamTask(t, r, "team", "worker", "get", profileHexID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out), &dto)
	if dto["bootPromptRef"] != ref || dto["bootPromptTargetDeleted"] != true {
		t.Fatal("raw JSON own pointer changed")
	}
}

// The gate accepts absent and null data equally at both validation statuses;
// every nonnull shape is a partial result and cannot choose a legacy read.
func TestEffectiveBootValidationParity1655(t *testing.T) {
	for _, status := range []int{200, 400} {
		for _, data := range []string{"", `,"data":null`, `,"data":{}`, `,"data":{"app":null}`, `,"data":[]`, `,"data":false`} {
			t.Run(fmt.Sprintf("%d/%s", status, data), func(t *testing.T) {
				var modern, legacy atomic.Int32
				response := bootResponses1655(t, `null`)["AppAgentInstallationRoster"]
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						OperationName string `json:"operationName"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					w.Header().Set("Content-Type", "application/json")
					switch req.OperationName {
					case "AppAgentEffectiveRoster":
						modern.Add(1)
						w.WriteHeader(status)
						fmt.Fprint(w, `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]`+data+`}`)
					case "AppAgentInstallationRoster":
						legacy.Add(1)
						fmt.Fprint(w, response)
					default:
						t.Errorf("unexpected operation %s", req.OperationName)
						fmt.Fprint(w, `{"data":null}`)
					}
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs([]string{"app", "agent", "list", "hrn:app:acme.com:support", "--server", srv.URL, "--json"})
				err := root.Execute()
				want := data == "" || data == `,"data":null`
				if (err == nil) != want || modern.Load() != 1 || legacy.Load() != map[bool]int32{true: 1, false: 0}[want] {
					t.Fatalf("err=%v modern%d legacy%d output%s", err, modern.Load(), legacy.Load(), out.String())
				}
				if !want && out.Len() != 0 {
					t.Fatal("partial output", out.String())
				}
			})
		}
	}
}

func TestEffectiveBootMalformedWireFails1655(t *testing.T) {
	for _, surface := range []string{"app", "worker"} {
		t.Run(surface, func(t *testing.T) {
			r := bootResponses1655(t, `{"served":{"pointerLevel":"AGENT","compiledNodeId":"n1","servedRevSeq":1.5},"refusal":null}`)
			args := []string{"app", "agent", "list", "hrn:app:acme.com:support", "--json"}
			if surface == "worker" {
				args = []string{"team", "worker", "get", profileHexID, "--json"}
			}
			calls, out, err := runTeamTask(t, r, args...)
			if err == nil || out != "" {
				t.Fatalf("typed malformed value accepted err%v output%s", err, out)
			}
			for _, c := range *calls {
				if c.Op == "AppAgentInstallationRoster" {
					t.Fatal("typed decode error selected legacy")
				}
			}
		})
	}
}

func TestEffectiveBootModernWithheldRoster1655(t *testing.T) {
	for _, field := range []string{"agents", "appAgents"} {
		t.Run(field, func(t *testing.T) {
			r := bootResponses1655(t, `{"served":{"pointerLevel":"AGENT","compiledNodeId":"WITHHELD_ID","servedRevSeq":1}}`)
			var p map[string]any
			_ = json.Unmarshal([]byte(r["AppAgentEffectiveRoster"]), &p)
			p["data"].(map[string]any)["app"].(map[string]any)[field] = nil
			wire, _ := json.Marshal(p)
			r["AppAgentEffectiveRoster"] = string(wire)
			calls, out, err := runTeamTask(t, r, "app", "agent", "list", "hrn:app:acme.com:support", "--json")
			if err != nil || strings.TrimSpace(out) != "null" {
				t.Fatalf("out%s err%v", out, err)
			}
			modern, legacy := 0, 0
			for _, c := range *calls {
				if c.Op == "AppAgentEffectiveRoster" {
					modern++
				}
				if c.Op == "AppAgentInstallationRoster" {
					legacy++
				}
			}
			if modern != 1 || legacy != 0 {
				t.Fatalf("modern%d legacy%d", modern, legacy)
			}
		})
	}
}

func TestEffectiveBootNullErrorsRefuse1655(t *testing.T) {
	const valid = `{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	for _, status := range []int{200, 400} {
		for _, entries := range []string{`null,` + valid, valid + `,null`, `null`, `{}`, `"not-an-error",` + valid, `42,` + valid} {
			t.Run(fmt.Sprintf("%d/%s", status, entries), func(t *testing.T) {
				var modern, legacy atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						OperationName string `json:"operationName"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					w.Header().Set("Content-Type", "application/json")
					switch req.OperationName {
					case "AppAgentEffectiveRoster":
						modern.Add(1)
						w.WriteHeader(status)
						fmt.Fprint(w, `{"errors":[`+entries+`]}`)
					case "AppAgentInstallationRoster":
						legacy.Add(1)
						fmt.Fprint(w, appRosterResp)
					default:
						t.Errorf("unexpected operation %s", req.OperationName)
						fmt.Fprint(w, `{"data":null}`)
					}
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs([]string{"app", "agent", "list", "hrn:app:acme.com:support", "--server", srv.URL, "--json"})
				err := root.Execute()
				if err == nil || out.Len() != 0 || modern.Load() != 1 || legacy.Load() != 0 {
					t.Fatalf("err%v output%s modern%d legacy%d", err, out.String(), modern.Load(), legacy.Load())
				}
			})
		}
	}
}

func TestWorkerBootValidationParity1655(t *testing.T) {
	const valid = `{"message":"Cannot query field \"bootPromptServe\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	for _, status := range []int{200, 400} {
		for _, tc := range []struct {
			name, body string
			ok         bool
		}{
			{"absent", `{"errors":[` + valid + `]}`, true},
			{"null", `{"data":null,"errors":[` + valid + `]}`, true},
			{"object", `{"data":{},"errors":[` + valid + `]}`, false},
			{"partial", `{"data":{"worker":null},"errors":[` + valid + `]}`, false},
			{"null-error", `{"errors":[null,` + valid + `]}`, false},
			{"mixed", `{"errors":[` + valid + `,{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, false},
		} {
			t.Run(fmt.Sprintf("%d/%s", status, tc.name), func(t *testing.T) {
				// Six existing worker-profile reads (cli#1911 added availability) plus one preview; no fanout.
				const existingWorkerReads = 6
				const previewReads = 1
				var lookup, preview, total atomic.Int32
				counts := map[string]*atomic.Int32{}
				for _, op := range []string{"GetWorker", "GetWorkerProfile", "GetWorkerTerminal", "GetWorkerModelGroups", "GetWorkerRendered", "GetWorkerAvailability", "GetWorkerBootPromptServe"} {
					counts[op] = &atomic.Int32{}
				}
				responses := bootResponses1655(t, `null`)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						OperationName string `json:"operationName"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					w.Header().Set("Content-Type", "application/json")
					total.Add(1)
					switch req.OperationName {
					case "GetWorker", "GetWorkerProfile", "GetWorkerTerminal", "GetWorkerModelGroups", "GetWorkerRendered", "GetWorkerAvailability", "GetWorkerBootPromptServe":
						counts[req.OperationName].Add(1)
					case "GetAgent", "GetNode", "NodeBatch", "Workers", "GetWorkerInApp", "GetWorkerIdInApp":
						t.Errorf("forbidden alternate preview operation %s", req.OperationName)
					default:
						t.Errorf("outside seven-read worker budget: %s", req.OperationName)
					}
					if req.OperationName == "GetWorkerBootPromptServe" {
						preview.Add(1)
						w.WriteHeader(status)
						fmt.Fprint(w, tc.body)
						return
					}
					if req.OperationName == "GetWorker" {
						lookup.Add(1)
					}
					response, ok := responses[req.OperationName]
					if !ok {
						response, ok = unstubbedDefault(req.OperationName)
					}
					if !ok {
						t.Errorf("unexpected operation %s", req.OperationName)
					}
					fmt.Fprint(w, response)
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs([]string{"team", "worker", "get", profileHexID, "--server", srv.URL, "--json"})
				err := root.Execute()
				if (err == nil) != tc.ok || lookup.Load() != 1 || preview.Load() != 1 || total.Load() != existingWorkerReads+previewReads {
					t.Fatalf("err%v lookup%d preview%d total%d", err, lookup.Load(), preview.Load(), total.Load())
				}
				for op, count := range counts {
					if count.Load() != 1 {
						t.Fatalf("%s calls=%d want1", op, count.Load())
					}
				}
				if !tc.ok && out.Len() != 0 {
					t.Fatal("failure emitted output", out.String())
				}
			})
		}
	}
}

// Synthetic decisions with an own pointer at another level must not decorate
// the inherited served identity with that unrelated pointer's URN.
func TestEffectiveBootOwnLevelCallSites1655(t *testing.T) {
	for _, surface := range []string{"app", "worker"} {
		t.Run(surface, func(t *testing.T) {
			own := "hrn:node:example.com:own:boot"
			outcome := `{"served":{"pointerLevel":"AGENT","compiledNodeId":"inherited","servedRevSeq":1},"refusal":null}`
			r := bootResponses1655(t, outcome)
			args := []string{"app", "agent", "list", "hrn:app:acme.com:support", "--json"}
			if surface == "app" {
				var p map[string]any
				_ = json.Unmarshal([]byte(r["AppAgentEffectiveRoster"]), &p)
				p["data"].(map[string]any)["app"].(map[string]any)["appAgents"].([]any)[0].(map[string]any)["bootPromptRef"] = own
				wire, _ := json.Marshal(p)
				r["AppAgentEffectiveRoster"] = string(wire)
			} else {
				r["GetWorkerBootPromptServe"] = `{"data":{"worker":{"bootPromptRef":"` + own + `","bootPromptTargetDeleted":false,"bootPromptServe":{"served":{"pointerLevel":"APP_AGENT","compiledNodeId":"inherited","servedRevSeq":1},"refusal":null}}}}`
				args = []string{"team", "worker", "get", profileHexID, "--json"}
			}
			_, out, err := runTeamTask(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			var row map[string]any
			if surface == "app" {
				var rows []map[string]any
				_ = json.Unmarshal([]byte(out), &rows)
				if len(rows) != 1 {
					t.Fatal(out)
				}
				row = rows[0]
			} else {
				_ = json.Unmarshal([]byte(out), &row)
			}
			if _, ok := row["bootPromptUrn"]; ok || row["bootPromptRef"] != own || row["compiledNodeId"] != "inherited" {
				t.Fatalf("own pointer decorated inherited source: %s", out)
			}
		})
	}
}

// A capable preview read supplies the current own-pointer keys, including nulls;
// the earlier lookup must not decorate a now-withheld preview with stale state.
func TestWorkerBootNullTarget1756(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			responses := bootResponses1655(t, `null`)
			var lookup map[string]any
			if err := json.Unmarshal([]byte(responses["GetWorker"]), &lookup); err != nil {
				t.Fatal(err)
			}
			worker := lookup["data"].(map[string]any)["worker"].(map[string]any)
			const stale = "hrn:node:example.com:stale:boot"
			worker["bootPromptRef"], worker["bootPromptTargetDeleted"] = stale, true
			wire, err := json.Marshal(lookup)
			if err != nil {
				t.Fatal(err)
			}
			responses["GetWorker"] = string(wire)
			responses["GetWorkerBootPromptServe"] = `{"data":{"worker":null}}`
			args := []string{"team", "worker", "get", profileHexID}
			if asJSON {
				args = append(args, "--json")
			}
			calls, out, err := runTeamTask(t, responses, args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, stale) || strings.Contains(out, "deleted: clear or repoint") {
				t.Fatalf("stale own-pointer cue: %s", out)
			}
			if asJSON {
				var dto map[string]any
				if err := json.Unmarshal([]byte(out), &dto); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"bootPromptRef", "bootPromptTargetDeleted", "pointerLevel", "compiledNodeId", "servedRevSeq", "bootPromptRefusal"} {
					if value, ok := dto[key]; !ok || value != nil {
						t.Fatalf("%s must be present null: %s", key, out)
					}
				}
				if _, ok := dto["bootPromptUrn"]; ok {
					t.Fatalf("served URN invented: %s", out)
				}
			} else if !strings.Contains(out, "  boot prompt (next bind): not visible to you (next bind)\n") {
				t.Fatalf("missing/augmented preview outcome: %s", out)
			}
			lookupCalls, previewCalls := 0, 0
			for _, call := range *calls {
				switch call.Op {
				case "GetWorker":
					lookupCalls++
				case "GetWorkerBootPromptServe":
					previewCalls++
				case "GetAgent", "GetNode", "NodeBatch", "Workers", "GetWorkerInApp", "GetWorkerIdInApp":
					t.Fatalf("alternate lookup: %s", call.Op)
				}
			}
			if lookupCalls != 1 || previewCalls != 1 {
				t.Fatalf("lookup=%d preview=%d", lookupCalls, previewCalls)
			}
		})
	}
}
