package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	urn "github.com/hadron-memory/urn-lib-go"
	"os"
	"path/filepath"
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
	if err != nil {
		renderError(f, err)
	}
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

// Mapped errors expose ServerCode() to the root sink; api.ServerCodeOf reads RAW GraphQL errors.
func serverCode1669(err error) string {
	var coded interface{ ServerCode() string }
	if errors.As(err, &coded) {
		return coded.ServerCode()
	}
	return ""
}
func TestAppAction1669RefusalCode(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		args := []string{"app.x.go"}
		if asJSON {
			args = append(args, "--json")
		}
		out, n, _, err := runAction1669(t, `{"errors":[{"message":"Invalid App action code","extensions":{"code":"APP_ACTION_CODE_INVALID","appUrn":"hrn:app:acme.com:x"}}]}`, args...)
		if err == nil || n != 1 || serverCode1669(err) != "APP_ACTION_CODE_INVALID" || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v %d", err, n)
		}
		if asJSON {
			var envelope struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil || envelope.Error.Code != exitcode.Usage || envelope.Error.ServerCode != "APP_ACTION_CODE_INVALID" {
				t.Fatalf("root envelope lost structured code: %s (decode %v)", out, decodeErr)
			}
		} else {
			out = err.Error()
		}
		if !strings.Contains(out, "hrn:app:acme.com:x") || !strings.Contains(out, "APP_ACTION_CODE_INVALID") {
			t.Fatalf("refusal lost code or App: %s", out)
		}
	}
}
func TestAppAction1669MappedTask(t *testing.T) {
	response := `{"data":{"recordAppAction":{"eventId":"ev1","action":{"code":"app.x.go","state":"MAPPED","taskLoc":"tasks:one","taskUrn":"hrn:agenttask:acme.com:x:AT0.7"}}}}`
	for _, asJSON := range []bool{false, true} {
		args := []string{"app.x.go"}
		if asJSON {
			args = append(args, "--json")
		}
		out, n, _, err := runAction1669(t, response, args...)
		if err != nil || n != 1 {
			t.Fatalf("%v calls=%d", err, n)
		}
		if !strings.Contains(out, "tasks:one") || !strings.Contains(out, "hrn:agenttask:acme.com:x:AT0.7") || !strings.Contains(out, "MAPPED") {
			t.Fatalf("mapped fields lost: %s", out)
		}
		if asJSON {
			var dto struct {
				Action struct{ TaskLoc, TaskURN *string }
			}
			if json.Unmarshal([]byte(out), &dto) != nil || dto.Action.TaskLoc == nil || *dto.Action.TaskLoc != "tasks:one" || dto.Action.TaskURN == nil || *dto.Action.TaskURN != "hrn:agenttask:acme.com:x:AT0.7" {
				t.Fatal(out)
			}
		}
	}
}
func TestAppAction1669MappedTextSafe(t *testing.T) {
	response := `{"data":{"recordAppAction":{"eventId":"ev1","action":{"code":"app.x.go","state":"MAPPED","taskLoc":"tasks:\u001bone","taskUrn":"hrn:agenttask:acme.com:x:AT0.7\u001b"}}}}`
	out, n, _, err := runAction1669(t, response, "app.x.go")
	if err != nil || n != 1 || strings.Contains(out, "\x1b") || !strings.Contains(out, "tasks: one") || !strings.Contains(out, "hrn:agenttask:acme.com:x:AT0.7") {
		t.Fatalf("unsafe mapped text %q %v", out, err)
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

func TestAppAction1669BoundariesAndFile(t *testing.T) {
	if urn.MaxAtomLen != 64 {
		t.Fatalf("provider d8ec074f urn-lib-js0.0.18 MAX_ATOM_LEN64 no longer matches Go%d", urn.MaxAtomLen)
	}
	code := "app.x." + strings.Repeat("a", urn.MaxAtomLen-len("app.x."))
	payload := `{"x":"` + strings.Repeat("x", 4088) + `"}`
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	_, n, v, err := runAction1669(t, response1669("MAPPED"), code, "--payload-file", path)
	if err != nil || n != 1 {
		t.Fatalf("boundary n=%d %v", n, err)
	}
	var m map[string]any
	_ = json.Unmarshal(v, &m)
	if m["code"] != code || len(m["payload"].(map[string]any)["x"].(string)) != 4088 {
		t.Fatal(string(v))
	}
}
