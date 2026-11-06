package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
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
