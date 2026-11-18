package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
func server5xx1795(t *testing.T, status int, body string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string
			Variables     map[string]any
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "ContentValidationNode":
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
	return srv, &sent
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
				srv, sent := server5xx1795(t, status, tc.body)
				err := record1795(t, srv, "--run-id", "run-9")
				if exitCodeFor(err) != exitcode.Unavailable || err == nil {
					t.Fatalf("exit=%d want 7: %v", exitCodeFor(err), err)
				}
				msg := err.Error()
				for _, want := range []string{fmt.Sprintf("HTTP %d", status), "unknown outcome", "may or may not have been recorded", "hadron node get", "same --run-id", "when the node and task revisions and your identity are unchanged", "refuses it as a conflict", "does not retry"} {
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
		srv, sent := server5xx1795(t, -1, "")
		err := record1795(t, srv)
		if exitCodeFor(err) != exitcode.Error || len(*sent) != 0 {
			t.Fatalf("exit=%d sent=%d: %v", exitCodeFor(err), len(*sent), err)
		}
	})
	t.Run("a 200 refusal envelope keeps its exit", func(t *testing.T) {
		srv, _ := server5xx1795(t, 200, `{"errors":[{"message":"x","extensions":{"code":"CONFLICT"}}]}`)
		if err := record1795(t, srv); exitCodeFor(err) != exitcode.Conflict {
			t.Fatalf("exit=%d: %v", exitCodeFor(err), err)
		}
	})
	t.Run("a 400 refusal keeps its exit", func(t *testing.T) {
		srv, _ := server5xx1795(t, 400, `{"errors":[{"message":"x","extensions":{"code":"CONTENT_VALIDATION_WRONG_TASK"}}]}`)
		if err := record1795(t, srv); exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("exit=%d: %v", exitCodeFor(err), err)
		}
	})
}

// What the code does on a repeat record, which the retry advice rests on:
// with --run-id both requests carry the same run id (and the same node and
// task revisions); without it no run id is sent at all.
func TestValidationRecordRepeatSendsTheSameRunID1795(t *testing.T) {
	srv, sent := server5xx1795(t, 502, `{"errors":[{"message":"gateway"}]}`)
	_ = record1795(t, srv, "--run-id", "run-9")
	_ = record1795(t, srv, "--run-id", "run-9")
	if len(*sent) != 2 || (*sent)[0]["runId"] != "run-9" || (*sent)[1]["runId"] != "run-9" ||
		(*sent)[0]["nodeRevision"] != (*sent)[1]["nodeRevision"] || (*sent)[0]["validationTaskRevision"] != (*sent)[1]["validationTaskRevision"] {
		t.Fatalf("a repeat with --run-id must send the identical request: %v", *sent)
	}
	srv2, sent2 := server5xx1795(t, 502, `{"errors":[{"message":"gateway"}]}`)
	_ = record1795(t, srv2)
	if _, ok := (*sent2)[0]["runId"]; len(*sent2) != 1 || ok {
		t.Fatalf("without --run-id no idempotency key is sent: %v", *sent2)
	}
}

// Without --run-id the advice says a repeat may record a second report; the
// help and the agent guide say a 500/502/504 answer exits 7 even with a JSON body.
func TestValidationRecord5xxAdviceWithoutRunIDAndDocs1795(t *testing.T) {
	srv, _ := server5xx1795(t, 502, `{"errors":[{"message":"gateway"}]}`)
	err := record1795(t, srv)
	if exitCodeFor(err) != exitcode.Unavailable || err == nil || !strings.Contains(err.Error(), "no --run-id") || !strings.Contains(err.Error(), "may record a second report") {
		t.Fatalf("no-run-id advice: %v", err)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "validation", "record", "--help"})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	guide, e := os.ReadFile("agentic/agentic-usage.md")
	if e != nil {
		t.Fatal(e)
	}
	const want = "An HTTP 500, 502 or 504 answer to the record request exits 7 even when its body is a JSON error: the outcome is unknown, so read the node's latest validation report before recording again"
	for name, text := range map[string]string{"help": flat1471(out.String()), "agentic-usage": flat1471(string(guide))} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", name, want)
		}
		if !strings.Contains(text, "revisions and your identity are unchanged") || !strings.Contains(text, "refused as a conflict") {
			t.Errorf("%s lacks the replay condition (same node/task revisions and identity, else a conflict)", name)
		}
	}
}

