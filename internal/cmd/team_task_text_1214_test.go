package cmd

import (
	"encoding/json"
	"fmt"
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

// Read refusals must fail before the unfiltered ancestry scan. Other tests
// prove that the successful intersection still uses that complete ancestry.
func TestTeamTextRefusalBeforeAncestry1214(t *testing.T) {
	calls, out, err := textPages1214(t, [][]task1044{{{id: "anchor", owner: worker1044("Marco")}}}, []string{memberGQLError("text must contain 1 to 200 characters", "BAD_USER_INPUT")}, "   ")
	if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "BAD_USER_INPUT") || out != "" {
		t.Fatalf("refusal: %v output %q", err, out)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" {
			t.Fatal("invalid text scanned unfiltered ancestry before the server refused it")
		}
	}
}

func TestTeamTextPagedIntersectionAndFailures1214(t *testing.T) {
	anchor := task1044{id: "anchor", status: "STARTED", owner: worker1044("Marco")}
	child := task1044{id: "child", status: "STARTED", owner: worker1044("Cameron"), parent: "anchor"}
	noise := []task1044{}
	for i := 0; i < 200; i++ {
		noise = append(noise, task1044{id: fmt.Sprintf("noise-%d", i), owner: user1044})
	}
	all := append([]task1044{anchor}, noise...)
	all = append(all, child)
	for _, tc := range []struct {
		name      string
		responses []string
		code      int
	}{
		{"two pages", []string{taskPage1044(noise), taskPage1044([]task1044{child})}, 0},
		{"later error", []string{taskPage1044(noise), memberGQLError("text read unavailable", "INTERNAL_SERVER_ERROR")}, 1},
		{"non advancing", []string{taskPage1044(noise), taskPage1044(noise)}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := textPages1214(t, [][]task1044{all[:200], all[200:]}, tc.responses, "needle")
			if tc.code != 0 {
				if err == nil || exitCodeFor(err) != tc.code || out != "" {
					t.Fatalf("failure %v output %q", err, out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Tasks []struct {
					ID    string `json:"id"`
					Match string `json:"match"`
				} `json:"tasks"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Tasks) != 1 || result.Tasks[0].ID != "child" || result.Tasks[0].Match != "descendant" {
				t.Fatalf("intersection: %s", out)
			}
			offsets := []float64{}
			for _, c := range *calls {
				if c.Op == "AgentTaskListText" {
					if c.Vars["text"] != "needle" {
						t.Fatal("lost text on a later page")
					}
					offsets = append(offsets, c.Vars["offset"].(float64))
				}
			}
			if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 200 {
				t.Fatalf("offsets %v", offsets)
			}
		})
	}
}

func textPages1214(t *testing.T, base [][]task1044, text []string, value string) (*[]attnCall, string, error) {
	t.Helper()
	writeTeamBinding(t)
	responses := rosterResponses993(roster1044()...)
	responses["TeamReportingLines"] = responses["TeamRoster"]
	responses["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[]}}`
	b, n := 0, 0
	srv, calls := attnServerHook(t, responses, func(op string) {
		switch op {
		case "AgentTaskList":
			r := `{"data":{"agentTasks":[]}}`
			if b < len(base) {
				r = taskPage1044(base[b])
			}
			responses[op] = r
			b++
		case "AgentTaskListText":
			r := `{"data":{"agentTasks":[]}}`
			if n < len(text) {
				r = text[n]
			}
			responses[op] = r
			n++
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "list", "--team=worker:Marco", "--text", value, "--json", "--server", srv.URL})
	err := root.Execute()
	return calls, out.String(), err
}
