package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidationProjection1706(t *testing.T) {
	for _, tc := range []struct {
		name, set, choice string
		exit              int
		writes            int
	}{
		{"singleton", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:validate","revision":3}]}`, "", 0, 1},
		{"multiple", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3},{"broken":false,"taskId":"t2","taskRef":"hrn:node:example.com:kb:two","revision":4}]}`, "", 2, 0},
		{"choice", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3},{"broken":false,"taskId":"t2","taskRef":"hrn:node:example.com:kb:two","revision":4}]}`, "t2", 0, 1},
		{"wrong-choice", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "other", 2, 0},
		{"none", `{"state":"NOT_CONFIGURED","tasks":[]}`, "", 5, 0},
		{"broken-sibling", `{"state":"BROKEN","tasks":[{"broken":true,"taskId":null,"taskRef":null,"revision":null},{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 5, 0},
		{"null", `null`, "", 8, 0},
		{"nonwriter", `{"state":"UNAVAILABLE","tasks":[]}`, "", 8, 0},
		{"hidden-task-writer", `{"state":"UNAVAILABLE","tasks":[]}`, "", 8, 0},
		{"null-tasks", `{"state":"READY","tasks":null}`, "", 1, 0},
		{"empty-ready", `{"state":"READY","tasks":[]}`, "", 1, 0},
		{"nil-entry", `{"state":"READY","tasks":[null]}`, "", 1, 0},
		{"bad-revision", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":0}]}`, "", 1, 0},
		{"missing-ref", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","revision":3}]}`, "", 1, 0},
		{"contradictory-unavailable", `{"state":"UNAVAILABLE","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 1, 0},
		{"contradictory-none", `{"state":"NOT_CONFIGURED","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 1, 0},
		{"broken-identity", `{"state":"BROKEN","tasks":[{"broken":true,"taskId":"secret"}]}`, "", 1, 0},
		{"duplicate", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3},{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 0, 1},
		{"missing-broken", `{"state":"READY","tasks":[{"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 1, 0},
		{"old-field", `null`, "", 2, 0},
		{"bad-receipt", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 7, 1},
		{"node-null", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 4, 0},
		{"node-bad-revision", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 1, 0},
		{"author-refusal", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 8, 1},
		{"conflict", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "", 5, 1},
		{"urn-choice", `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`, "hrn:node:example.com:kb:one", 0, 1},
		{"unknown", `{"state":"FUTURE","tasks":[]}`, "", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string
					Query         string
					Variables     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "ContentValidationNode":
					reads++
					if tc.name == "node-null" {
						fmt.Fprint(w, `{"data":{"node":null}}`)
						return
					}
					if tc.name == "node-bad-revision" {
						fmt.Fprint(w, `{"data":{"node":{"id":"n1","revision":0}}}`)
						return
					}
					if tc.name == "old-field" {
						fmt.Fprint(w, `{"errors":[{"message":"Cannot query field \"validationTasks\" on type \"Node\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`)
						return
					}
					if !strings.Contains(q.Query, "validationTasks") {
						t.Error("discovery query omits server projection")
					}
					fmt.Fprintf(w, `{"data":{"node":{"id":"n1","revision":7,"memoryId":"m1","validationTasks":%s}}}`, tc.set)
				case "RecordContentValidation":
					writes++
					if _, ok := q.Variables["reporter"]; ok || strings.Contains(q.Query, "reporter") {
						t.Error("reporter remains server-derived")
					}
					if tc.name == "bad-receipt" {
						fmt.Fprint(w, `{"data":{}}`)
						return
					}
					if tc.name == "author-refusal" {
						fmt.Fprint(w, `{"errors":[{"message":"author cannot report","extensions":{"code":"CONTENT_VALIDATION_REPORTER_IS_AUTHOR"}}]}`)
						return
					}
					if tc.name == "conflict" {
						fmt.Fprint(w, `{"errors":[{"message":"node changed","extensions":{"code":"CONFLICT"}}]}`)
						return
					}
					want := "t1"
					rev := float64(3)
					if tc.choice == "t2" {
						want = "t2"
						rev = 4
					}
					if q.Variables["validationTaskRef"] != want || q.Variables["validationTaskRevision"] != rev || q.Variables["runId"] != "projection-run" {
						t.Errorf("wrong chosen revision/run: %v", q.Variables)
					}
					fmt.Fprint(w, `{"data":{"recordNodeContentValidationReport":{"nodeId":"n1","replayed":true,"state":"PASS","report":{"nodeRevision":7,"validationTaskRef":"t1","validationTaskRevision":3,"outcome":"PASS","reportedAt":"2026-11-07T06:00:00Z","authorCheck":"AUTHOR_UNKNOWN"}}}}`)
				default:
					t.Errorf("forbidden legacy discovery operation %s", q.OperationName)
					fmt.Fprint(w, `{"errors":[{"message":"legacy discovery forbidden"}]}`)
				}
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "validation", "record", "01a1b494d4b471ac80e3b1f63080b211", "--outcome", "PASS", "--run-id", "projection-run", "--json", "--server", srv.URL}
			if tc.choice != "" {
				args = append(args, "--task", tc.choice)
			}
			root.SetArgs(args)
			err := root.Execute()
			if exitCodeFor(err) != tc.exit || writes != tc.writes || reads != 1 {
				t.Fatalf("exit=%d want=%d writes=%d want=%d reads=%d err=%v", exitCodeFor(err), tc.exit, writes, tc.writes, reads, err)
			}
			if tc.exit == 8 && tc.writes == 0 && fmt.Sprint(err) != "validation task set is unavailable for this caller; no report sent" {
				t.Fatalf("unavailable/null must be identical and cause-free: %v", err)
			}
			if tc.writes == 0 && out.Len() != 0 {
				t.Fatal("partial output on refusal")
			}
			if tc.exit == 0 && tc.writes == 1 && !strings.Contains(out.String(), `"replayed": true`) && !strings.Contains(out.String(), `"replayed":true`) {
				t.Fatal("replay receipt missing")
			}
		})
	}
}
