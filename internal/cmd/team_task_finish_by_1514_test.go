package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const finishBy1514 = "2099-11-02T09:00:00+01:00"

func TestTaskFinishByWire(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		args     []string
		want     string
	}{
		{"create set", "AgentTaskCreate", []string{"create", "T", "--finish-by", finishBy1514}, `"` + finishBy1514 + `"`},
		{"create omitted", "AgentTaskCreate", []string{"create", "T"}, ""},
		{"update set", "AgentTaskUpdate", []string{"update", "AT1.2", "--finish-by", finishBy1514}, `"` + finishBy1514 + `"`},
		{"update clear", "AgentTaskUpdate", []string{"update", "AT1.2", "--clear-finish-by"}, "null"},
		{"update omitted", "AgentTaskUpdate", []string{"update", "AT1.2", "--title", "T"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskUpdateResponses1137(), append([]string{"team", "task"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			call := c2Mutation(t, calls, tc.op)
			var v struct{ Input map[string]json.RawMessage }
			if err := json.Unmarshal(call.RawVars, &v); err != nil {
				t.Fatal(err)
			}
			got, present := v.Input["finishBy"]
			if present != (tc.want != "") || string(got) != tc.want {
				t.Fatalf("finishBy=%s present=%t want %q: %s", got, present, tc.want, call.RawVars)
			}
		})
	}
}

func TestTaskFinishByUsageBeforeRequests(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, v := range []string{"", "tomorrow", "2099-11-02", "2099-11-02T09:00:00+24:00", "2099-11-02T09:00:00,5Z"} {
			t.Run(verb+"/"+v, func(t *testing.T) {
				args := []string{"team", "task", verb, "T", "--finish-by", v}
				calls, _, err := runTeamTask(t, nil, args...)
				if exitCodeFor(err) != 2 || len(*calls) != 0 || !strings.Contains(err.Error(), "RFC 3339") {
					t.Fatalf("err=%v calls=%v", err, *calls)
				}
			})
		}
	}
	calls, _, err := runTeamTask(t, nil, "team", "task", "update", "AT1.2", "--finish-by", finishBy1514, "--clear-finish-by")
	if exitCodeFor(err) != 2 || len(*calls) != 0 || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
}

func TestTaskFinishByServerRefusals(t *testing.T) {
	for _, message := range []string{"finishBy must not be in the past", "finishBy must not be earlier than startBy"} {
		for _, verb := range []string{"create", "update"} {
			t.Run(verb+"/"+message, func(t *testing.T) {
				op := "AgentTaskCreate"
				id := "T"
				if verb == "update" {
					op = "AgentTaskUpdate"
					id = "AT1.2"
				}
				body, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "BAD_USER_INPUT"}}}})
				calls, out, err := runTeamTask(t, map[string]string{op: string(body)}, "team", "task", verb, id, "--finish-by", "2000-01-01T00:00:00Z")
				if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), message) || out != "" {
					t.Fatalf("err=%v output=%q", err, out)
				}
				c2Mutation(t, calls, op)
			})
		}
	}
}

func TestTaskFinishByReads(t *testing.T) {
	for _, value := range []string{`"` + finishBy1514 + `"`, "null"} {
		task := strings.Replace(taskFieldsJSON, `"startBy":`, `"finishBy":`+value+`,"startBy":`, 1)
		if !strings.Contains(task, `"finishBy":`+value) {
			t.Fatal("deadline fixture was not inserted")
		}
		for _, verb := range []string{"get", "list"} {
			for _, jsonMode := range []bool{true, false} {
				t.Run(verb+"/"+value+"/"+map[bool]string{true: "json", false: "text"}[jsonMode], func(t *testing.T) {
					responses := taskResponses()
					responses["AgentTaskGet"] = `{"data":{"agentTask":` + task + `}}`
					responses["AgentTaskList"] = `{"data":{"agentTasks":[` + task + `]}}`
					args := []string{"team", "task", verb}
					if verb == "get" {
						args = append(args, "AT1.2")
					}
					if jsonMode {
						args = append(args, "--json")
					}
					_, out, err := runTeamTask(t, responses, args...)
					if err != nil {
						t.Fatal(err)
					}
					if jsonMode {
						var rows []map[string]json.RawMessage
						if verb == "list" {
							if err := json.Unmarshal([]byte(out), &rows); err != nil {
								t.Fatal(err)
							}
						} else {
							var row map[string]json.RawMessage
							if err := json.Unmarshal([]byte(out), &row); err != nil {
								t.Fatal(err)
							}
							rows = append(rows, row)
						}
						if len(rows) != 1 || string(rows[0]["finishBy"]) != value {
							t.Fatalf("finishBy lost: %s", out)
						}
					} else if value != "null" && (!strings.Contains(out, finishBy1514) || !strings.Contains(strings.ToLower(out), "finish by")) {
						t.Fatalf("text deadline missing: %s", out)
					}
				})
			}
		}
	}
}

func TestTaskFinishByFragmentSelection(t *testing.T) {
	b, err := os.ReadFile("../api/queries/agenttasks.graphql")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: string(b)})
	if err != nil {
		t.Fatal(err)
	}
	fragment := doc.Fragments.ForName("AgentTaskFields")
	if fragment == nil {
		t.Fatal("no shared task fragment")
	}
	for _, selection := range fragment.SelectionSet {
		if field, ok := selection.(*ast.Field); ok && field.Name == "finishBy" {
			return
		}
	}
	t.Fatal("shared task fragment does not request finishBy")
}

func TestTaskFinishByHelp(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range [][]string{{"team", "task", "create"}, {"team", "task", "update"}, {"team", "events"}, {"team", "alarm"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Long, "stall.finish-by") {
			t.Fatalf("%v does not name finish alarm", path)
		}
		if path[len(path)-1] == "update" && (c.Flags().Lookup("finish-by") == nil || c.Flags().Lookup("clear-finish-by") == nil) {
			t.Fatal("missing update flags")
		}
	}
}
