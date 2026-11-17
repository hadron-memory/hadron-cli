package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
			var compact bytes.Buffer
			if e := json.Compact(&compact, []byte(out)); e != nil {
				t.Fatal(e)
			}
			if compact.String()+"\n" != fmt.Sprintf("{\"taskId\":\"task1\",\"findingId\":\"F1\",\"disposition\":%q,\"reference\":null}\n", code) {
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
	var compact bytes.Buffer
	if e := json.Compact(&compact, []byte(out)); e != nil {
		t.Fatal(e)
	}
	if compact.String() != "{\"items\":[],\"total\":12,\"hasMore\":true}" {
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

func TestFindingDispositions1556ReferenceAndServerReceipt(t *testing.T) {
	label := "danger\x1b]52;c;secret\a\nnext"
	ref := map[string]any{"kind": "URL", "target": "https://example.invalid/ref\x1b]52;c;target\a\tX", "label": label}
	for _, jsonMode := range []bool{true, false} {
		r := taskResponses()
		r["AgentTaskDisposeFinding"] = dispositionResponse1556("accepted", ref)
		args := []string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", "url=https://example.invalid/input|raw\tlabel"}
		if jsonMode {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, r, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 {
			t.Fatalf("unexpected fetch/retry: %v", *calls)
		}
		v := (*calls)[0].Vars["reference"].(map[string]any)
		if v["kind"] != "URL" || v["target"] != "https://example.invalid/input" || v["label"] != "raw\tlabel" {
			t.Fatalf("ref=%v", v)
		}
		if jsonMode {
			var got map[string]any
			if e := json.Unmarshal([]byte(out), &got); e != nil {
				t.Fatal(e)
			}
			if got["disposition"] != "accepted" || got["reference"].(map[string]any)["label"] != label {
				t.Fatalf("server receipt lost: %s", out)
			}
		} else if strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, "accepted") || strings.Contains(out, "fixed") {
			t.Fatalf("unsafe/guessed receipt %q", out)
		}
	}
	r := taskResponses()
	r["AgentTaskDisposeFinding"] = dispositionResponse1556("fixed", nil)
	calls, _, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", "issue=https://example.invalid/1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := (*calls)[0].Vars["reference"].(map[string]any)["label"]; ok {
		t.Fatal("unset label not omitted")
	}
}

func TestFindingDispositions1556BadArgsAndMissingReadback(t *testing.T) {
	for _, args := range [][]string{
		{"dispose-finding", "task1", "F1"},
		{"dispose-finding", "task1", "F1", "--disposition", ""},
		{"dispose-finding", "task1", "F1", "--disposition", "unknown"},
		{"dispose-finding", "task1", " ", "--disposition", "fixed"},
		{"dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", ""},
		{"finding-dispositions", "task1", "--limit", "201"},
		{"finding-dispositions", "task1", "--offset", "-1"},
		{"list", "--team", "--unresolved"},
		{"list", "--team=worker:Iris", "--unresolved=false"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task"}, args...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Errorf("args=%v err=%v calls=%v", args, err, *calls)
		}
	}
	for _, body := range []string{`{"data":{"disposeAgentTaskFinding":null}}`, `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDispositions":null}}}`, `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDispositions":{"items":[],"total":0,"hasMore":false}}}}`} {
		r := taskResponses()
		r["AgentTaskDisposeFinding"] = body
		calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json")
		if exitCodeFor(err) != 1 || out != "" || len(*calls) != 1 {
			t.Fatalf("fabricated success/retry: %v %q %v", err, out, *calls)
		}
	}
}

func TestFindingDispositions1556ReadOrderAndNull(t *testing.T) {
	r := taskResponses()
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":{"items":[{"findingId":"F2","disposition":"deferred","reference":null},{"findingId":"F1","disposition":"fixed","reference":null}],"total":7,"hasMore":true}}}}`
	_, out, err := runTeamTask(t, r, "team", "task", "finding-dispositions", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []struct {
			FindingID string `json:"findingId"`
		}
		Total   int
		HasMore bool
	}
	if err = json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].FindingID != "F2" || page.Total != 7 || !page.HasMore {
		t.Fatalf("page changed %s", out)
	}
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":null}}`
	_, out, err = runTeamTask(t, r, "team", "task", "finding-dispositions", "task1")
	if exitCodeFor(err) != 4 || out != "" {
		t.Fatalf("missing task %v %q", err, out)
	}
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":null}}}`
	_, out, err = runTeamTask(t, r, "team", "task", "finding-dispositions", "task1")
	if exitCodeFor(err) != 1 || out != "" {
		t.Fatalf("null page %v %q", err, out)
	}
}

