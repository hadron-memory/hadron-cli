package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeRecordValidation1706(t *testing.T) {
	for _, mode := range []string{"single", "multiple", "choice", "wrong-choice", "discovery-denied", "replay", "conflict", "zero", "broken", "unreadable", "null-rules", "task-null", "bad-receipt", "node-null", "bad-revision"} {
		t.Run(mode, func(t *testing.T) {
			var ops []string
			var sent map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string         `json:"operationName"`
					Variables     map[string]any `json:"variables"`
					Query         string         `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				ops = append(ops, q.OperationName)
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "ContentValidationNode":
					if mode == "node-null" {
						fmt.Fprint(w, `{"data":{"node":null}}`)
						return
					}
					if mode == "bad-revision" {
						fmt.Fprint(w, `{"data":{"node":{"id":"n1","memoryId":"m1","revision":0}}}`)
						return
					}
					fmt.Fprint(w, `{"data":{"node":{"id":"n1","memoryId":"m1","revision":7,"role":"spec.rule","isRunnable":true}}}`)
				case "ContentValidationRules":
					if mode == "discovery-denied" {
						fmt.Fprint(w, `{"errors":[{"message":"Memory not found","extensions":{"code":"MEMORY_NOT_FOUND"}}]}`)
						return
					}
					if mode == "zero" {
						fmt.Fprint(w, `{"data":{"memoryConfig":{"rules":[]}}}`)
						return
					}
					if mode == "null-rules" {
						fmt.Fprint(w, `{"data":{"memoryConfig":{"rules":[null]}}}`)
						return
					}
					rules := `{"role":"spec","enabled":true,"strictSubRoles":false,"validationTaskId":"t1","validationTask":"hrn:node:example.com:kb:validate","validationTaskState":"OK"}`
					if mode == "multiple" || mode == "choice" || mode == "wrong-choice" {
						rules += `,{"role":"task","enabled":true,"strictSubRoles":false,"validationTaskId":"t2","validationTaskState":"OK"}`
					}
					if mode == "broken" {
						rules = strings.ReplaceAll(rules, `"OK"`, `"BROKEN"`)
					}
					if mode == "unreadable" {
						rules = strings.ReplaceAll(rules, `"OK"`, `"UNREADABLE"`)
					}
					fmt.Fprintf(w, `{"data":{"memoryConfig":{"rules":[%s]}}}`, rules)
				case "ContentValidationTask":
					if mode == "task-null" {
						fmt.Fprint(w, `{"data":{"node":null}}`)
						return
					}
					rev := 3
					id := "t1"
					if q.Variables["ref"] == "t2" {
						rev = 4
						id = "t2"
					}
					fmt.Fprintf(w, `{"data":{"node":{"id":%q,"revision":%d,"isRunnable":true}}}`, id, rev)
				case "RecordContentValidation":
					sent = q.Variables
					if mode == "bad-receipt" {
						fmt.Fprint(w, `{"data":{}}`)
						return
					}
					if strings.Contains(q.Query, "reporter") {
						t.Error("client must not send/select reporter")
					}
					if mode == "conflict" {
						fmt.Fprint(w, `{"errors":[{"message":"node changed","extensions":{"code":"CONFLICT"}}]}`)
						return
					}
					fmt.Fprintf(w, `{"data":{"recordNodeContentValidationReport":{"nodeId":"n1","replayed":%t,"state":"PASS","report":{"nodeRevision":7,"validationTaskRef":"t1","validationTaskRevision":3,"outcome":"PASS","reportedAt":"2026-11-07T06:00:00Z","authorCheck":"AUTHOR_UNKNOWN"}}}}`, mode == "replay")
				default:
					t.Errorf("unexpected op %s", q.OperationName)
					fmt.Fprint(w, `{}`)
				}
			}))
			defer srv.Close()
			f, out := testFactory(t)
			args := []string{"node", "validation", "record", "01a1b494d4b471ac80e3b1f63080b211", "--outcome", "PASS", "--run-id", "run-1706", "--server", srv.URL, "--json"}
			if mode == "choice" {
				args = append(args, "--task", "t2")
			}
			if mode == "wrong-choice" {
				args = append(args, "--task", "other")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			err := root.Execute()
			want := 0
			if mode == "multiple" || mode == "wrong-choice" {
				want = 2
			}
			if mode == "discovery-denied" {
				want = 8
			}
			if mode == "conflict" || mode == "zero" || mode == "broken" || mode == "task-null" {
				want = 5
			}
			if mode == "unreadable" {
				want = 2
			}
			if mode == "node-null" {
				want = 4
			}
			if mode == "null-rules" || mode == "bad-revision" {
				want = 1
			}
			if mode == "bad-receipt" {
				want = 7
			}
			if exitCodeFor(err) != want {
				t.Fatalf("exit %d want%d: %v, ops%v", exitCodeFor(err), want, err, ops)
			}
			if mode == "multiple" {
				for _, part := range []string{"t1", "t2", "revision 3", "revision 4", "--task"} {
					if !strings.Contains(fmt.Sprint(err), part) {
						t.Fatalf("missing choice detail %s: %v", part, err)
					}
				}
			}
			if want != 0 && mode != "conflict" && mode != "bad-receipt" {
				if sent != nil {
					t.Fatal("mutation on discovery/choice refusal")
				}
				if out.Len() != 0 {
					t.Fatal("partial stdout")
				}
				return
			}
			if sent == nil {
				t.Fatal("no report sent")
			}
			task := "t1"
			rev := float64(3)
			if mode == "choice" {
				task = "t2"
				rev = 4
			}
			if sent["nodeRevision"] != float64(7) || sent["validationTaskRevision"] != rev || sent["validationTaskRef"] != task || sent["runId"] != "run-1706" || sent["outcome"] != "PASS" || len(sent) != 6 {
				t.Fatalf("derived wire %v", sent)
			}
			count := 0
			for _, op := range ops {
				if op == "RecordContentValidation" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("retry count%d", count)
			}
			if mode == "replay" && !strings.Contains(out.String(), `"replayed": true`) {
				t.Fatal(out.String())
			}
			if want == 0 && !strings.Contains(out.String(), `"authorCheck": "AUTHOR_UNKNOWN"`) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestNodeRecordValidationRefusals1706(t *testing.T) {
	codes := map[string]int{"NODE_NOT_FOUND": 4, "FORBIDDEN": 8, "CONTENT_VALIDATION_REPORTER_UNKNOWN": 8, "CONTENT_VALIDATION_NODE_REVISION_INVALID": 2, "CONTENT_VALIDATION_NOT_CONFIGURED": 5, "CONTENT_VALIDATION_TASK_BROKEN": 5, "CONTENT_VALIDATION_WRONG_TASK": 2, "CONTENT_VALIDATION_TASK_REVISION_INVALID": 2, "CONTENT_VALIDATION_REPORTER_IS_AUTHOR": 8, "CONTENT_VALIDATION_RUN_ID_CONFLICT": 5, "BAD_USER_INPUT": 2, "CONFLICT": 5}
	for code, want := range codes {
		t.Run(code, func(t *testing.T) {
			mutations := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string         `json:"operationName"`
					Variables     map[string]any `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				switch q.OperationName {
				case "ContentValidationNode":
					fmt.Fprint(w, `{"data":{"node":{"id":"n1","memoryId":"m1","revision":7,"role":"spec","isRunnable":false}}}`)
				case "ContentValidationRules":
					fmt.Fprint(w, `{"data":{"memoryConfig":{"rules":[{"role":"spec","enabled":true,"strictSubRoles":false,"validationTaskId":"t1","validationTaskState":"OK"}]}}}`)
				case "ContentValidationTask":
					fmt.Fprint(w, `{"data":{"node":{"id":"t1","revision":3,"isRunnable":true}}}`)
				case "RecordContentValidation":
					mutations++
					if _, ok := q.Variables["runId"]; ok {
						t.Error("unset runId must be omitted")
					}
					fmt.Fprintf(w, `{"errors":[{"message":"typed refusal","extensions":{"code":%q}}]}`, code)
				default:
					t.Errorf("unexpected op%s", q.OperationName)
				}
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "validation", "record", "01a1b494d4b471ac80e3b1f63080b211", "--outcome", "ERROR", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != want || mutations != 1 || out.Len() != 0 || !strings.Contains(fmt.Sprint(err), "typed refusal") {
				t.Fatalf("code%d want%d mutations%d out%s err%v", exitCodeFor(err), want, mutations, out, err)
			}
		})
	}
}

func TestNodeRecordValidationInput1706(t *testing.T) {
	for _, flags := range [][]string{{}, {"--outcome", "pass"}, {"--outcome", "PASS", "--task="}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("input refusal made request") }))
			defer srv.Close()
			f, out := testFactory(t)
			args := []string{"node", "validation", "record", "01a1b494d4b471ac80e3b1f63080b211", "--server", srv.URL}
			args = append(args, flags...)
			root := NewRootCmd(f)
			root.SetArgs(args)
			err := root.Execute()
			if exitCodeFor(err) != 2 || calls != 0 || out.Len() != 0 {
				t.Fatalf("exit%d calls%d out%s err%v", exitCodeFor(err), calls, out, err)
			}
		})
	}
}
