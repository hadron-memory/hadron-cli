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
	for _, mode := range []string{"single", "multiple", "choice", "wrong-choice", "discovery-denied", "replay", "conflict"} {
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
					fmt.Fprint(w, `{"data":{"node":{"id":"n1","memoryId":"m1","revision":7,"role":"spec.rule","isRunnable":true}}}`)
				case "ContentValidationRules":
					if mode == "discovery-denied" {
						fmt.Fprint(w, `{"errors":[{"message":"Memory not found","extensions":{"code":"MEMORY_NOT_FOUND"}}]}`)
						return
					}
					rules := `{"role":"spec","enabled":true,"strictSubRoles":false,"validationTaskId":"t1","validationTask":"hrn:node:example.com:kb:validate","validationTaskState":"OK"}`
					if mode == "multiple" || mode == "choice" || mode == "wrong-choice" {
						rules += `,{"role":"task","enabled":true,"strictSubRoles":false,"validationTaskId":"t2","validationTaskState":"OK"}`
					}
					fmt.Fprintf(w, `{"data":{"memoryConfig":{"rules":[%s]}}}`, rules)
				case "ContentValidationTask":
					rev := 3
					id := "t1"
					if q.Variables["ref"] == "t2" {
						rev = 4
						id = "t2"
					}
					fmt.Fprintf(w, `{"data":{"node":{"id":%q,"revision":%d,"isRunnable":true}}}`, id, rev)
				case "RecordContentValidation":
					sent = q.Variables
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
			args := []string{"node", "validation", "record", "00000000-0000-0000-0000-000000000001", "--outcome", "PASS", "--run-id", "run-1706", "--server", srv.URL, "--json"}
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
			if mode == "conflict" {
				want = 5
			}
			if exitCodeFor(err) != want {
				t.Fatalf("exit %d want%d: %v, ops%v", exitCodeFor(err), want, err, ops)
			}
			if want != 0 && mode != "conflict" {
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
			if want == 0 && !strings.Contains(out.String(), `"authorCheck": "AUTHOR_UNKNOWN"`) {
				t.Fatal(out.String())
			}
		})
	}
}
