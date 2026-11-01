package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestTaskModelGroupWire1485(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, tc := range []struct {
			name  string
			flags []string
			want  map[string]any
		}{
			{"in", []string{"--model-group", "Large, odd! ,Large"}, map[string]any{"modelGroupIn": []any{"Large", " odd! ", "Large"}}},
			{"not", []string{"--model-group-not", "Small"}, map[string]any{"modelGroupNotIn": []any{"Small"}}},
			{"both raw overlap", []string{"--model-group", "Same,UPPER", "--model-group-not", "Same"}, map[string]any{"modelGroupIn": []any{"Same", "UPPER"}, "modelGroupNotIn": []any{"Same"}}},
			{"combined", []string{"--model-group", "Large", "--model-group-not", "Small", "--require-role", "qa", "--family-in", "claude", "--exclude-worker", "Iris", "--min-tier", "mid"}, map[string]any{"modelGroupIn": []any{"Large"}, "modelGroupNotIn": []any{"Small"}, "role": "qa", "familyIn": []any{"claude"}, "excludeWorkers": []any{"Iris"}, "minTier": "mid"}},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				args := append([]string{"team", "task", verb, "AT1.2"}, tc.flags...)
				calls, _, err := runTeamTask(t, taskUpdateResponses1137(), args...)
				if err != nil {
					t.Fatal(err)
				}
				op := "AgentTaskCreate"
				if verb == "update" {
					op = "AgentTaskUpdate"
				}
				n := 0
				for _, c := range *calls {
					if c.Op != op {
						continue
					}
					n++
					var v struct {
						Input map[string]json.RawMessage `json:"input"`
					}
					if err := json.Unmarshal(c.RawVars, &v); err != nil {
						t.Fatal(err)
					}
					var got map[string]any
					if err := json.Unmarshal(v.Input["requirements"], &got); err != nil {
						t.Fatal(err)
					}
					// Existing create fields may be explicit null; new group fields must never be.
					for k, v := range got {
						if v == nil && k != "modelGroupIn" && k != "modelGroupNotIn" {
							delete(got, k)
						}
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("requirements=%v want %v", got, tc.want)
					}
				}
				if n != 1 {
					t.Fatalf("mutations=%d", n)
				}
			})
		}
	}
}

func TestTaskModelGroupSyntaxBeforeHTTP1485(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, flag := range []string{"--model-group", "--model-group-not"} {
			cases := [][]string{{flag, ""}, {flag, ","}, {flag, " , "}}
			if verb == "update" {
				cases = append(cases, []string{flag, "G", "--clear-requirements"})
			}
			for _, flags := range cases {
				t.Run(verb+strings.Join(flags, "/"), func(t *testing.T) {
					calls, _, err := runTeamTask(t, taskUpdateResponses1137(), append([]string{"team", "task", verb, "AT1.2"}, flags...)...)
					if exitCodeFor(err) != exitcode.Usage || strings.Contains(err.Error(), "unknown flag") {
						t.Fatalf("syntax refusal=%v", err)
					}
					if len(*calls) != 0 {
						t.Fatalf("HTTP calls=%v", *calls)
					}
				})
			}
		}
	}
}

func TestTaskModelGroupOmissionReplacement1485(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{[]string{"--title", "only"}, ""},
		{[]string{"--min-tier", "high"}, `{"minTier":"high"}`},
		{[]string{"--clear-requirements"}, "null"},
	} {
		in, _ := updateInput1137(t, append([]string{"AT1.2"}, tc.flags...)...)
		if string(in["requirements"]) != tc.want {
			t.Fatalf("%v requirements=%s", tc.flags, in["requirements"])
		}
	}
	// Ordinary create with an existing requirement omits BOTH new keys.
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "T", "--min-tier", "mid")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			var v map[string]map[string]json.RawMessage
			_ = json.Unmarshal(c.RawVars, &v)
			var req map[string]json.RawMessage
			_ = json.Unmarshal(v["input"]["requirements"], &req)
			for _, k := range []string{"modelGroupIn", "modelGroupNotIn"} {
				if _, ok := req[k]; ok {
					t.Fatalf("absent %s sent: %s", k, c.RawVars)
				}
			}
		}
	}
}

func TestTaskModelGroupOutput1485(t *testing.T) {
	for _, groups := range []string{`"modelGroupIn":["Large\u001b[31m"],"modelGroupNotIn":["Small"],`, `"modelGroupIn":null,"modelGroupNotIn":null,`, ""} {
		row := strings.Replace(taskFieldsJSON, `"requirements":{`, `"requirements":{`+groups, 1)
		for _, verb := range []string{"get", "list"} {
			for _, jsonMode := range []bool{false, true} {
				resp := taskResponses()
				resp["AgentTaskGet"] = `{"data":{"agentTask":` + row + `}}`
				resp["AgentTaskList"] = `{"data":{"agentTasks":[` + row + `]}}`
				args := []string{"team", "task", verb}
				if verb == "get" {
					args = append(args, "AT1.2")
				}
				if jsonMode {
					args = append(args, "--json")
				}
				_, out, err := runTeamTask(t, resp, args...)
				if err != nil {
					t.Fatal(err)
				}
				if jsonMode {
					var val any
					if err := json.Unmarshal([]byte(out), &val); err != nil {
						t.Fatal(err)
					}
					var task map[string]any
					if verb == "get" {
						task = val.(map[string]any)
					} else {
						task = val.([]any)[0].(map[string]any)
					}
					req := task["requirements"].(map[string]any)
					for _, k := range []string{"modelGroupIn", "modelGroupNotIn"} {
						a, ok := req[k].([]any)
						if !ok {
							t.Fatalf("%s missing/not array: %s", k, out)
						}
						if strings.Contains(groups, "Large") && len(a) != 1 {
							t.Fatalf("missing group: %s", out)
						}
						if !strings.Contains(groups, "Large") && len(a) != 0 {
							t.Fatalf("not empty: %s", out)
						}
					}
					if strings.Contains(groups, "Large") && req["modelGroupIn"].([]any)[0] != "Large\x1b[31m" {
						t.Fatalf("JSON altered: %s", out)
					}
				} else if strings.Contains(groups, "Large") {
					if !strings.Contains(out, "Large") || !strings.Contains(out, "Small") || strings.Contains(out, "\x1b") {
						t.Fatalf("unsafe/missing groups: %q", out)
					}
				}
			}
		}
	}
}

func TestTaskModelGroupRefusals1485(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, code := range []string{"MODEL_GROUP_NOT_FOUND", "BAD_USER_INPUT", "TEAM_AGENT_AMBIGUOUS"} {
			resp := taskUpdateResponses1137()
			op := "AgentTaskCreate"
			if verb == "update" {
				op = "AgentTaskUpdate"
			}
			resp[op] = `{"errors":[{"message":"provider says missing group Large","extensions":{"code":"` + code + `","name":"Large"}}]}`
			calls, _, err := runTeamTask(t, resp, "team", "task", verb, "AT1.2", "--model-group", "Large")
			if err == nil || !strings.Contains(err.Error(), "provider says missing group Large") {
				t.Fatalf("refusal hidden: %v", err)
			}
			n := 0
			for _, c := range *calls {
				if c.Op == op {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("refusal mutations=%d", n)
			}
		}
	}
}
