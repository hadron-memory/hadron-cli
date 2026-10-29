package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestTaskListText1214(t *testing.T) {
	for _, text := range []string{"needle", "", "   ", strings.Repeat("x", 201), `%_\\ literal`} {
		t.Run(text, func(t *testing.T) {
			response := `{"data":{"agentTasks":[]}}`
			invalid := strings.TrimSpace(text) == "" || len(text) > 200
			if invalid {
				response = memberGQLError("text must contain 1 to 200 characters", "BAD_USER_INPUT")
			}
			srv, captured := captureGraphQL(t, map[string]string{"AgentTaskListText": response})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "task", "list", "--app", "capp100000000000000000000", "--text", text, "--status", "PENDING", "--tag", "release", "--json", "--server", srv.URL})
			err := root.Execute()
			if invalid {
				if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "BAD_USER_INPUT") {
					t.Fatalf("server refusal not surfaced: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if out.String() != "[]\n" {
				t.Fatalf("output changed: %q", out.String())
			}
			var vars map[string]any
			raw, ok := captured["AgentTaskListText"]
			if !ok {
				t.Fatal("no request")
			}
			if err := json.Unmarshal(raw, &vars); err != nil {
				t.Fatal(err)
			}
			if vars["text"] != text || vars["tag"] != "release" {
				t.Fatalf("lost filters: %v", vars)
			}
			status, ok := vars["status"].([]any)
			if !ok || len(status) != 1 || status[0] != "PENDING" {
				t.Fatal(vars)
			}
		})
	}
}

func TestTaskListTextOmitted1214(t *testing.T) {
	srv, captured := captureGraphQL(t, map[string]string{"AgentTaskList": `{"data":{"agentTasks":[]}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"team", "task", "list", "--app", "capp100000000000000000000", "--json", "--server", srv.URL})
	if e := r.Execute(); e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	raw, ok := captured["AgentTaskList"]
	if !ok {
		t.Fatal("missing request")
	}
	if e := json.Unmarshal(raw, &vars); e != nil {
		t.Fatal(e)
	}
	if _, ok := vars["text"]; ok {
		t.Fatal("omitted flag sent text")
	}
	if out.String() != "[]\n" {
		t.Fatalf("omitted output %q", out.String())
	}
}

func TestTaskTeamTextPreservesDescendantJoin1214(t *testing.T) {
	anchor := task1044{id: "anchor", owner: worker1044("Marco")}
	child := task1044{id: "child", owner: worker1044("Cameron"), parent: "anchor"}
	calls, out, _, err := runText1214(t, roster1044(), [][]task1044{{anchor, child}, {child}}, "team", "task", "list", "--team=worker:Marco", "--text", "needle", "--status", "STARTED", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tasks []struct {
			ID    string `json:"id"`
			Match string `json:"match"`
		} `json:"tasks"`
	}
	if e := json.Unmarshal([]byte(out), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Tasks) != 1 || result.Tasks[0].ID != "child" || result.Tasks[0].Match != "descendant" {
		t.Fatalf("lost descendant: %s", out)
	}
	n := 0
	for _, c := range *calls {
		if c.Op != "AgentTaskList" && c.Op != "AgentTaskListText" {
			continue
		}
		n++
		if n == 1 {
			if _, ok := c.Vars["text"]; ok {
				t.Fatal("join read was filtered")
			}
		} else if c.Vars["text"] != "needle" {
			t.Fatal("missing server text filter")
		}
	}
	if n != 2 {
		t.Fatalf("task reads %d", n)
	}
}

// The pinned schema adds this unrelated nullable input. Never introduce an
// explicit null into an existing settings write while refreshing the schema.
func TestTaskTextSchemaRefreshPreservesSettingsOmission1214(t *testing.T) {
	b, err := json.Marshal(gen.AppDeliverySettingsInput{})
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := json.Unmarshal(b, &vars); err != nil {
		t.Fatal(err)
	}
	if _, present := vars["darkAlarmWindowSeconds"]; present {
		t.Fatal("schema refresh sends an unselected nullable setting")
	}
}

func runText1214(t *testing.T, roster []string, pages [][]task1044, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	responses := rosterResponses993(roster...)
	responses["TeamReportingLines"] = responses["TeamRoster"]
	responses["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[]}}`
	responses["AgentTaskList"] = taskPage1044(pages[0])
	responses["AgentTaskListText"] = taskPage1044(pages[1])
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}
func TestTaskTextLegacyDocumentUnchanged1214(t *testing.T) {
	if strings.Contains(gen.AgentTaskList_Operation, "$text") {
		t.Fatal("legacy document gained a new server argument")
	}
	if !strings.Contains(gen.AgentTaskListText_Operation, "text: $text") {
		t.Fatal("text operation does not apply its filter")
	}
}
