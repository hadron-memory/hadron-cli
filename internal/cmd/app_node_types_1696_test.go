package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestAppNodeTypes1696Parity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  any
		code  string
		exit  int
	}{
		{"unset", nil, nil, "", 0},
		{"other filter", []string{"--type", "task"}, nil, "", 0},
		{"union", []string{"--types", "Task, task", "--types", "Task,data"}, []any{"Task", " task", "Task", "data"}, "", 0},
		{"CSV", []string{"--types", `"task"`}, []any{"task"}, "", 0},
		{"empty", []string{"--types", ""}, []any{}, "BAD_USER_INPUT", 2},
		{"both", []string{"--type", "task", "--types", "data"}, []any{"data"}, "BAD_USER_INPUT", 2},
		{"comment", []string{"--types", "comment"}, []any{"comment"}, "BAD_USER_INPUT", 2},
		{"cap", []string{"--types", strings.TrimSuffix(strings.Repeat("task,", 21), ",")}, repeatTypes1696(21), "BAD_USER_INPUT", 2},
		{"concealed", []string{"--types", "data"}, []any{"data"}, "NOT_FOUND", 4},
		{"denied", []string{"--types", "data"}, []any{"data"}, "FORBIDDEN", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, door := range []string{"app", "node"} {
				op, response, args := "AppNodesPage", `{"data":{"appNodesPage":{"items":[],"total":0,"hasMore":false}}}`, []string{"app", "nodes", "acme.com:team"}
				if door == "node" {
					op = "FindNodes"
					response = nodeLsChildrenEmpty
					args = []string{"node", "list"}
				}
				if tc.code != "" {
					response = `{"errors":[{"message":"nodeTypes refused","extensions":{"code":"` + tc.code + `"}}]}`
				}
				srv, calls := captureGraphQL(t, map[string]string{op: response})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args = append(args, "--json", "--server", srv.URL)
				args = append(args, tc.flags...)
				root.SetArgs(args)
				err := root.Execute()
				gotExit := 0
				if err != nil {
					gotExit = renderError(f, err)
				}
				if gotExit != tc.exit {
					t.Fatalf("%s exit=%d want=%d err=%v", door, gotExit, tc.exit, err)
				}
				vars, ok := varsOf(t, calls, op)
				if !ok {
					t.Fatalf("%s did not reach server", door)
				}
				filter, _ := vars["filter"].(map[string]any)
				got, present := filter["nodeTypes"]
				if present != (tc.want != nil) || !reflect.DeepEqual(got, tc.want) {
					b, _ := json.Marshal(vars)
					t.Fatalf("%s wire=%s want=%v", door, b, tc.want)
				}
			}
		})
	}
}
func repeatTypes1696(n int) []any {
	v := make([]any, n)
	for i := range v {
		v[i] = "task"
	}
	return v
}
func TestAppNodes1696NoManagementTracker(t *testing.T) {
	b, err := os.ReadFile("../api/coverage-matrix.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "Query.appNodesPage(") && strings.Contains(line, "#63") {
			t.Fatalf("stale tracker: %s", line)
		}
	}
}
