package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// cli#1831 (cli#1656 row 14): an explicit --app qualifies a BARE task handle
// (^AT\d+(\.\d+)*$) into hrn:agenttask:<root>:<slug>:<handle> before the call,
// at every task-ref site of `team task`. No --app is exactly today's behaviour.

type call1831 struct {
	Op   string
	Vars map[string]any
}

// server1831 answers known operations from taskResponses and every other one
// with a GraphQL error, recording each request: these pins assert what was
// SENT, so an operation the canned map lacks must not fail the test.
func server1831(t *testing.T, overrides map[string]string) (*httptest.Server, *[]call1831) {
	t.Helper()
	responses := taskResponses()
	for k, v := range overrides {
		responses[k] = v
	}
	var mu sync.Mutex
	calls := &[]call1831{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		*calls = append(*calls, call1831{Op: body.OperationName, Vars: body.Variables})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if resp, ok := responses[body.OperationName]; ok {
			_, _ = io.WriteString(w, resp)
			return
		}
		_, _ = io.WriteString(w, `{"errors":[{"message":"stub: no canned answer","extensions":{"code":"NOT_FOUND"}}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func run1831(t *testing.T, overrides map[string]string, argv ...string) (*[]call1831, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := server1831(t, overrides)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String() + f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// leaves1831 collects every string value in a request's variables.
func leaves1831(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			leaves1831(e, out)
		}
	case map[string]any:
		for _, e := range x {
			leaves1831(e, out)
		}
	}
}

func sent1831(calls *[]call1831) []string {
	var out []string
	for _, c := range *calls {
		leaves1831(c.Vars, &out)
	}
	return out
}

func has1831(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

const app1831 = "hrn:app:r:s"

// Acceptance 1, 2, 6, 7: the handle forms, App spellings and pass-through.
func TestTaskAppQualify1831Get(t *testing.T) {
	for _, tc := range []struct {
		name, app, ref, want string
	}{
		{"urn app", "hrn:app:r:s", "AT0.5", "hrn:agenttask:r:s:AT0.5"},
		{"short app", "r:s", "AT0.5", "hrn:agenttask:r:s:AT0.5"},
		{"trimmed handle", "r:s", "  AT12  ", "hrn:agenttask:r:s:AT12"},
		{"nested handle", "acme.com:eng-team", "AT1.2.3", "hrn:agenttask:acme.com:eng-team:AT1.2.3"},
		{"task id unchanged", "r:s", "01a1c78a26947a15a374469a1ec1eea2", "01a1c78a26947a15a374469a1ec1eea2"},
		{"lowercase not a handle", "r:s", "at0.5", "at0.5"},
		{"bare AT not a handle", "r:s", "AT", "AT"},
		{"trailing dot not a handle", "r:s", "AT0.", "AT0."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, _ := run1831(t, nil, "team", "task", "get", tc.ref, "--app", tc.app, "--json")
			vals := sent1831(calls)
			if !has1831(vals, tc.want) {
				t.Fatalf("want %q sent, got %v\n%s", tc.want, vals, out)
			}
		})
	}
}

// Acceptance 3: no --app is exactly today's request, and the server's refusal
// comes through unchanged.
func TestTaskAppQualify1831NoAppUnchanged(t *testing.T) {
	refusal := `{"errors":[{"message":"You belong to more than one App, so a bare handle is ambiguous.","extensions":{"code":"TASK_HANDLE_QUALIFY"}}]}`
	calls, out, err := run1831(t, map[string]string{"AgentTaskGet": refusal}, "team", "task", "get", "AT0.5")
	if err == nil {
		t.Fatal("want the server refusal")
	}
	if !has1831(sent1831(calls), "AT0.5") {
		t.Fatalf("bare handle not sent verbatim: %v", sent1831(calls))
	}
	if !strings.Contains(out+err.Error(), "You belong to more than one App, so a bare handle is ambiguous.") {
		t.Fatalf("server text not passed through: %s / %v", out, err)
	}
}

// Acceptance 4 and 5: refusals before any request.
func TestTaskAppQualify1831RefusalsSendNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		msg  string
	}{
		{"app id with a bare handle", []string{"team", "task", "get", "AT0.5", "--app", "capp100000000000000000000"}, "hrn:app:"},
		{"qualified ref names another App", []string{"team", "task", "get", "hrn:agenttask:r:other:AT0.5", "--app", app1831}, "hrn:app:r:s"},
		{"server-form qualified ref names another App", []string{"team", "task", "get", "hrn:agenttask:hrn:app:x.com:s:AT0.5", "--app", app1831}, "hrn:app:r:s"},
		{"blocker names another App", []string{"team", "task", "add-blocker", "AT0.5", "hrn:agenttask:r:other:AT0.6", "--app", app1831}, "hrn:app:r:s"},
		{"--parent names another App", []string{"team", "task", "list", "--parent", "hrn:agenttask:r:other:AT0.5", "--app", app1831}, "hrn:app:r:s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := run1831(t, nil, tc.argv...)
			if exitCodeFor(err) != 2 {
				t.Fatalf("want exit 2, err=%v out=%s", err, out)
			}
			if len(*calls) != 0 {
				t.Fatalf("want zero requests, got %v", *calls)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("refusal %q does not name %q", err, tc.msg)
			}
		})
	}
}

// Acceptance 5 (second half): a qualified ref for the SAME App goes unchanged,
// in either the flat form or the server's embedded-App form.
func TestTaskAppQualify1831SameAppQualifiedUnchanged(t *testing.T) {
	for _, ref := range []string{"hrn:agenttask:r:s:AT0.5", "hrn:agenttask:hrn:app:r:s:AT0.5"} {
		calls, out, _ := run1831(t, nil, "team", "task", "get", ref, "--app", "r:s")
		if !has1831(sent1831(calls), ref) {
			t.Fatalf("%s not sent unchanged: %v\n%s", ref, sent1831(calls), out)
		}
	}
}

// Acceptance 8: every task-ref site of `team task` goes through the helper.
// A site that skips it sends the bare handle, and its row goes red.
func TestTaskAppQualify1831EverySite(t *testing.T) {
	const at5, at6, at9 = "hrn:agenttask:r:s:AT0.5", "hrn:agenttask:r:s:AT0.6", "hrn:agenttask:r:s:AT0.9"
	// op is the GraphQL operation that must carry the ref(s); refs maps the exact
	// variable path to the qualified value expected there (#1876). A leaf search
	// would pass a ref in the wrong operation or variable, or the two refs of a
	// two-ref command swapped.
	for _, tc := range []struct {
		name string
		argv []string
		op   string
		refs map[string]string
	}{
		{"get", []string{"get", "AT0.5"}, "AgentTaskGet", map[string]string{"id": at5}},
		{"tree", []string{"tree", "AT0.5"}, "AgentTaskTree", map[string]string{"root": at5}},
		{"update", []string{"update", "AT0.5", "--title", "x"}, "AgentTaskUpdate", map[string]string{"taskId": at5}},
		{"start", []string{"start", "AT0.5"}, "AgentTaskStart", map[string]string{"taskId": at5}},
		{"progress", []string{"progress", "AT0.5"}, "AgentTaskProgress", map[string]string{"taskId": at5}},
		{"finish", []string{"finish", "AT0.5", "--outcome", "ok"}, "AgentTaskFinish", map[string]string{"taskId": at5}},
		{"hand-back", []string{"hand-back", "AT0.5", "--code", "refused"}, "AgentTaskHandBack", map[string]string{"taskRef": at5}},
		{"cancel", []string{"cancel", "AT0.5"}, "AgentTaskCancel", map[string]string{"taskId": at5}},
		{"park", []string{"park", "AT0.5"}, "AgentTaskPark", map[string]string{"taskId": at5}},
		{"ready", []string{"ready", "AT0.5"}, "AgentTaskReady", map[string]string{"taskId": at5}},
		{"assign", []string{"assign", "AT0.5", "--assignee", "worker:Iris"}, "AgentTaskAssign", map[string]string{"taskId": at5}},
		{"reassign", []string{"reassign", "AT0.5", "--assignee", "worker:Iris"}, "AgentTaskReassign", map[string]string{"taskId": at5}},
		{"clone", []string{"clone", "AT0.5"}, "AgentTaskClone", map[string]string{"taskId": at5}},
		{"findings", []string{"findings", "AT0.5"}, "AgentTaskResultFindings", map[string]string{"id": at5}},
		{"finding-dispositions", []string{"finding-dispositions", "AT0.5"}, "AgentTaskFindingDispositions", map[string]string{"taskRef": at5}},
		{"dispose-finding", []string{"dispose-finding", "AT0.5", "F1", "--disposition", "fixed"}, "AgentTaskDisposeFinding", map[string]string{"taskRef": at5}},
		{"dependencies", []string{"dependencies", "AT0.5"}, "AgentTaskDependencies", map[string]string{"taskRef": at5}},
		{"set-priority", []string{"set-priority", "AT0.5", "2"}, "AgentTaskSetPriority", map[string]string{"taskRef": at5}},
		{"add-blocker", []string{"add-blocker", "AT0.5", "AT0.6"}, "AgentTaskAddBlocker", map[string]string{"taskRef": at5, "blockerTaskRef": at6}},
		{"remove-blocker", []string{"remove-blocker", "AT0.5", "AT0.6"}, "AgentTaskRemoveBlocker", map[string]string{"taskRef": at5, "blockerTaskRef": at6}},
		{"create --parent", []string{"create", "child", "--parent", "AT0.5"}, "AgentTaskCreate", map[string]string{"input.parentTaskId": at5}},
		{"create --blocked-by", []string{"create", "blocked", "--blocked-by", "AT0.5", "--blocked-by", "AT0.6"}, "AgentTaskCreate", map[string]string{"input.blockedBy.0": at5, "input.blockedBy.1": at6}},
		{"clone --parent", []string{"clone", "AT0.9", "--parent", "AT0.5"}, "AgentTaskClone", map[string]string{"taskId": at9, "input.parentTaskId": at5}},
		{"list --parent", []string{"list", "--parent", "AT0.5"}, "AgentTaskList", map[string]string{"parentTaskId": at5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := append(append([]string{"team", "task"}, tc.argv...), "--app", app1831)
			calls, out, _ := run1831(t, nil, argv...)
			var got *call1831
			for i := range *calls {
				if (*calls)[i].Op == tc.op {
					got = &(*calls)[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("%s: operation %s never sent; sent %v\n%s", tc.name, tc.op, *calls, out)
			}
			for path, want := range tc.refs {
				if v, ok := varAt1831(got.Vars, path); !ok || v != want {
					t.Errorf("%s: %s %s = %q (present=%v), want %q\n%s", tc.name, tc.op, path, v, ok, want, out)
				}
			}
			vals := sent1831(calls)
			for _, bare := range []string{"AT0.5", "AT0.6", "AT0.9"} {
				if has1831(vals, bare) {
					t.Errorf("%s: bare handle %q sent despite --app", tc.name, bare)
				}
			}
		})
	}
}

// varAt1831 reads the string at a dotted variable path ("input.blockedBy.1");
// a numeric segment indexes an array.
func varAt1831(vars map[string]any, path string) (string, bool) {
	var cur any = vars
	for _, seg := range strings.Split(path, ".") {
		switch x := cur.(type) {
		case map[string]any:
			cur = x[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return "", false
			}
			cur = x[i]
		default:
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// Acceptance 9: the help line no longer says the refusal depends on matches.
func TestTaskAppQualify1831Help(t *testing.T) {
	_, out, err := run1831(t, nil, "team", "task", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	if strings.Contains(flat, "matches tasks in more than one of your Apps") {
		t.Fatal("stale help line still present")
	}
	want := "if you belong to more than one App, a bare handle is refused: pass --app <app>, or qualify it as hrn:agenttask:acme.com:eng-team:AT1.2."
	if !strings.Contains(flat, want) {
		t.Fatalf("help lacks %q:\n%s", want, out)
	}
}
