package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
			var submitted []map[string]any
			if err := json.Unmarshal([]byte(body), &submitted); err != nil {
				t.Fatal(err)
			}
			returned := make([]map[string]any, 0, len(submitted))
			for i, row := range submitted {
				id, ok := row["id"]
				if !ok || id == nil {
					id = fmt.Sprintf("F%d", i+1)
				}
				returned = append(returned, map[string]any{"id": id, "severity": row["severity"], "text": row["text"]})
			}
			page := string(mustJSON1550(t, map[string]any{"items": returned, "total": len(returned), "hasMore": false}))
			calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, page)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, body), "--json")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains((*calls)[0].Query, "findings(limit: 20, offset: 0)") {
				t.Fatalf("finish page must request 20 at zero: %s", (*calls)[0].Query)
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
			for _, v := range want.([]any) {
				m := v.(map[string]any)
				if _, ok := m["id"]; !ok {
					m["id"] = nil
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("wire findings changed: got%#v want%#v", got, want)
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
			if json.Unmarshal([]byte(out), &dto) != nil || dto.Result.Findings.Items == nil || !reflect.DeepEqual(dto.Result.Findings.Items, mustDecode1550(t, mustJSON1550(t, returned))) {
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

func TestTaskFindingsMalformedRead1550(t *testing.T) {
	for i, page := range []string{`null`, `{}`, `{"total":0,"hasMore":false,"items":null}`, `{"total":1,"hasMore":false,"items":[null]}`, `{"total":1,"hasMore":false,"items":[{"id":"F1","severity":"P1"}]}`} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": `{"data":{"agentTask":{"id":"task1","result":{"findings":` + page + `}}}}`}, "team", "task", "findings", "AT0.7")
			if err == nil || len(*calls) != 1 {
				t.Fatalf("malformed page accepted: %v", err)
			}
		})
	}
	for _, flag := range []string{"--limit", "--offset"} {
		for _, value := range []string{"-1", "2147483648"} {
			calls, _, err := runTeamTask(t, map[string]string{}, "team", "task", "findings", "AT0.7", flag, value)
			if err == nil || exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("bad page input accepted: %v", err)
			}
		}
	}
}

func TestTaskFinishFindingsUncertainty1550(t *testing.T) {
	for _, reply := range []string{`{"data":{"finishAgentTask":null}}`, `{"data":{"finishAgentTask":{}}}`, finishReply1550(t, `null`), `{"errors":[{"message":"lost answer","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`} {
		calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": reply}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`))
		if err == nil || !strings.Contains(err.Error(), "may have changed") || len(*calls) != 1 || out != "" {
			t.Fatalf("uncertain write err%v calls%v out%q", err, *calls, out)
		}
	}
}

func TestTaskFindingsContinuationAndSafeText1550(t *testing.T) {
	text := "SENTINEL\x1b[31mred\r\u202e\nsecond"
	page := map[string]any{"items": []any{map[string]any{"id": "F1", "severity": "P1", "text": text}}, "total": 25, "hasMore": true}
	raw := string(mustJSON1550(t, page))
	reply := `{"data":{"agentTask":{"id":"task1","result":{"findings":` + raw + `}}}}`
	calls, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": reply}, "team", "task", "findings", "AT0.7", "--offset", "7")
	if err != nil || len(*calls) != 1 || !strings.Contains(out, "--offset 8") || strings.ContainsAny(out, "\x1b\r\u202e") || !strings.Contains(out, "\n    second") {
		t.Fatalf("unsafe/bad continuation: %v %q", err, out)
	}
	_, out, err = runTeamTask(t, map[string]string{"AgentTaskResultFindings": reply}, "team", "task", "findings", "AT0.7", "--json")
	var dto struct {
		Findings struct {
			Items []struct {
				Text string `json:"text"`
			} `json:"items"`
		} `json:"findings"`
	}
	if err != nil || json.Unmarshal([]byte(out), &dto) != nil || len(dto.Findings.Items) != 1 || dto.Findings.Items[0].Text != text {
		t.Fatalf("raw JSON changed: %v %s", err, out)
	}
	for _, limit := range []string{"0", "20"} {
		reply := `{"data":{"agentTask":{"id":"task1","result":{"findings":{"items":[],"total":25,"hasMore":true}}}}}`
		_, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": reply}, "team", "task", "findings", "AT0.7", "--limit", limit)
		if err != nil || strings.Contains(out, "continue:") || !strings.Contains(out, map[string]string{"0": "count-only", "20": "no progress"}[limit]) {
			t.Fatalf("empty page progress invented: %v %q", err, out)
		}
	}
}

func mustDecode1550(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The source boundary runs before client creation and never echoes file contents.
func TestTaskFindingsSource1550(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		tty              bool
		code             int
	}{
		{"pipe", "-", "[]", false, 0}, {"interactive", "-", "[]", true, 2},
		{"empty", "", "[]", false, 2}, {"unreadable", "SENTINEL-missing", "", false, 2},
		{"too large", "FILE", "[]" + strings.Repeat(" ", 4*1024*1024), false, 2},
		{"boundary", "FILE", "[]" + strings.Repeat(" ", 4*1024*1024-2), false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)})
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			if tc.tty {
				f, out, _ = testFactoryTTY(t, tc.body)
			} else {
				f.IOStreams.In = strings.NewReader(tc.body)
			}
			path := tc.path
			if path == "FILE" {
				path = findingsFile1550(t, tc.body)
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", path, "--json"})
			err := root.Execute()
			if tc.code == 0 {
				if err != nil || len(*calls) != 1 || out.Len() == 0 {
					t.Fatalf("success err%v calls%d out%q", err, len(*calls), out.String())
				}
			} else if err == nil || exitCodeFor(err) != tc.code || len(*calls) != 0 || out.Len() != 0 || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("refusal err%v calls%d out%q", err, len(*calls), out.String())
			}
		})
	}
}

