package cmd

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNodeTypes1528Wire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    []string
		present bool
	}{
		{"unset", nil, nil, false},
		{"unset with filter", []string{"--type", "task"}, nil, false},
		{"repeat comma exact", []string{"--types", "Task, task", "--types", "Task,comment"}, []string{"Task", " task", "Task", "comment"}, true},
		{"explicit empty", []string{"--types", ""}, []string{}, true},
		{"both forwarded", []string{"--type", "task", "--types", "data"}, []string{"data"}, true},
		{"search path", []string{"--search", "term", "--types", "data,task"}, []string{"data", "task"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars, code := nodeLsChildrenRun(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			filter, _ := vars["filter"].(map[string]any)
			raw, ok := filter["nodeTypes"]
			if ok != tc.present {
				t.Fatalf("presence %v want %v: %v", ok, tc.present, vars)
			}
			if ok {
				b, _ := json.Marshal(raw)
				var got []string
				if err := json.Unmarshal(b, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %#v want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestNodeTypes1528ServerRefusal(t *testing.T) {
	for _, args := range [][]string{{"--types", ""}, {"--types", "comment"}, {"--types", "data", "--type", "task"}, {"--types", "a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a"}} {
		srv, calls := captureGraphQL(t, map[string]string{"FindNodes": `{"errors":[{"message":"nodeTypes is invalid","extensions":{"code":"BAD_USER_INPUT"}}]}`})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"node", "list", "--json", "--server", srv.URL}, args...))
		err := root.Execute()
		if err == nil || renderError(f, err) != 2 {
			t.Fatalf("wanted server usage refusal: %v", err)
		}
		if _, ok := calls["FindNodes"]; !ok {
			t.Fatal("must reach server, not local policy")
		}
	}
}
