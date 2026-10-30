package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// runTaskList1250 runs `team task list` against a server that answers both
// list documents, and returns which one was sent with its variables.
func runTaskList1250(t *testing.T, args ...string) (op string, vars map[string]any, out string, err error) {
	t.Helper()
	empty := `{"data":{"agentTasks":[]}}`
	srv, captured := captureGraphQL(t, map[string]string{
		"AgentTaskList": empty, "AgentTaskListText": empty, "AgentTaskListFiltered": empty,
	})
	f, o := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "task", "list", "--app", "capp100000000000000000000", "--json", "--server", srv.URL}, args...))
	err = root.Execute()
	for name, raw := range captured {
		if op != "" {
			t.Fatalf("two list requests sent: %s and %s", op, name)
		}
		op = name
		if e := json.Unmarshal(raw, &vars); e != nil {
			t.Fatal(e)
		}
	}
	return op, vars, o.String(), err
}

// Each new flag alone reaches its own GraphQL variable on the filtered
// document, and nothing else new is sent (cli#1250).
func TestTaskListFilterFlags1250(t *testing.T) {
	for _, tc := range []struct {
		args []string
		key  string
		want any
	}{
		{[]string{"--tag-any", "release"}, "tagsAny", []any{"release"}},
		{[]string{"--tag-all", "release"}, "tagsAll", []any{"release"}},
		{[]string{"--source", "https://github.com/hadron-memory/hadron-cli/issues/1250"}, "source", "https://github.com/hadron-memory/hadron-cli/issues/1250"},
		{[]string{"--overdue"}, "overdue", true},
		{[]string{"--overdue=false"}, "overdue", false},
		{[]string{"--created-after", "2026-10-01T00:00:00Z"}, "createdAfter", "2026-10-01T00:00:00Z"},
		{[]string{"--created-before", "2026-10-30T12:00:00+02:00"}, "createdBefore", "2026-10-30T12:00:00+02:00"},
		{[]string{"--started-after", "2026-10-01T00:00:00Z"}, "startedAfter", "2026-10-01T00:00:00Z"},
		{[]string{"--started-before", "2026-10-30T00:00:00Z"}, "startedBefore", "2026-10-30T00:00:00Z"},
		{[]string{"--finished-after", "2026-10-01T00:00:00.5Z"}, "finishedAfter", "2026-10-01T00:00:00.5Z"},
		{[]string{"--finished-before", "2026-10-30T00:00:00Z"}, "finishedBefore", "2026-10-30T00:00:00Z"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			op, vars, out, err := runTaskList1250(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if op != "AgentTaskListFiltered" {
				t.Fatalf("sent %s, want AgentTaskListFiltered", op)
			}
			if got := vars[tc.key]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s = %#v, want %#v (vars %v)", tc.key, got, tc.want, vars)
			}
			for _, k := range filterVars1250 {
				if _, ok := vars[k]; ok && k != tc.key {
					t.Errorf("unset filter %s sent: %v", k, vars)
				}
			}
			if out != "[]\n" {
				t.Errorf("--json shape changed: %q", out)
			}
		})
	}
}

var filterVars1250 = []string{"tagsAny", "tagsAll", "source", "overdue", "createdAfter", "createdBefore", "startedAfter", "startedBefore", "finishedAfter", "finishedBefore"}

// With no new flag the request is the legacy document with the legacy
// variables, so an older server and today's output are unaffected.
func TestTaskListNoFilterUnchanged1250(t *testing.T) {
	op, vars, out, err := runTaskList1250(t, "--tag", "release", "--status", "PENDING")
	if err != nil {
		t.Fatal(err)
	}
	if op != "AgentTaskList" {
		t.Fatalf("sent %s, want the legacy AgentTaskList", op)
	}
	want := map[string]any{"appRef": "capp100000000000000000000", "limit": float64(20), "offset": float64(0), "status": []any{"PENDING"}, "tag": "release"}
	if !reflect.DeepEqual(vars, want) {
		t.Fatalf("vars = %v, want %v", vars, want)
	}
	if out != "[]\n" {
		t.Fatalf("output %q", out)
	}
	for _, k := range filterVars1250 {
		if strings.Contains(gen.AgentTaskList_Operation, "$"+k) || strings.Contains(gen.AgentTaskListText_Operation, "$"+k) {
			t.Errorf("legacy document gained $%s", k)
		}
	}
}

