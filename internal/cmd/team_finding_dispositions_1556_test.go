package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func dispositionResponse1556(code string, ref any) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"disposeAgentTaskFinding": map[string]any{
		"id": "task1", "findingDispositions": map[string]any{"total": 1, "hasMore": false, "items": []any{map[string]any{"findingId": "F1", "disposition": code, "reference": ref}}},
	}}})
	return string(b)
}

func TestFindingDispositions1556Write(t *testing.T) {
	for _, code := range []string{"fixed", "accepted", "deferred", "rejected"} {
		t.Run(code, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = dispositionResponse1556(code, nil)
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "AT1.2", "F1", "--disposition", code, "--json")
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Op != "AgentTaskDisposeFinding" {
				t.Fatalf("calls=%v", *calls)
			}
			v := (*calls)[0].Vars
			if v["taskRef"] != "AT1.2" || v["findingId"] != "F1" || v["disposition"] != code {
				t.Fatalf("vars=%v", v)
			}
			if _, ok := v["reference"]; ok {
				t.Fatal("omitted reference sent")
			}
			if out != fmt.Sprintf("{\"taskId\":\"task1\",\"findingId\":\"F1\",\"disposition\":%q,\"reference\":null}\n", code) {
				t.Fatalf("receipt=%q", out)
			}
		})
	}
}

func TestFindingDispositions1556Read(t *testing.T) {
	r := taskResponses()
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":{"items":[],"total":12,"hasMore":true}}}}`
	calls, out, err := runTeamTask(t, r, "team", "task", "finding-dispositions", "AT1.2", "--limit", "2", "--offset", "4", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if out != "{\"items\":[],\"total\":12,\"hasMore\":true}\n" {
		t.Fatalf("page=%q", out)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(2) || (*calls)[0].Vars["offset"] != float64(4) {
		t.Fatalf("calls=%v", *calls)
	}
}

func TestFindingDispositions1556Unresolved(t *testing.T) {
	for _, val := range []string{"true", "false"} {
		r := taskResponses()
		r["AgentTaskListUnresolved"] = `{"data":{"agentTasks":[]}}`
		calls, out, err := runTeamTask(t, r, "team", "task", "list", "--unresolved="+val, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if out != "[]\n" {
			t.Fatalf("out=%q", out)
		}
		found := false
		for _, c := range *calls {
			if c.Op == "AgentTaskListUnresolved" {
				found = true
				if c.Vars["unresolvedFindings"] != (val == "true") {
					t.Fatalf("vars=%v", c.Vars)
				}
			}
		}
		if !found {
			t.Fatalf("calls=%v", *calls)
		}
	}
}

func TestFindingDispositions1556Refusals(t *testing.T) {
	for code, want := range map[string]int{"TASK_NOT_OWNER": 8, "TASK_NOT_FINISHED": 5, "TASK_FINDING_NOT_FOUND": 4} {
		t.Run(code, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = fmt.Sprintf(`{"errors":[{"message":"Refused","extensions":{"code":%q}}]}`, code)
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed")
			if exitCodeFor(err) != want || len(*calls) != 1 || out != "" || !strings.Contains(err.Error(), code) {
				t.Fatalf("err=%v out=%q calls=%v", err, out, *calls)
			}
		})
	}
}