// The exit-7 error's --json envelope carries the code, the
// message the CLI wrote, and for a typed INTERNAL_SERVER_ERROR the server code
// and request id (cor:api:260:01: a 5xx is that code plus a request id); never
// the server's message or extensions.
func TestValidationRecord5xxJSONEnvelopeLeaksNothing1795(t *testing.T) {
	for name, tc := range map[string]struct {
		body             string
		wantServerCode   bool
		wantRequestIDStr string
	}{
		"typed":    {fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req-abc-123","secret":%q}}]}`, secretDetail1795, secretDetail1795), true, "req-abc-123"},
		"codeless": {fmt.Sprintf(`{"errors":[{"message":%q}]}`, secretDetail1795), false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := server5xx1795(t, 502, tc.body)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "validation", "record", "01a1b494d4b471ac80e3b1f63080b211", "--outcome", "PASS", "--json", "--server", srv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("succeeded")
			}
			if code := renderError(f, err); code != exitcode.Unavailable {
				t.Fatalf("exit %d", code)
			}
			var raw map[string]json.RawMessage
			if e := json.Unmarshal([]byte(out.String()), &raw); e != nil {
				t.Fatalf("%v: %s", e, out.String())
			}
			var failure map[string]any
			if e := json.Unmarshal(raw["error"], &failure); e != nil || len(failure) == 0 {
				t.Fatalf("no error envelope: %v %s", e, out.String())
			}
			if msg, _ := failure["message"].(string); !strings.Contains(msg, "unknown outcome") || failure["code"] != float64(exitcode.Unavailable) {
				t.Fatalf("the CLI-written exit-7 message is missing: %v", failure)
			}
			if _, ok := failure["requestId"]; ok != tc.wantServerCode {
				t.Fatalf("requestId present=%v want %v: %v", ok, tc.wantServerCode, failure)
			}
			if strings.Contains(out.String(), secretDetail1795) || strings.Contains(out.String(), "shard") || failure["extensions"] != nil {
				t.Fatalf("server detail in the envelope: %s", out.String())
			}
			if _, ok := failure["serverCode"]; ok != tc.wantServerCode {
				t.Fatalf("serverCode present=%v want %v: %v", ok, tc.wantServerCode, failure)
			}
			if tc.wantServerCode && (failure["serverCode"] != "INTERNAL_SERVER_ERROR" || failure["requestId"] != tc.wantRequestIDStr) {
				t.Fatalf("fault fields: %v", failure)
			}
		})
	}
}

// Edge cases: a 502 with a body that is not JSON degrades to the plain
// HTTP status; a 503 keeps the shared mapping (deferred to #2430); an empty
// --run-id is not "a run id was passed".
func TestValidationRecord5xxEdges1795(t *testing.T) {
	t.Run("HTML body", func(t *testing.T) {
		srv, sent := server5xx1795(t, 502, `<html><body>Bad Gateway</body></html>`)
		err := record1795(t, srv)
		if exitCodeFor(err) != exitcode.Unavailable || err == nil || !strings.Contains(err.Error(), "HTTP 502") || strings.Contains(err.Error(), "INTERNAL_SERVER_ERROR") || strings.Contains(err.Error(), "Bad Gateway") || len(*sent) != 1 {
			t.Fatalf("html 502: sent=%d %v", len(*sent), err)
		}
	})
	t.Run("503 keeps the shared mapping", func(t *testing.T) {
		srv, _ := server5xx1795(t, 503, `{"errors":[{"message":"unavailable"}]}`)
		err := record1795(t, srv)
		// "unknown outcome" is the marker of the exit-7 guidance text.
		if err == nil || exitCodeFor(err) != exitcode.Error || strings.Contains(err.Error(), "unknown outcome") {
			t.Fatalf("503 must keep the shared mapping (#2430): exit=%d %v", exitCodeFor(err), err)
		}
	})
	t.Run("an empty --run-id is refused locally, nothing sent", func(t *testing.T) {
		srv, sent := server5xx1795(t, 502, `{"errors":[{"message":"gateway"}]}`)
		err := record1795(t, srv, "--run-id", "")
		if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "--run-id must not be empty") || len(*sent) != 0 {
			t.Fatalf("empty run id: sent=%d %v", len(*sent), err)
		}
	})
}
