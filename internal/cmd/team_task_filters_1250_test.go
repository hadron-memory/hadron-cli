package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const filters1250App = "capp100000000000000000000"

func filters1250Run(t *testing.T, op string, args ...string) (map[string]any, bool, error) {
	t.Helper()
	srv, captured := captureGraphQL(t, map[string]string{op: `{"data":{"agentTasks":[]}}`, "AgentTaskList": `{"data":{"agentTasks":[]}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs(append([]string{"team", "task", "list", "--app", filters1250App, "--json", "--server", srv.URL}, args...))
	err := r.Execute()
	raw, ok := captured[op]
	if !ok {
		return nil, false, err
	}
	var vars map[string]any
	if e := json.Unmarshal(raw, &vars); e != nil {
		t.Fatal(e)
	}
	return vars, true, err
}

func TestTaskListFiltersReachVariables1250(t *testing.T) {
	cases := []struct {
		args []string
		want map[string]any
	}{
		{[]string{"--tag-any", "a", "--tag-any", "b"}, map[string]any{"tagsAny": []any{"a", "b"}}},
		{[]string{"--tag-all", "x", "--tag-all", "y"}, map[string]any{"tagsAll": []any{"x", "y"}}},
		{[]string{"--source", "slack"}, map[string]any{"source": "slack"}},
		{[]string{"--overdue"}, map[string]any{"overdue": true}},
		{[]string{"--overdue=false"}, map[string]any{"overdue": false}},
		{[]string{"--created-after", "2026-10-01T00:00:00Z"}, map[string]any{"createdAfter": "2026-10-01T00:00:00Z"}},
		{[]string{"--created-before", "2026-10-02T00:00:00+02:00"}, map[string]any{"createdBefore": "2026-10-02T00:00:00+02:00"}},
		{[]string{"--started-after", "2026-10-01T00:00:00Z"}, map[string]any{"startedAfter": "2026-10-01T00:00:00Z"}},
		{[]string{"--started-before", "2026-10-01T00:00:00Z"}, map[string]any{"startedBefore": "2026-10-01T00:00:00Z"}},
		{[]string{"--finished-after", "2026-10-01T00:00:00Z"}, map[string]any{"finishedAfter": "2026-10-01T00:00:00Z"}},
		{[]string{"--finished-before", "2026-10-01T00:00:00Z"}, map[string]any{"finishedBefore": "2026-10-01T00:00:00Z"}},
		// a comma is part of the tag, not a separator
		{[]string{"--tag-any", "a,b"}, map[string]any{"tagsAny": []any{"a,b"}}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			vars, ok, err := filters1250Run(t, "AgentTaskListFiltered", c.args...)
			if err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			for k, want := range c.want {
				got, present := vars[k]
				if !present {
					t.Fatalf("%s not sent: %v", k, vars)
				}
				if a, _ := json.Marshal(got); string(a) != mustJSON1250(want) {
					t.Fatalf("%s = %s, want %s", k, a, mustJSON1250(want))
				}
			}
			for _, k := range []string{"tagsAny", "tagsAll", "source", "overdue", "createdAfter", "createdBefore", "startedAfter", "startedBefore", "finishedAfter", "finishedBefore"} {
				if _, want := c.want[k]; !want {
					if _, sent := vars[k]; sent {
						t.Fatalf("unset %s was sent: %v", k, vars)
					}
				}
			}
		})
	}
}

func mustJSON1250(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestTaskListNoNewFlagKeepsLegacyRequest1250(t *testing.T) {
	_, ok, err := filters1250Run(t, "AgentTaskListFiltered")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no new flag must not use the filtered document")
	}
	if _, ok, _ := filters1250Run(t, "AgentTaskList"); !ok {
		t.Fatal("legacy document not used")
	}
}

func TestTaskListMalformedTimeIsUsageError1250(t *testing.T) {
	for _, flag := range []string{"created-after", "created-before", "started-after", "started-before", "finished-after", "finished-before"} {
		for _, bad := range []string{"2026-10-01", "2026-10-01T00:00:00", "yesterday", ""} {
			t.Run(flag+"="+bad, func(t *testing.T) {
				srv, captured := captureGraphQL(t, map[string]string{})
				f, _ := testFactory(t)
				r := NewRootCmd(f)
				r.SetArgs([]string{"team", "task", "list", "--app", filters1250App, "--server", srv.URL, "--" + flag, bad})
				err := r.Execute()
				if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "--"+flag) {
					t.Fatalf("want usage error naming --%s, got %v", flag, err)
				}
				if len(captured) != 0 {
					t.Fatalf("request sent: %v", captured)
				}
			})
		}
	}
}

func TestTaskListNewFiltersConflictWithTeam1250(t *testing.T) {
	for _, args := range [][]string{{"--tag-any", "a"}, {"--tag-all", "a"}, {"--source", "s"}, {"--overdue"}, {"--created-after", "2026-10-01T00:00:00Z"}, {"--started-before", "2026-10-01T00:00:00Z"}, {"--finished-after", "2026-10-01T00:00:00Z"}} {
		srv, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append([]string{"team", "task", "list", "--app", filters1250App, "--server", srv.URL, "--team"}, args...))
		err := r.Execute()
		if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), args[0]) {
			t.Fatalf("%v: want usage conflict, got %v", args, err)
		}
		if len(captured) != 0 {
			t.Fatalf("request sent: %v", captured)
		}
	}
}

func TestTaskListHelpStatesFilterSemantics1250(t *testing.T) {
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"team", "task", "list", "--help"})
	_ = r.Execute()
	h := strings.ToLower(out.String())
	for _, s := range []string{"inclusive", "never matches", "backlog", "and", "--tag-any", "--overdue"} {
		if !strings.Contains(h, s) {
			t.Errorf("help missing %q", s)
		}
	}
}

// An inverted range is refused before any request (pins from Jonas's
// jonas/1250-task-list-filters); the same instant in two offsets is valid.
func TestTaskListInvertedRange1250(t *testing.T) {
	for _, field := range []string{"created", "started", "finished"} {
		_, ok, err := filters1250Run(t, "AgentTaskListFiltered", "--"+field+"-after", "2026-10-30T00:00:00Z", "--"+field+"-before", "2026-10-29T00:00:00Z")
		if err == nil || exitCodeFor(err) != 2 || ok {
			t.Fatalf("%s: inverted range sent=%v err=%v", field, ok, err)
		}
		_, ok, err = filters1250Run(t, "AgentTaskListFiltered", "--"+field+"-after", "2026-10-30T02:00:00+02:00", "--"+field+"-before", "2026-10-30T00:00:00Z")
		if err != nil || !ok {
			t.Fatalf("%s: equal instants must pass: sent=%v err=%v", field, ok, err)
		}
	}
}
