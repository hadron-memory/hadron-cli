package cmd

// hadron-cli#1524 (hadron-server#3125; plan
// hrn:node:hadronmemory.com:hadron-dev-team-shared:plans:1524-find-untracked).
// C1: externalId on the task commands (S2, cor:agt:120:02).
// C2: `app properties get|set|clear` over App.properties (S1, cor:dmo:050:20).
// The CLI is a thin client: it sends values as given and maps the server's
// refusals; every rule is the server's (cor:api:240:01).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const issueURL1524 = "https://github.com/acme/app/issues/42"

// envelope1524 renders err through the production renderer as --json and
// returns the error envelope.
func envelope1524(t *testing.T, err error) (int, map[string]any) {
	t.Helper()
	f, out := testFactory(t)
	f.JSON = true
	code := renderError(f, err)
	var env struct {
		Error map[string]any `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", jerr, out.String())
	}
	return code, env.Error
}

// oneCall1524 is the single call of op, failing the test unless there is
// exactly one call and it is op.
func oneCall1524(t *testing.T, calls *[]attnCall, op string) attnCall {
	t.Helper()
	if len(*calls) != 1 || (*calls)[0].Op != op {
		t.Fatalf("calls = %v, want exactly one %s", *calls, op)
	}
	return (*calls)[0]
}

// oneOp1524 is the single call of op among calls (other ops allowed).
func oneOp1524(t *testing.T, calls *[]attnCall, op string) attnCall {
	t.Helper()
	got := opCalls1524(calls, op)
	if len(got) != 1 {
		t.Fatalf("%s calls = %d, want 1 (all: %v)", op, len(got), *calls)
	}
	return got[0]
}

func opCalls1524(calls *[]attnCall, op string) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == op {
			got = append(got, c)
		}
	}
	return got
}

// ---- C1: externalId on task commands --------------------------------------

func TestTaskCreateExternalID1524(t *testing.T) {
	t.Run("sent verbatim", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "T", "--external-id", issueURL1524)
		if err != nil {
			t.Fatal(err)
		}
		input, _ := oneOp1524(t, calls, "AgentTaskCreate").Vars["input"].(map[string]any)
		if input["externalId"] != issueURL1524 {
			t.Errorf("input.externalId = %#v, want %q", input["externalId"], issueURL1524)
		}
	})

	t.Run("omitted flag sends no key", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "T")
		if err != nil {
			t.Fatal(err)
		}
		input, _ := oneOp1524(t, calls, "AgentTaskCreate").Vars["input"].(map[string]any)
		if _, present := input["externalId"]; present {
			t.Errorf("an omitted --external-id must send no key: %v", input)
		}
	})

	t.Run("a blank value is usage, before any call", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "T", "--external-id", "  ")
		if err != nil && strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("the flag must exist: %v", err)
		}
		if exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
		}
		if len(opCalls1524(calls, "AgentTaskCreate")) != 0 {
			t.Error("a blank --external-id must not reach the server")
		}
	})

	t.Run("a value with inner whitespace is the server's to judge", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "T", "--external-id", "a b")
		if err != nil {
			t.Fatal(err)
		}
		input, _ := oneOp1524(t, calls, "AgentTaskCreate").Vars["input"].(map[string]any)
		if input["externalId"] != "a b" {
			t.Errorf("the CLI must not format-check: %v", input["externalId"])
		}
	})

	t.Run("a duplicate exits 5 with serverCode", func(t *testing.T) {
		r := taskResponses()
		r["AgentTaskCreate"] = `{"errors":[{"message":"another task already claims that external id","extensions":{"code":"TASK_EXTERNAL_ID_TAKEN"}}]}`
		_, _, err := runTeamTask(t, r, "team", "task", "create", "T", "--external-id", issueURL1524)
		if exitCodeFor(err) != exitcode.Conflict {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Conflict, err)
		}
		if _, env := envelope1524(t, err); env["serverCode"] != "TASK_EXTERNAL_ID_TAKEN" {
			t.Errorf("serverCode = %v, want TASK_EXTERNAL_ID_TAKEN", env["serverCode"])
		}
	})
}

func TestTaskGetExternalID1524(t *testing.T) {
	withID := strings.Replace(taskFieldsJSON, `"source":`, `"externalId":"`+issueURL1524+`","source":`, 1)

	t.Run("json and text carry it", func(t *testing.T) {
		r := taskResponses()
		r["AgentTaskGet"] = `{"data":{"agentTask":` + withID + `}}`
		_, out, err := runTeamTask(t, r, "team", "task", "get", "task1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if dto["externalId"] != issueURL1524 {
			t.Errorf("externalId = %#v", dto["externalId"])
		}
		_, out, err = runTeamTask(t, r, "team", "task", "get", "task1")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "external id: "+issueURL1524+"\n") {
			t.Errorf("text is missing the external id line:\n%s", out)
		}
	})

	t.Run("absent is null in json and no text line", func(t *testing.T) {
		_, out, err := runTeamTask(t, taskResponses(), "team", "task", "get", "task1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if v, present := dto["externalId"]; !present || v != nil {
			t.Errorf("externalId = %#v (present=%v), want null", v, present)
		}
		_, out, _ = runTeamTask(t, taskResponses(), "team", "task", "get", "task1")
		if strings.Contains(out, "external id:") {
			t.Errorf("no external id must print no line:\n%s", out)
		}
	})

	t.Run("the task selection asks for it", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "get", "task1")
		if err != nil {
			t.Fatal(err)
		}
		gets := opCalls1524(calls, "AgentTaskGet")
		if len(gets) != 1 || !strings.Contains(gets[0].Query, "externalId") {
			t.Errorf("AgentTaskGet must select externalId")
		}
	})
}

func TestTaskListExternalID1524(t *testing.T) {
	t.Run("the filter is sent exactly", func(t *testing.T) {
		r := taskResponses()
		r["AgentTaskListFiltered"] = `{"data":{"agentTasks":[` + taskFieldsJSON + `]}}`
		calls, _, err := runTeamTask(t, r, "team", "task", "list", "--external-id", issueURL1524)
		if err != nil {
			t.Fatal(err)
		}
		lists := opCalls1524(calls, "AgentTaskListFiltered")
		if len(lists) != 1 {
			t.Fatalf("calls = %v, want one AgentTaskListFiltered", *calls)
		}
		if lists[0].Vars["externalId"] != issueURL1524 {
			t.Errorf("externalId var = %#v", lists[0].Vars["externalId"])
		}
	})

	t.Run("an unfiltered list sends no externalId", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "list")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if _, present := c.Vars["externalId"]; present {
				t.Errorf("%s sent externalId unasked", c.Op)
			}
		}
	})

	t.Run("it refuses --team like --source", func(t *testing.T) {
		calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "list", "--team", "Iris", "--external-id", issueURL1524)
		if err != nil && strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("the flag must exist: %v", err)
		}
		if exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
		}
		if len(opCalls1524(calls, "AgentTaskListFiltered"))+len(opCalls1524(calls, "AgentTaskList")) != 0 {
			t.Error("refused before any list call")
		}
	})
}

// ---- C2: app properties ----------------------------------------------------

const appRef1524 = "acme.com:support"

func appPropsJSON1524(props string, rev string) string {
	return `{"id":"capp1","urn":"hrn:app:acme.com:support","propertiesRevision":` + rev + `,"properties":` + props + `}`
}

func runApp1524(t *testing.T, responses map[string]string, stdin string, argv ...string) (*[]attnCall, string, error) {
	t.Helper()
	srv, calls := attnServer(t, responses)
	f, out := testFactory(t)
	if stdin != "" {
		f.IOStreams.In = strings.NewReader(stdin)
	}
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), err
}

func setResponses1524(props string) map[string]string {
	return map[string]string{"SetAppProperties": `{"data":{"setAppProperties":` + appPropsJSON1524(props, "4") + `}}`}
}

func compactEq1524(t *testing.T, got any, want string) bool {
	t.Helper()
	b, _ := json.Marshal(got)
	var w any
	_ = json.Unmarshal([]byte(want), &w)
	wb, _ := json.Marshal(w)
	return string(b) == string(wb)
}

func TestAppPropertiesSet1524(t *testing.T) {
	const obj = `{"taskIntegrations":[{"name":"github","provider":"github","repositories":["acme/app"]}],"team":{"x":1}}`

	t.Run("one call, the object verbatim, no revision key", func(t *testing.T) {
		calls, out, err := runApp1524(t, setResponses1524(obj), "", "app", "properties", "set", appRef1524, obj)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || oneCall1524(t, calls, "SetAppProperties").Op != "SetAppProperties" {
			t.Fatalf("calls = %v, want exactly one SetAppProperties", *calls)
		}
		v := oneCall1524(t, calls, "SetAppProperties").Vars
		if !compactEq1524(t, v["properties"], obj) {
			t.Errorf("properties = %v", v["properties"])
		}
		if v["appRef"] != "hrn:app:acme.com:support" {
			t.Errorf("appRef = %v", v["appRef"])
		}
		if _, present := v["expectedRevision"]; present {
			t.Errorf("no --expected-revision must send no key: %v", v)
		}
		if !strings.Contains(out, `"taskIntegrations"`) {
			t.Errorf("set prints the stored object:\n%s", out)
		}
	})

	t.Run("--expected-revision is passed through", func(t *testing.T) {
		calls, _, err := runApp1524(t, setResponses1524(obj), "", "app", "properties", "set", appRef1524, obj, "--expected-revision", "3")
		if err != nil {
			t.Fatal(err)
		}
		if oneCall1524(t, calls, "SetAppProperties").Vars["expectedRevision"] != float64(3) {
			t.Errorf("expectedRevision = %#v", oneCall1524(t, calls, "SetAppProperties").Vars["expectedRevision"])
		}
	})

	t.Run("@file and - read the value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "props.json")
		if err := os.WriteFile(path, []byte(obj), 0o600); err != nil {
			t.Fatal(err)
		}
		calls, _, err := runApp1524(t, setResponses1524(obj), "", "app", "properties", "set", appRef1524, "@"+path)
		if err != nil {
			t.Fatal(err)
		}
		if !compactEq1524(t, oneCall1524(t, calls, "SetAppProperties").Vars["properties"], obj) {
			t.Errorf("@file properties = %v", oneCall1524(t, calls, "SetAppProperties").Vars["properties"])
		}
		calls, _, err = runApp1524(t, setResponses1524(obj), obj+"\n", "app", "properties", "set", appRef1524, "-")
		if err != nil {
			t.Fatal(err)
		}
		if !compactEq1524(t, oneCall1524(t, calls, "SetAppProperties").Vars["properties"], obj) {
			t.Errorf("stdin properties = %v", oneCall1524(t, calls, "SetAppProperties").Vars["properties"])
		}
	})

	for name, value := range map[string]string{"invalid JSON": `{"a":`, "null": "null", "empty": " "} {
		t.Run(name+" is usage before any call", func(t *testing.T) {
			calls, _, err := runApp1524(t, setResponses1524(obj), "", "app", "properties", "set", appRef1524, value)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if len(*calls) != 0 {
				t.Errorf("calls = %v, want none", *calls)
			}
		})
	}

	for _, tc := range []struct {
		code, msg string
		exit      int
	}{
		{"FORBIDDEN", "only whoever manages the App", exitcode.Forbidden},
		{"TASK_INTEGRATION_INVALID", "taskIntegrations[0].provider is invalid", exitcode.Usage},
		{"APP_PROPERTIES_CONFLICT", "the properties changed since revision 3", exitcode.Conflict},
		{"PROPERTIES_KEY_RESERVED", "a key named __proto__ cannot be stored", exitcode.Usage},
		{"TASK_INTEGRATION_ORG_REQUIRED", "this App has no organization", exitcode.Conflict},
	} {
		t.Run(tc.code+" maps with serverCode", func(t *testing.T) {
			r := map[string]string{"SetAppProperties": `{"errors":[{"message":"` + tc.msg + `","extensions":{"code":"` + tc.code + `"}}]}`}
			_, _, err := runApp1524(t, r, "", "app", "properties", "set", appRef1524, obj)
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), tc.exit, err)
			}
			if _, env := envelope1524(t, err); env["serverCode"] != tc.code {
				t.Errorf("serverCode = %v, want %s", env["serverCode"], tc.code)
			}
		})
	}
}

func TestAppPropertiesClear1524(t *testing.T) {
	t.Run("non-TTY without --yes refuses before any call", func(t *testing.T) {
		calls, _, err := runApp1524(t, setResponses1524("null"), "", "app", "properties", "clear", appRef1524)
		if exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
		}
		if len(*calls) != 0 {
			t.Errorf("calls = %v, want none", *calls)
		}
	})

	t.Run("--yes sends an explicit null", func(t *testing.T) {
		calls, _, err := runApp1524(t, setResponses1524("null"), "", "app", "properties", "clear", appRef1524, "--yes", "--expected-revision", "7")
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || oneCall1524(t, calls, "SetAppProperties").Op != "SetAppProperties" {
			t.Fatalf("calls = %v", *calls)
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(oneCall1524(t, calls, "SetAppProperties").RawVars, &raw)
		if p, present := raw["properties"]; !present || string(p) != "null" {
			t.Errorf("properties must be sent as an explicit null: %s", oneCall1524(t, calls, "SetAppProperties").RawVars)
		}
		if string(raw["expectedRevision"]) != "7" {
			t.Errorf("expectedRevision = %s", raw["expectedRevision"])
		}
	})
}

func TestAppPropertiesGet1524(t *testing.T) {
	const obj = `{"b":2,"a":{"c":[1,2]}}`
	get := func(props, rev string) map[string]string {
		return map[string]string{"AppPropertiesGet": `{"data":{"app":` + appPropsJSON1524(props, rev) + `}}`}
	}

	t.Run("text is the object alone, as valid JSON", func(t *testing.T) {
		calls, out, err := runApp1524(t, get(obj, "2"), "", "app", "properties", "get", appRef1524)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 {
			t.Errorf("calls = %d, want 1", len(*calls))
		}
		var back any
		if err := json.Unmarshal([]byte(out), &back); err != nil {
			t.Fatalf("text output must be a working JSON file: %v\n%s", err, out)
		}
		if !compactEq1524(t, back, obj) {
			t.Errorf("round trip = %s", out)
		}
	})

	t.Run("json carries the id and revision", func(t *testing.T) {
		_, out, err := runApp1524(t, get(obj, "2"), "", "app", "properties", "get", appRef1524, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatal(err)
		}
		if dto["appId"] != "capp1" || dto["propertiesRevision"] != float64(2) || !compactEq1524(t, dto["properties"], obj) {
			t.Errorf("dto = %v", dto)
		}
	})

	t.Run("null prints null", func(t *testing.T) {
		_, out, err := runApp1524(t, get("null", "null"), "", "app", "properties", "get", appRef1524)
		if err != nil {
			t.Fatal(err)
		}
		if out != "null\n" {
			t.Errorf("out = %q, want null", out)
		}
		_, out, _ = runApp1524(t, get("null", "null"), "", "app", "properties", "get", appRef1524, "--json")
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		for _, k := range []string{"properties", "propertiesRevision"} {
			if v, present := dto[k]; !present || v != nil {
				t.Errorf("%s = %#v (present=%v), want null", k, v, present)
			}
		}
	})

	t.Run("no App is not found", func(t *testing.T) {
		_, _, err := runApp1524(t, map[string]string{"AppPropertiesGet": `{"data":{"app":null}}`}, "", "app", "properties", "get", appRef1524)
		if exitCodeFor(err) != exitcode.NotFound {
			t.Errorf("exit = %d, want %d", exitCodeFor(err), exitcode.NotFound)
		}
	})
}

func TestAppGetShowsProperties1524(t *testing.T) {
	r := map[string]string{"GetAppMetadata": `{"data":{"app":{"id":"capp1","urn":"hrn:app:acme.com:support","name":"Support","appType":"TEAM","description":null,"propertiesRevision":2,"properties":{"a":1}}}}`}
	_, out, err := runApp1524(t, r, "", "app", "get", appRef1524, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out), &dto)
	if !compactEq1524(t, dto["properties"], `{"a":1}`) {
		t.Errorf("app get --json properties = %v\n%s", dto["properties"], out)
	}
	_, out, err = runApp1524(t, r, "", "app", "get", appRef1524)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "properties:\n") || !strings.Contains(out, `"a": 1`) {
		t.Errorf("app get text must show the properties block:\n%s", out)
	}
}
