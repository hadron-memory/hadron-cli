package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1795 item 1 (option A, Wolfgang's ruling): a 500/502/504 JSON envelope on the
// content-validation RECORD mutation is an unknown outcome (exit 7) with
// uncertain-write guidance. Recorder-only: api.MapError and the read are
// untouched. The guidance shows only INTERNAL_SERVER_ERROR and the request id
// (cor:api:260:01), never server detail, and the CLI never retries.

const (
	secretDetail1795 = "database exploded at shard-7"
	ready5xx1795     = `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`
)

// server5xx1795 answers the discovery read normally and the record mutation
// with status and body; it captures every record request's variables.
func server5xx1795(t *testing.T, status int, body string) (*httptest.Server, *[]map[string]any, *int) {
	t.Helper()
	var sent []map[string]any
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string
			Variables     map[string]any
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "ContentValidationNode":
			reads++
			if status == -1 { // a 5xx on the READ, to prove the read is untouched
				w.WriteHeader(500)
				fmt.Fprintf(w, `{"errors":[{"message":%q,"extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req-read-1"}}]}`, secretDetail1795)
				return
			}
			fmt.Fprintf(w, `{"data":{"node":{"id":"n1","revision":7,"memoryId":"m1","validationTasks":%s}}}`, ready5xx1795)
		case "RecordContentValidation":
			sent = append(sent, q.Variables)
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		default:
			t.Errorf("unexpected operation %s", q.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &sent, &reads
}

func TestValidationRecord5xxEnvelopeIsAnUnknownOutcome1795(t *testing.T) {
	typed := fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req-abc-123"}}]}`, secretDetail1795)
	other := fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":"FORBIDDEN"}}]}`, secretDetail1795)
	codeless := fmt.Sprintf(`{"errors":[{"message":%q}]}`, secretDetail1795)
	for _, status := range []int{500, 502, 504} {
		for name, tc := range map[string]struct {
			body        string
			wantFault   bool // the guidance carries INTERNAL_SERVER_ERROR and the id
			wantRequest string
		}{
			"typed INTERNAL_SERVER_ERROR": {typed, true, "req-abc-123"},
			"typed other code":            {other, false, ""},
			"codeless":                    {codeless, false, ""},
		} {
			t.Run(fmt.Sprintf("%d %s", status, name), func(t *testing.T) {
				srv, sent, _ := server5xx1795(t, status, tc.body)
				err := record1795(t, srv, "--run-id", "run-9")
				if exitCodeFor(err) != exitcode.Unavailable || err == nil {
					t.Fatalf("exit=%d want 7: %v", exitCodeFor(err), err)
				}
				msg := err.Error()
				for _, want := range []string{fmt.Sprintf("HTTP %d", status), "unknown outcome", "may or may not have been recorded", "hadron node get", "same --run-id", "does not retry"} {
					if !strings.Contains(msg, want) {
						t.Errorf("guidance lacks %q: %s", want, msg)
					}
				}
				if strings.Contains(msg, secretDetail1795) || strings.Contains(msg, "FORBIDDEN") || strings.Contains(msg, "shard") {
					t.Errorf("server detail leaked: %s", msg)
				}
				if tc.wantFault != (strings.Contains(msg, "INTERNAL_SERVER_ERROR") && strings.Contains(msg, tc.wantRequest) && tc.wantRequest != "") {
					t.Errorf("INTERNAL_SERVER_ERROR/request id shown=%v want %v: %s", !tc.wantFault, tc.wantFault, msg)
				}
				if !tc.wantFault && strings.Contains(msg, "INTERNAL_SERVER_ERROR") {
					t.Errorf("INTERNAL_SERVER_ERROR claimed without the envelope saying so: %s", msg)
				}
				if len(*sent) != 1 {
					t.Errorf("the mutation was sent %d times; the CLI never retries", len(*sent))
				}
			})
		}
	}
}

// Only the record mutation's 500/502/504 is wrapped: a 503 and a 5xx on the
// READ keep the shared mapping.
func TestValidationRecord5xxWrapperIsBounded1795(t *testing.T) {
	t.Run("read 500 stays the internal-fault exit 1", func(t *testing.T) {
		srv, sent, _ := server5xx1795(t, -1, "")
		err := record1795(t, srv)
		if exitCodeFor(err) != exitcode.Error || len(*sent) != 0 {
			t.Fatalf("exit=%d sent=%d: %v", exitCodeFor(err), len(*sent), err)
		}
	})
	t.Run("a 200 refusal envelope keeps its exit", func(t *testing.T) {
		srv, _, _ := server5xx1795(t, 200, `{"errors":[{"message":"x","extensions":{"code":"CONFLICT"}}]}`)
		if err := record1795(t, srv); exitCodeFor(err) != exitcode.Conflict {
			t.Fatalf("exit=%d: %v", exitCodeFor(err), err)
		}
	})
	t.Run("a 400 refusal keeps its exit", func(t *testing.T) {
		srv, _, _ := server5xx1795(t, 400, `{"errors":[{"message":"x","extensions":{"code":"CONTENT_VALIDATION_WRONG_TASK"}}]}`)
		if err := record1795(t, srv); exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("exit=%d: %v", exitCodeFor(err), err)
		}
	})
}

// What the code does on a repeat record, which the retry advice rests on:
// with --run-id both requests carry the same run id (and the same node and
// task revisions); without it no run id is sent at all.
func TestValidationRecordRepeatSendsTheSameRunID1795(t *testing.T) {
	srv, sent, _ := server5xx1795(t, 502, `{"errors":[{"message":"gateway"}]}`)
	_ = record1795(t, srv, "--run-id", "run-9")
	_ = record1795(t, srv, "--run-id", "run-9")
	if len(*sent) != 2 || (*sent)[0]["runId"] != "run-9" || (*sent)[1]["runId"] != "run-9" ||
		(*sent)[0]["nodeRevision"] != (*sent)[1]["nodeRevision"] || (*sent)[0]["validationTaskRevision"] != (*sent)[1]["validationTaskRevision"] {
		t.Fatalf("a repeat with --run-id must send the identical request: %v", *sent)
	}
	srv2, sent2, _ := server5xx1795(t, 502, `{"errors":[{"message":"gateway"}]}`)
	_ = record1795(t, srv2)
	if _, ok := (*sent2)[0]["runId"]; len(*sent2) != 1 || ok {
		t.Fatalf("without --run-id no idempotency key is sent: %v", *sent2)
	}
}
