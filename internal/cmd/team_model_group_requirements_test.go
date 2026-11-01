package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
					if c.Op == "AgentTaskGet" || c.Op == "AgentTaskList" {
						t.Fatal("hidden preflight read")
					}
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
	creates := 0
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			creates++
			var v map[string]map[string]json.RawMessage
			if err := json.Unmarshal(c.RawVars, &v); err != nil {
				t.Fatal(err)
			}
			raw, ok := v["input"]["requirements"]
			if !ok {
				t.Fatal("min-tier requirement missing")
			}
			var req map[string]json.RawMessage
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			if string(req["minTier"]) != `"mid"` {
				t.Fatalf("requirements=%s", raw)
			}
			for _, k := range []string{"modelGroupIn", "modelGroupNotIn"} {
				if _, ok := req[k]; ok {
					t.Fatalf("absent %s sent: %s", k, c.RawVars)
				}
			}
		}
	}
	if creates != 1 {
		t.Fatalf("creates=%d", creates)
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
				op := gen.AgentTaskGet_Operation
				if verb == "list" {
					op = gen.AgentTaskList_Operation
				}
				for _, field := range []string{"modelGroupIn", "modelGroupNotIn"} {
					found := false
					for _, line := range strings.Split(op, "\n") {
						found = found || strings.TrimSpace(line) == field
					}
					if !found {
						t.Fatalf("query omits %s", field)
					}
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
			if err == nil || !strings.Contains(err.Error(), "provider says missing group Large") || api.ServerCodeOf(err) != code {
				t.Fatalf("refusal hidden: %v", err)
			}
			wantExit := exitcode.Usage
			if code == "MODEL_GROUP_NOT_FOUND" {
				wantExit = exitcode.NotFound
			}
			if exitCodeFor(err) != wantExit {
				t.Fatalf("exit=%d want %d", exitCodeFor(err), wantExit)
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

// Exercise the production error renderer too: the chain's code must reach the
// JSON envelope, and human refusal text must survive the terminal wrapper.
func TestTaskModelGroupRefusalSink1485(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, code := range []string{"MODEL_GROUP_NOT_FOUND", "BAD_USER_INPUT", "TEAM_AGENT_AMBIGUOUS"} {
			for _, jsonMode := range []bool{false, true} {
				writeTeamBinding(t)
				op := "AgentTaskCreate"
				if verb == "update" {
					op = "AgentTaskUpdate"
				}
				responses := taskUpdateResponses1137()
				responses[op] = `{"errors":[{"message":"provider says missing group Large","extensions":{"code":"` + code + `","name":"Large"}}]}`
				srv, calls := attnServer(t, responses)
				setTeamBindingServer(t, srv.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "task", verb, "AT1.2", "--model-group", "Large", "--server", srv.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("refusal lost")
				}
				wantExit := exitcode.Usage
				if code == "MODEL_GROUP_NOT_FOUND" {
					wantExit = exitcode.NotFound
				}
				if got := renderError(f, err); got != wantExit {
					t.Fatalf("exit=%d want %d", got, wantExit)
				}
				if jsonMode {
					var e struct {
						Error struct {
							Message    string `json:"message"`
							ServerCode string `json:"serverCode"`
						} `json:"error"`
					}
					if err := json.Unmarshal([]byte(out.String()), &e); err != nil {
						t.Fatal(err)
					}
					if e.Error.ServerCode != code || !strings.Contains(e.Error.Message, "provider says missing group Large") {
						t.Fatalf("envelope=%s", out.String())
					}
				} else if !strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "provider says missing group Large") {
					t.Fatalf("human=%s", f.IOStreams.ErrOut.(*strings.Builder).String())
				}
				n := 0
				for _, c := range *calls {
					if c.Op == op {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("mutation count=%d", n)
				}
			}
		}
	}
}

func TestTaskModelGroupCompatibilityHelp1485(t *testing.T) {
	for _, tc := range []struct{ verb, want string }{
		{"", "even when no model-group flag is named"},
		{"create", "even with no group flag"},
		{"update", "even without group flags"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			args := []string{"team", "task"}
			if tc.verb != "" {
				args = append(args, tc.verb)
			}
			args = append(args, "--help")
			calls, out, err := runTeamTask(t, taskResponses(), args...)
			if err != nil || len(*calls) != 0 {
				t.Fatalf("help err=%v calls=%v", err, *calls)
			}
			for _, want := range []string{"task-record", "hadron-server#3029", tc.want} {
				if !strings.Contains(out, want) {
					t.Fatalf("help missing %q: %s", want, out)
				}
			}
		})
	}
}

func TestTaskModelGroupNullRequirements1485(t *testing.T) {
	for _, verb := range []string{"get", "list"} {
		resp := taskResponses()
		resp["AgentTaskGet"] = `{"data":{"agentTask":` + startedTaskFieldsJSON + `}}`
		resp["AgentTaskList"] = `{"data":{"agentTasks":[` + startedTaskFieldsJSON + `]}}`
		args := []string{"team", "task", verb}
		if verb == "get" {
			args = append(args, "AT1.2")
		}
		args = append(args, "--json")
		_, out, err := runTeamTask(t, resp, args...)
		if err != nil {
			t.Fatal(err)
		}
		var task map[string]json.RawMessage
		if verb == "get" {
			if err := json.Unmarshal([]byte(out), &task); err != nil {
				t.Fatal(err)
			}
		} else {
			var tasks []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &tasks); err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 {
				t.Fatalf("tasks=%s", out)
			}
			task = tasks[0]
		}
		if string(task["requirements"]) != "null" {
			t.Fatalf("requirements=%s", task["requirements"])
		}
	}
}