func TestFindingDispositions1556OmittedFilterKeepsLegacy(t *testing.T) {
	for _, args := range [][]string{{"list", "--json"}, {"list", "--match-failed", "--json"}} {
		r := taskResponses()
		r["AgentTaskListFiltered"] = `{"data":{"agentTasks":[]}}`
		calls, _, err := runTeamTask(t, r, append([]string{"team", "task"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if strings.Contains(c.Query, "unresolvedFindings") {
				t.Fatal("omitted flag raised old document floor")
			}
			if _, ok := c.Vars["unresolvedFindings"]; ok {
				t.Fatal("omitted filter sent")
			}
		}
	}
}

func TestFindingDispositions1556RealErrorSink(t *testing.T) {
	for code, want := range map[string]int{"TASK_NOT_OWNER": 8, "TASK_NOT_FINISHED": 5, "TASK_FINDING_NOT_FOUND": 4} {
		for _, jsonMode := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", code, jsonMode), func(t *testing.T) {
				writeTeamBinding(t)
				r := taskResponses()
				r["AgentTaskDisposeFinding"] = fmt.Sprintf(`{"errors":[{"message":"refused hostile\u001b[31m","extensions":{"code":%q}}]}`, code)
				srv, calls := attnServer(t, r)
				setTeamBindingServer(t, srv.URL)
				f, out := testFactory(t)
				stderr := captureErrOut(f)
				args := []string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--server", srv.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				renderFailure(f, args, err)
				if exitCodeFor(err) != want || len(*calls) != 1 {
					t.Fatalf("err=%v calls=%v", err, *calls)
				}
				if jsonMode {
					var env struct {
						Error struct {
							Code       int    `json:"code"`
							ServerCode string `json:"serverCode"`
						}
					}
					if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
						t.Fatal(e)
					}
					if env.Error.Code != want || env.Error.ServerCode != code {
						t.Fatalf("envelope=%s", out.String())
					}
				} else if out.Len() != 0 || !strings.Contains(stderr(), code) || strings.Contains(stderr(), "\x1b") {
					t.Fatalf("human=%q stdout=%q", stderr(), out.String())
				}
			})
		}
	}
}

func TestFindingDispositions1556CountOnlyAndCompoundFilter(t *testing.T) {
	r := taskResponses()
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":{"items":[],"total":100,"hasMore":true}}}}`
	calls, _, err := runTeamTask(t, r, "team", "task", "finding-dispositions", "task1", "--limit", "0", "--offset", "3")
	if err != nil || len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(0) {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
	r["AgentTaskListUnresolved"] = `{"data":{"agentTasks":[]}}`
	calls, _, err = runTeamTask(t, r, "team", "task", "list", "--unresolved=false", "--status", "FINISHED", "--owner", "user:alex", "--text", "raw text", "--limit", "2", "--offset", "4", "--json")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range *calls {
		if c.Op == "AgentTaskListUnresolved" {
			found = true
			if c.Vars["unresolvedFindings"] != false || c.Vars["text"] != "raw text" || c.Vars["offset"] != float64(4) || c.Vars["owner"].(map[string]any)["user"] != "alex" {
				t.Fatalf("vars=%v", c.Vars)
			}
		}
	}
	if !found {
		t.Fatal("no unresolved read")
	}
}

func TestFindingDispositions1556WriterFailureDoesNotRetry(t *testing.T) {
	for _, jsonMode := range []bool{true, false} {
		writeTeamBinding(t)
		r := taskResponses()
		r["AgentTaskDisposeFinding"] = dispositionResponse1556("fixed", nil)
		srv, calls := attnServer(t, r)
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		f.IOStreams.Out = failingWriter{}
		root := NewRootCmd(f)
		args := []string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--server", srv.URL}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err == nil || len(*calls) != 1 {
			t.Fatalf("err=%v calls=%v", err, *calls)
		}
	}
}

func TestFindingDispositions1556OperationDocuments(t *testing.T) {
	for _, doc := range []string{gen.AgentTaskList_Operation, gen.AgentTaskListFiltered_Operation} {
		if strings.Contains(doc, "unresolvedFindings") {
			t.Fatal("legacy document gained new argument")
		}
	}
	if !strings.Contains(gen.AgentTaskListUnresolved_Operation, "unresolvedFindings: $unresolvedFindings") {
		t.Fatal("filter variable is not used in document")
	}
	if strings.Contains(gen.AgentTaskDisposeFinding_Operation, "result {") || strings.Contains(gen.AgentTaskFindingDispositions_Operation, "result {") {
		t.Fatal("must not fetch immutable result text")
	}
	if !strings.Contains(gen.AgentTaskDisposeFinding_Operation, "findingDispositions(limit: 200, offset: 0)") {
		t.Fatal("mutation readback must cover the capped finding set")
	}
}
