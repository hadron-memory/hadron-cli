package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
					fmt.Fprint(w, `{"data":{"node":{"id":"n1","revision":7,"validationTasks":{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:validate","revision":3}]}}}}`)
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
