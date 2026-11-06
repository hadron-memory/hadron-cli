package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findingsFile1550(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "findings.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func finishReply1550(t *testing.T, page string) string {
	t.Helper()
	var task map[string]any
	if err := json.Unmarshal([]byte(finishedTaskFieldsJSON), &task); err != nil {
		t.Fatal(err)
	}
	var p any
	if err := json.Unmarshal([]byte(page), &p); err != nil {
		t.Fatal(err)
	}
	task["findingsResult"] = map[string]any{"findings": p}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"finishAgentTask": task}})
	return string(b)
}

func TestTaskFinishFindings1550(t *testing.T) {
	for _, body := range []string{`[]`, `[{"severity":"P1","text":" a\n😀 "}]`, `[{"id":"Mine","severity":"note","text":""}]`} {
		t.Run(body, func(t *testing.T) {
			page := `{"items":[],"total":0,"hasMore":false}`
			calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, page)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, body), "--json")
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Op != "AgentTaskFinishWithFindings" {
				t.Fatalf("calls%v", *calls)
			}
			var vars map[string]json.RawMessage
			if json.Unmarshal(mustJSON1550(t, (*calls)[0].Vars), &vars) != nil {
				t.Fatal("variables")
			}
			var want, got any
			_ = json.Unmarshal([]byte(body), &want)
			_ = json.Unmarshal(vars["findings"], &got)
			// Absent/null ids are represented equivalently; no client ids are generated.
			if len(body) == 2 && string(vars["findings"]) != "[]" {
				t.Fatalf("empty input lost: %s", vars["findings"])
			}
			if got == nil || want == nil {
				t.Fatalf("findings absent/null: %s", vars["findings"])
			}
			var dto struct {
				Result struct {
					Findings struct {
						Items   []any `json:"items"`
						Total   int   `json:"total"`
						HasMore bool  `json:"hasMore"`
					} `json:"findings"`
				} `json:"result"`
			}
			if json.Unmarshal([]byte(out), &dto) != nil || dto.Result.Findings.Items == nil {
				t.Fatalf("page envelope lost: %s", out)
			}
		})
	}
}

func TestTaskFindingsRead1550(t *testing.T) {
	for _, limit := range []string{"0", "20", "200", "201", "2147483647"} {
		t.Run(limit, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": `{"data":{"agentTask":{"id":"task1","result":{"findings":{"items":[],"total":21,"hasMore":true}}}}}`}, "team", "task", "findings", "AT0.7", "--limit", limit, "--offset", "3", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Op != "AgentTaskResultFindings" {
				t.Fatalf("calls%v", *calls)
			}
			var vars map[string]json.RawMessage
			_ = json.Unmarshal(mustJSON1550(t, (*calls)[0].Vars), &vars)
			if string(vars["limit"]) != limit || string(vars["offset"]) != "3" {
				t.Fatalf("paging %s", (*calls)[0].Vars)
			}
			if !strings.Contains(out, `"total": 21`) || !strings.Contains(out, `"hasMore": true`) {
				t.Fatalf("page lost %s", out)
			}
		})
	}
	for _, result := range []string{`null`, `{"findings":{"items":[],"total":0,"hasMore":false}}`} {
		_, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": `{"data":{"agentTask":{"id":"task1","result":` + result + `}}}`}, "team", "task", "findings", "AT0.7", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if (result == "null") != strings.Contains(out, `"findings": null`) {
			t.Fatalf("null/empty collapsed: %s", out)
		}
	}
}

func TestTaskFindingsLocalRefusals1550(t *testing.T) {
	bodies := []string{`null`, `{}`, `[null]`, `[{"severity":"p1","text":"SENTINEL"}]`, `[{"severity":"P1"}]`, `[{"severity":"P1","text":null}]`, `[{"severity":"P1","text":"x","extra":0}]`, `[{"severity":"P1","text":"x","text":"SENTINEL"}]`, `[{"severity":"P1","text":"\ud800"}]`, `[{"severity":"P1","text":"\udc00"}]`, `[{"severity":"P1","text":"\u0000"}]`, `[] {}`, `[{"id":"","severity":"P1","text":"x"}]`, `[{"id":"A","severity":"P1","text":"x"},{"severity":"P1","text":"x"}]`, `[{"id":"A","severity":"P1","text":"x"},{"id":"A","severity":"P1","text":"x"}]`, "[\xff]"}
	bodies = append(bodies, `[{"severity":"P1","text":`+fmt.Sprintf("%q", strings.Repeat("😀", 2001))+`}]`)
	entries := strings.Repeat(`{"severity":"P1","text":"x"},`, 101)
	bodies = append(bodies, "["+strings.TrimSuffix(entries, ",")+"]")
	for i, body := range bodies {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, body))
			if err == nil || exitCodeFor(err) != 2 || strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "SENTINEL") || len(*calls) != 0 || out != "" {
				t.Fatalf("local refusal err%v calls%v out%q", err, *calls, out)
			}
		})
	}
}

func mustJSON1550(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
