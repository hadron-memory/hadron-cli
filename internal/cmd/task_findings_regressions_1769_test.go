package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// These optional pins extend1550 without changing its provider contract.
func TestTaskFindingsLostFinish1769(t *testing.T) {
	for _, mode := range []string{"gateway502", "lost transport"} {
		t.Run(mode, func(t *testing.T) {
			writeTeamBinding(t)
			var finishes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string `json:"operationName"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				if q.OperationName == "GetBoundSessionEnd" {
					body, _ := unstubbedDefault(q.OperationName)
					_, _ = fmt.Fprint(w, body)
					return
				}
				if q.OperationName != "AgentTaskFinishWithFindings" {
					t.Errorf("unexpected operation %s", q.OperationName)
					http.Error(w, "unexpected", http.StatusInternalServerError)
					return
				}
				finishes.Add(1)
				if mode == "gateway502" {
					http.Error(w, "bad gateway", http.StatusBadGateway)
					return
				}
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Error("HTTP hijacking unavailable")
					return
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}))
			t.Cleanup(srv.Close)
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`)})
			err := root.Execute()
			if exitCodeFor(err) != 7 || finishes.Load() != 1 || out.Len() != 0 {
				t.Fatalf("exit=%d mutation count=%d out=%q err=%v", exitCodeFor(err), finishes.Load(), out.String(), err)
			}
			for _, want := range []string{"state may have changed", "hadron team task findings", "do not resend automatically"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("lost-write advice lacks %q: %v", want, err)
				}
			}
		})
	}
}

func findingPage1769(t *testing.T, n int, id string) string {
	t.Helper()
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": fmt.Sprintf("%s%d", id, i), "severity": "note", "text": "finding text"}
	}
	return string(mustJSON1550(t, map[string]any{"items": rows, "total": n, "hasMore": false}))
}

func TestTaskFindingsOversizedPages1769(t *testing.T) {
	t.Run("read over requested limit", func(t *testing.T) {
		for _, n := range []int{1, 2} {
			reply := `{"data":{"agentTask":{"id":"task1","result":{"findings":` + findingPage1769(t, n, "F") + `}}}}`
			calls, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": reply}, "team", "task", "findings", "AT0.7", "--limit", "1")
			if len(*calls) != 1 {
				t.Fatalf("unexpected request count %d", len(*calls))
			}
			if n == 1 {
				if err != nil || !strings.Contains(out, "shown 1 / total 1") {
					t.Fatalf("valid page: %v %q", err, out)
				}
				continue
			}
			if exitCodeFor(err) != 1 || out != "" || !strings.Contains(err.Error(), "exceeds requested limit") {
				t.Fatalf("oversized read: %v %q", err, out)
			}
		}
	})
	t.Run("finish over receipt limit", func(t *testing.T) {
		for _, n := range []int{20, 21} {
			input := make([]map[string]any, n)
			for i := range input {
				input[i] = map[string]any{"severity": "note", "text": "finding text"}
			}
			calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, findingPage1769(t, n, "F"))}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, string(mustJSON1550(t, input))))
			if len(*calls) != 1 {
				t.Fatalf("unexpected mutation count %d", len(*calls))
			}
			if n == 20 {
				if err != nil || !strings.Contains(out, "shown 20 / total 20") {
					t.Fatalf("valid receipt: %v %q", err, out)
				}
				continue
			}
			if exitCodeFor(err) != 1 || out != "" || !strings.Contains(err.Error(), "invalid finish findings response") || !strings.Contains(err.Error(), "do not resend automatically") {
				t.Fatalf("oversized receipt: %v %q", err, out)
			}
		}
	})
}

func TestTaskFindingHostileID1769(t *testing.T) {
	id := "F-\x1b[31m\u202e-hostile"
	page := string(mustJSON1550(t, map[string]any{"items": []any{map[string]any{"id": id, "severity": "note", "text": "finding text"}}, "total": 1, "hasMore": false}))
	reply := `{"data":{"agentTask":{"id":"task1","result":{"findings":` + page + `}}}}`
	for _, jsonMode := range []bool{false, true} {
		args := []string{"team", "task", "findings", "AT0.7"}
		if jsonMode {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, map[string]string{"AgentTaskResultFindings": reply}, args...)
		if err != nil || len(*calls) != 1 {
			t.Fatalf("err=%v calls=%v", err, *calls)
		}
		if !jsonMode {
			if strings.ContainsAny(out, "\x1b\u202e") || !strings.Contains(out, "F-") || !strings.Contains(out, "finding text") {
				t.Fatalf("unsafe/missing row: %q", out)
			}
			continue
		}
		var dto struct {
			Findings struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			} `json:"findings"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil || len(dto.Findings.Items) != 1 || dto.Findings.Items[0].ID != id {
			t.Fatalf("JSON id changed: %s err=%v", out, err)
		}
	}
}

func TestTaskFinishEmptyWarnings1769(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, `[]`), "--json")
	if err != nil || len(*calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
	var dto map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	value, ok := dto["warnings"]
	if !ok || strings.TrimSpace(string(value)) != "[]" {
		t.Fatalf("warnings must be present []: %s", out)
	}
}

func TestTaskFindingQuoteSurrogate1769(t *testing.T) {
	good := `[{"severity":"note","text":"SENTINEL slash \\ then quote \" then tail"}]`
	bad := strings.Replace(good, "then tail", `then \ud800`, 1)
	for _, body := range []string{good, bad} {
		if !json.Valid([]byte(body)) {
			t.Fatalf("fixture is not syntactically valid JSON: %q", body)
		}
		page := `{"items":[{"id":"F1","severity":"note","text":"SENTINEL slash \\ then quote \" then tail"}],"total":1,"hasMore":false}`
		calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, page)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, body), "--json")
		if body == bad {
			if exitCodeFor(err) != 2 || len(*calls) != 0 || out != "" || !strings.Contains(err.Error(), "surrogate") || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("surrogate refusal: %v calls=%v out=%q", err, *calls, out)
			}
			continue
		}
		if err != nil || len(*calls) != 1 {
			t.Fatalf("valid escapes: %v calls=%v", err, *calls)
		}
		var input []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(good), &input); err != nil {
			t.Fatal(err)
		}
		rows, ok := (*calls)[0].Vars["findings"].([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("wire findings: %#v", (*calls)[0].Vars)
		}
		row, ok := rows[0].(map[string]any)
		if !ok || row["text"] != input[0].Text {
			t.Fatalf("wire text changed: %#v", rows)
		}
		var dto struct {
			Result struct {
				Findings struct {
					Items []struct {
						Text string `json:"text"`
					} `json:"items"`
				} `json:"findings"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil || len(dto.Result.Findings.Items) != 1 || dto.Result.Findings.Items[0].Text != input[0].Text {
			t.Fatalf("JSON text changed: %s err=%v", out, err)
		}
	}
}