// Filters AND together on one request, alongside --tag, --text and --status;
// a repeated tag flag accumulates and a comma splits, as `task create --tag`.
func TestTaskListFiltersCombine1250(t *testing.T) {
	op, vars, _, err := runTaskList1250(t,
		"--tag", "release", "--text", "needle", "--status", "PENDING",
		"--tag-any", "a,b", "--tag-any", "c", "--tag-all", "x", "--tag-all", "y",
		"--source", "src", "--overdue",
		"--created-after", "2026-10-01T00:00:00Z", "--created-before", "2026-10-30T00:00:00Z",
		"--started-after", "2026-10-02T00:00:00Z", "--started-before", "2026-10-29T00:00:00Z",
		"--finished-after", "2026-10-03T00:00:00Z", "--finished-before", "2026-10-28T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if op != "AgentTaskListFiltered" {
		t.Fatalf("sent %s", op)
	}
	want := map[string]any{
		"tag": "release", "text": "needle", "status": []any{"PENDING"},
		"tagsAny": []any{"a", "b", "c"}, "tagsAll": []any{"x", "y"}, "source": "src", "overdue": true,
		"createdAfter": "2026-10-01T00:00:00Z", "createdBefore": "2026-10-30T00:00:00Z",
		"startedAfter": "2026-10-02T00:00:00Z", "startedBefore": "2026-10-29T00:00:00Z",
		"finishedAfter": "2026-10-03T00:00:00Z", "finishedBefore": "2026-10-28T00:00:00Z",
	}
	for k, v := range want {
		if !reflect.DeepEqual(vars[k], v) {
			t.Errorf("%s = %#v, want %#v", k, vars[k], v)
		}
	}
}

// A malformed time on any of the six flags is a usage error (exit 2) and no
// request is sent: an offset-less or date-only value is refused, as on
// `usage events --since/--until` (cli#1205).
func TestTaskListMalformedTime1250(t *testing.T) {
	for _, flag := range []string{"--created-after", "--created-before", "--started-after", "--started-before", "--finished-after", "--finished-before"} {
		for _, bad := range []string{"2026-10-30T00:00:00", "2026-10-30", "yesterday", ""} {
			t.Run(flag+"="+bad, func(t *testing.T) {
				op, _, out, err := runTaskList1250(t, flag, bad)
				if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), flag) {
					t.Fatalf("want a usage error naming %s, got %v", flag, err)
				}
				if op != "" || out != "" {
					t.Fatalf("sent %q / printed %q for a malformed time", op, out)
				}
			})
		}
	}
}

// An inverted range is refused before any request, as the server would;
// equal bounds are a valid inclusive instant.
func TestTaskListInvertedRange1250(t *testing.T) {
	for _, field := range []string{"created", "started", "finished"} {
		op, _, _, err := runTaskList1250(t, "--"+field+"-after", "2026-10-30T00:00:00Z", "--"+field+"-before", "2026-10-29T00:00:00Z")
		if err == nil || exitCodeFor(err) != 2 || op != "" {
			t.Fatalf("%s: inverted range sent %q, err %v", field, op, err)
		}
		op, _, _, err = runTaskList1250(t, "--"+field+"-after", "2026-10-30T02:00:00+02:00", "--"+field+"-before", "2026-10-30T00:00:00Z")
		if err != nil || op != "AgentTaskListFiltered" {
			t.Fatalf("%s: the same instant in two offsets must pass, got %q %v", field, op, err)
		}
	}
}

// The team view reads the whole App and joins by roster, so each new
// server-side filter would narrow below that read: refused, like --tag.
func TestTaskListTeamRefusesFilters1250(t *testing.T) {
	for _, args := range [][]string{
		{"--tag-any", "a"}, {"--tag-all", "a"}, {"--source", "s"}, {"--overdue"}, {"--overdue=false"},
		{"--created-after", "2026-10-01T00:00:00Z"}, {"--created-before", "2026-10-01T00:00:00Z"},
		{"--started-after", "2026-10-01T00:00:00Z"}, {"--started-before", "2026-10-01T00:00:00Z"},
		{"--finished-after", "2026-10-01T00:00:00Z"}, {"--finished-before", "2026-10-01T00:00:00Z"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			op, _, _, err := runTaskList1250(t, append([]string{"--team=worker:Marco"}, args...)...)
			if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), strings.SplitN(args[0], "=", 2)[0]) {
				t.Fatalf("want a usage refusal naming %s, got %v", args[0], err)
			}
			if op != "" {
				t.Fatalf("sent %s", op)
			}
		})
	}
}

// The help states the semantics the server applies (issue acceptance).
func TestTaskListFilterHelp1250(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--tag-any", "--tag-all", "--source", "--overdue", "--created-after", "--finished-before", "inclusive", "never matches", "backlog task is never overdue", "AND"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
