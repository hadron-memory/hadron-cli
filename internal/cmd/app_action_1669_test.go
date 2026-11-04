package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func runAction1669(t *testing.T, response string, args ...string) (string, int, json.RawMessage, error) {
	t.Helper()
	srv, count, vars := countingGraphQL(t, map[string]string{"RecordAppAction": response})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"app", "action", "record", "--app", "hrn:app:acme.com:x", "--server", srv.URL}, args...))
	err := root.Execute()
	return out.String(), count("RecordAppAction"), vars("RecordAppAction"), err
}
func response1669(state string) string {
	return fmt.Sprintf(`{"data":{"recordAppAction":{"eventId":"ev1","action":{"code":"app.x.design.approved","state":%q,"taskLoc":null,"taskUrn":null}}}}`, state)
}
func TestAppAction1669WireAndStates(t *testing.T) {
	for _, state := range []string{"MAPPED", "UNMAPPED", "UNRESOLVED", "WITHHELD"} {
		out, n, v, err := runAction1669(t, response1669(state), "app.x.design.approved", "--json")
		if err != nil || n != 1 {
			t.Fatalf("%s n=%d err=%v", state, n, err)
		}
		var m map[string]any
		if err = json.Unmarshal(v, &m); err != nil {
			t.Fatal(err)
		}
		if m["appRef"] != "hrn:app:acme.com:x" || m["code"] != "app.x.design.approved" {
			t.Fatal(string(v))
		}
		for _, k := range []string{"taskRef", "payload"} {
			if _, ok := m[k]; ok {
				t.Fatalf("omitted %s: %s", k, v)
			}
		}
		var dto struct {
			EventID string `json:"eventId"`
			Action  struct {
				Code, State      string
				TaskLoc, TaskURN *string
			} `json:"action"`
		}
		if err = json.Unmarshal([]byte(out), &dto); err != nil || dto.EventID != "ev1" || dto.Action.State != state || dto.Action.TaskLoc != nil || dto.Action.TaskURN != nil {
			t.Fatalf("dto %s %v", out, err)
		}
	}
	_, n, v, err := runAction1669(t, response1669("MAPPED"), "app.x.design.approved", "--task", "hrn:agenttask:hadronmemory.com:team:AT0.7", "--payload", `{"ok":true}`)
	if err != nil || n != 1 {
		t.Fatalf("%v %d", err, n)
	}
	var m map[string]any
	_ = json.Unmarshal(v, &m)
	if m["taskRef"] != "hrn:agenttask:hadronmemory.com:team:AT0.7" || m["payload"].(map[string]any)["ok"] != true {
		t.Fatal(string(v))
	}
}
func TestAppAction1669LocalRefusals(t *testing.T) {
	for _, args := range [][]string{{"app.X.go"}, {"app.x."}, {"app.x.design-"}, {"app.x." + strings.Repeat("a", 59)}, {"app.x.go", "--payload", "[]"}, {"app.x.go", "--payload", "null"}, {"app.x.go", "--payload", `{"x":"` + strings.Repeat("x", 4089) + `"}`}, {"app.x.go", "--payload", "{}", "--payload-file", "missing"}} {
		_, n, _, err := runAction1669(t, response1669("MAPPED"), args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
			t.Errorf("args=%v err=%v code=%d calls=%d", args, err, exitCodeFor(err), n)
		}
	}
}
func TestAppAction1669RefusalCode(t *testing.T) {
	out, n, _, err := runAction1669(t, `{"errors":[{"message":"code must name hrn:app:acme.com:x","extensions":{"code":"APP_ACTION_CODE_INVALID"}}]}`, "app.x.go", "--json")
	if err == nil || n != 1 {
		t.Fatalf("%v %d", err, n)
	}
	// The root Execute caller writes errors; the chain must retain the wire code.
	if !strings.Contains(fmt.Sprintf("%+v", err), "hrn:app:acme.com:x") {
		t.Fatalf("%s %v", out, err)
	}
}
func TestEvents1669CodePrefix(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		srv, count, vars := countingGraphQL(t, map[string]string{"AgentTeamEvents": `{"data":{"agentTeamEvents":[]}}`, "AgentTeamEventsByCode": `{"data":{"agentTeamEvents":[]}}`})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"team", "events", "--app", "hrn:app:acme.com:x", "--server", srv.URL, "--json"}
		op := "AgentTeamEvents"
		if filtered {
			args = append(args, "--code-prefix", "app.x.")
			op = "AgentTeamEventsByCode"
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if count(op) != 1 {
			t.Fatal("missing request")
		}
		var m map[string]any
		_ = json.Unmarshal(vars(op), &m)
		_, ok := m["codePrefix"]
		if ok != filtered || (filtered && m["codePrefix"] != "app.x.") {
			t.Fatalf("%s", vars(op))
		}
	}
}