type failingFindingsWriter1550 struct{}

func (failingFindingsWriter1550) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestTaskFindingsWriter1550(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, finish := range []bool{false, true} {
			t.Run(fmt.Sprintf("json%t/finish%t", jsonMode, finish), func(t *testing.T) {
				writeTeamBinding(t)
				srv, calls := attnServer(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`), "AgentTaskResultFindings": `{"data":{"agentTask":{"id":"t","result":null}}}`})
				setTeamBindingServer(t, srv.URL)
				f, _ := testFactory(t)
				f.IOStreams.Out = failingFindingsWriter1550{}
				args := []string{"--server", srv.URL, "team", "task", "findings", "AT0.7"}
				if finish {
					args = []string{"--server", srv.URL, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`)}
				}
				if jsonMode {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || len(*calls) != 1 {
					t.Fatalf("write error lost/retried err%v calls%d", err, len(*calls))
				}
			})
		}
	}
}

func TestTaskFindingsRefusalCodes1550(t *testing.T) {
	for code, want := range map[string]int{"BAD_USER_INPUT": 2, "GRAPHQL_VALIDATION_FAILED": 2, "TASK_NOT_FOUND": 4, "TASK_NOT_ASSIGNEE": 8, "FORBIDDEN": 8, "TASK_ENDED": 5, "TASK_INVALID_TRANSITION": 5, "UNAUTHENTICATED": 3, "INTERNAL_SERVER_ERROR": 1} {
		for _, finish := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/finish%t", code, finish), func(t *testing.T) {
				op := "AgentTaskResultFindings"
				args := []string{"team", "task", "findings", "AT0.7"}
				if finish {
					op = "AgentTaskFinishWithFindings"
					args = []string{"team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`)}
				}
				calls, out, err := runTeamTask(t, map[string]string{op: gqlErrorJSON(code)}, args...)
				if err == nil || exitCodeFor(err) != want || len(*calls) != 1 || out != "" {
					t.Fatalf("code %s err%v want%d calls%d out%q", code, err, want, len(*calls), out)
				}
				var coded interface{ ServerCode() string }
				if !errors.As(err, &coded) || coded.ServerCode() != code {
					t.Fatalf("server code lost on %v want%q", err, code)
				}
			})
		}
	}
}

func TestTaskFinishFindingsWarnings1550(t *testing.T) {
	reply := finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)
	reply = strings.TrimSuffix(reply, "}") + `,"extensions":{"warnings":` + c962Warnings("finishAgentTask") + `}}`
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			args := []string{"team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`)}
			if jsonMode {
				args = append(args, "--json")
			}
			out, stderr, err := runTeamTaskStreams(t, map[string]string{"AgentTaskFinishWithFindings": reply}, args...)
			if err != nil {
				t.Fatal(err)
			}
			if jsonMode {
				var dto struct {
					Warnings []any `json:"warnings"`
				}
				if json.Unmarshal([]byte(out), &dto) != nil || len(dto.Warnings) != 2 || stderr != "" {
					t.Fatalf("warnings lost %s %q", out, stderr)
				}
			} else if !strings.Contains(stderr, "task.future.unknown") || !strings.Contains(out, "finished task") {
				t.Fatalf("warnings lost %s %q", out, stderr)
			}
		})
	}
}

func TestTaskFinishFindingsFirstPage1550(t *testing.T) {
	input := make([]map[string]any, 21)
	served := make([]map[string]any, 20)
	for i := range input {
		input[i] = map[string]any{"severity": "note", "text": fmt.Sprintf("entry %d", i)}
		if i < 20 {
			served[i] = map[string]any{"id": fmt.Sprintf("F%d", i+1), "severity": "note", "text": fmt.Sprintf("entry %d", i)}
		}
	}
	body := string(mustJSON1550(t, input))
	page := string(mustJSON1550(t, map[string]any{"items": served, "total": 21, "hasMore": true}))
	calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, page)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, body))
	if err != nil || len(*calls) != 1 || !strings.Contains(out, "shown 20 / total 21; hasMore true") || !strings.Contains(out, "--offset 20") || strings.Contains(out, "entry 20") {
		t.Fatalf("page/drain error %v calls%d out%q", err, len(*calls), out)
	}
	for _, offset := range []string{"0", "20", "21"} {
		rows := []map[string]any{}
		more := false
		if offset == "0" {
			rows = served
			more = true
		}
		if offset == "20" {
			rows = []map[string]any{{"id": "F21", "severity": "note", "text": "entry 20"}}
		}
		p := string(mustJSON1550(t, map[string]any{"items": rows, "total": 21, "hasMore": more}))
		calls, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": `{"data":{"agentTask":{"id":"task1","result":{"findings":` + p + `}}}}`}, "team", "task", "findings", "AT0.7", "--offset", offset, "--json")
		if err != nil || len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(20) {
			t.Fatalf("explicit page %s err%v calls%v out%q", offset, err, *calls, out)
		}
	}
}
