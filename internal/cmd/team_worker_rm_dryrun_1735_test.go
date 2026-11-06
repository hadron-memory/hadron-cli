package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1735 (provider hadron-server#3605 at 8bbf82dd, CHECKED Option A):
// `team worker rm --dry-run` is ONE deleteWorkerPreview Query after the worker
// lookup. It never sends the attestation, never calls deleteWorker, never
// prompts, never retries and never falls back; it is advisory, not a
// reservation.

const previewOK1735 = `{"data":{"deleteWorkerPreview":{"workerId":"wkr1","attestationRequired":true}}}`

type rmRun1735 struct {
	out  string
	err  error
	ops  []string
	vars map[string]json.RawMessage
}

func (r rmRun1735) count(op string) int {
	n := 0
	for _, o := range r.ops {
		if o == op {
			n++
		}
	}
	return n
}

func (r rmRun1735) deletes() int {
	return r.count("DeleteWorker") + r.count("DeleteWorkerInstructed")
}

// runRm1735 runs `team worker rm Iris <args>` against a fake server whose
// preview answers with the given body.
func runRm1735(t *testing.T, preview string, args ...string) rmRun1735 {
	t.Helper()
	var mu sync.Mutex
	var run rmRun1735
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		run.ops = append(run.ops, op)
		mu.Unlock()
		switch op {
		case "Workers", "WorkersRoster":
			return staffJSON
		case "GetWorkerInApp":
			return `{"data":{"worker":` + irisWorkerJSON + `}}`
		case "DeleteWorkerPreview", "DeleteWorkerPreviewInstructed":
			return preview
		case "DeleteWorker", "DeleteWorkerInstructed":
			return `{"data":{"deleteWorker":true}}`
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "rm", "Iris", "--app", "acme.com:eng-team", "--server", gql.URL}, args...))
	run.err = root.Execute()
	run.out = out.String()
	run.vars = captured
	return run
}

// Acceptance 1: exactly one preview request, no delete, no attestation or
// confirmation argument in it.
func TestWorkerRmDryRunSendsOneQueryAndNoDelete1735(t *testing.T) {
	for _, extra := range [][]string{{"--dry-run"}, {"--dry-run", "--yes"}} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			r := runRm1735(t, previewOK1735, extra...)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if r.count("DeleteWorkerPreview") != 1 || r.deletes() != 0 || r.count("DeleteWorkerPreviewInstructed") != 0 {
				t.Fatalf("ops = %v", r.ops)
			}
			var vars map[string]any
			if err := json.Unmarshal(r.vars["DeleteWorkerPreview"], &vars); err != nil {
				t.Fatal(err)
			}
			if len(vars) != 1 || vars["workerRef"] != "wkr1" {
				t.Fatalf("preview variables = %v, want only workerRef wkr1", vars)
			}
		})
	}
}

// The preview is `status: preview`, never the delete's `deleted`, with the
// stable four keys.
func TestWorkerRmDryRunJSONShape1735(t *testing.T) {
	r := runRm1735(t, previewOK1735, "--dry-run", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("%v: %s", err, r.out)
	}
	if len(got) != 4 || got["id"] != "wkr1" || got["name"] != "Iris" || got["attestationRequired"] != true || got["status"] != "preview" {
		t.Fatalf("preview JSON = %s", r.out)
	}
	if strings.Contains(r.out, "deleted") {
		t.Fatalf("a preview must never say deleted: %s", r.out)
	}
}

// Acceptance 3: the text says nothing was deleted or reserved, the
// attestation is still required, and the real rm re-checks and may refuse;
// it never promises the delete will pass.
func TestWorkerRmDryRunTextIsAdvisory1735(t *testing.T) {
	r := runRm1735(t, previewOK1735, "--dry-run")
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, want := range []string{"nothing was deleted", "not reserved", "--confirm-no-external-attribution", "still required", "may still refuse"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("preview text lacks %q:\n%s", want, r.out)
		}
	}
	for _, bad := range []string{"✓ deleted", "its name is free", "will succeed", "can be deleted", "safe to delete", "nothing was written"} {
		if strings.Contains(r.out, bad) {
			t.Errorf("preview text must not say %q:\n%s", bad, r.out)
		}
	}
}

// Acceptance 2: each refusal keeps the existing exit code and the server's
// own message, after exactly one preview request and no delete.
func TestWorkerRmDryRunRefusals1735(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"WORKER_NOT_FOUND", exitcode.NotFound},
		{"WORKER_REFERENCE_NOT_FOUND", exitcode.NotFound},
		{"WORKER_IN_USE", exitcode.Conflict},
		{"FORBIDDEN", exitcode.Forbidden},
	} {
		t.Run(tc.code, func(t *testing.T) {
			body := fmt.Sprintf(`{"errors":[{"message":"server says %s","extensions":{"code":%q}}]}`, tc.code, tc.code)
			r := runRm1735(t, body, "--dry-run")
			if got := exitCodeFor(r.err); got != tc.want {
				t.Fatalf("exit %d want %d: %v", got, tc.want, r.err)
			}
			if r.err == nil || !strings.Contains(r.err.Error(), "server says "+tc.code) {
				t.Fatalf("server message lost: %v", r.err)
			}
			if r.count("DeleteWorkerPreview") != 1 || r.deletes() != 0 {
				t.Fatalf("ops = %v", r.ops)
			}
		})
	}
}

// Acceptance 5: an older server (schema refusal) and a transport failure each
// end after ONE preview request: no resend, no fallback to any other
// operation.
func TestWorkerRmDryRunNeverRetriesOrFallsBack1735(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"older-server schema refusal", `{"errors":[{"message":"Cannot query field \"deleteWorkerPreview\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`},
		{"server error", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`},
		{"no data", `{"data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runRm1735(t, tc.body, "--dry-run")
			if r.err == nil {
				t.Fatal("expected a failure")
			}
			if r.count("DeleteWorkerPreview") != 1 || len(r.ops) != r.count("DeleteWorkerPreview")+countLookups1735(r.ops) {
				t.Fatalf("ops = %v", r.ops)
			}
			if strings.Contains(r.out, "preview") {
				t.Fatalf("a failed preview printed a success: %s", r.out)
			}
		})
	}
}

func countLookups1735(ops []string) int {
	n := 0
	for _, o := range ops {
		if o == "Workers" || o == "WorkersRoster" || o == "GetWorkerInApp" {
			n++
		}
	}
	return n
}

// A reply that is not the previewed worker is a failure, not a success:
// null result, wrong id, empty id.
func TestWorkerRmDryRunValidatesTheReply1735(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"null result", `{"data":{"deleteWorkerPreview":null}}`},
		{"wrong worker id", `{"data":{"deleteWorkerPreview":{"workerId":"wkr-other","attestationRequired":true}}}`},
		{"empty worker id", `{"data":{"deleteWorkerPreview":{"workerId":"","attestationRequired":true}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runRm1735(t, tc.body, "--dry-run", "--json")
			if r.err == nil || r.out != "" {
				t.Fatalf("want a failure with no stdout, got err=%v out=%q", r.err, r.out)
			}
			if r.count("DeleteWorkerPreview") != 1 || r.deletes() != 0 {
				t.Fatalf("ops = %v", r.ops)
			}
		})
	}
}

// The preview never takes the attestation: combining the flags is exit 2
// before ANY request (not even the worker lookup).
func TestWorkerRmDryRunRefusesTheAttestationFlag1735(t *testing.T) {
	r := runRm1735(t, previewOK1735, "--dry-run", "--confirm-no-external-attribution")
	if exitCodeFor(r.err) != exitcode.Usage || r.err == nil || !strings.Contains(r.err.Error(), "--confirm-no-external-attribution") {
		t.Fatalf("want a Usage refusal naming the flag, got %v", r.err)
	}
	if len(r.ops) != 0 {
		t.Fatalf("requests were sent: %v", r.ops)
	}
}

// A malformed --instruction is still exit 2 with nothing sent, dry run or not.
func TestWorkerRmDryRunBadInstructionSendsNothing1735(t *testing.T) {
	r := runRm1735(t, previewOK1735, "--dry-run", "--instruction", "nonsense")
	if exitCodeFor(r.err) != exitcode.Usage || len(r.ops) != 0 {
		t.Fatalf("exit %d ops %v: %v", exitCodeFor(r.err), r.ops, r.err)
	}
}

// Option A: a dry run with --instruction goes out as the instructed preview
// document and its text says a claim may be recorded; it never claims the
// call was write-free. Without a session the claim is not sent and no claim
// is described.
func TestWorkerRmDryRunInstructedDiscloseTheClaim1735(t *testing.T) {
	r := runRm1735(t, previewOK1735, "--dry-run", "--instruction", instrTask1592)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.count("DeleteWorkerPreviewInstructed") != 1 || r.count("DeleteWorkerPreview") != 0 || r.deletes() != 0 {
		t.Fatalf("ops = %v", r.ops)
	}
	if strings.Contains(r.out, "nothing was written") {
		t.Fatalf("an instructed preview is not write-free: %s", r.out)
	}
}

// Acceptance 6 (help): the flag, its advisory nature and the minimum server.
func TestWorkerRmDryRunHelp1735(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"--dry-run", "8bbf82dd", "deleteWorkerPreview", "not a reservation", "never replaces"} {
		if !strings.Contains(out.String(), phrase) {
			t.Errorf("help lacks %q:\n%s", phrase, out)
		}
	}
}

// Option A: the claim note appears exactly when the instruction went out
// through the worker session, and says the preview is not write-free; the
// person's headerless fallback describes no claim.
func TestWorkerRmDryRunClaimNoteOnlyThroughTheSession1735(t *testing.T) {
	var args []string
	for _, c := range cmds1594 {
		if c.name == "rm --dry-run" {
			args = c.args(app1594, "--instruction", instrTask1592)
		}
	}
	if args == nil {
		t.Fatal("rm --dry-run is not in the #1594 table")
	}
	_, r := run1594(run1592{bound: true}).exec(t, args...)
	if r.err != nil || !strings.Contains(r.out, "went out through the worker session") || !strings.Contains(r.out, "not write-free") {
		t.Fatalf("session preview: err %v out %q", r.err, r.out)
	}
	_, r = run1594(run1592{}).exec(t, args...)
	if r.err != nil || strings.Contains(r.out, "worker session") || strings.Contains(r.out, "not write-free") {
		t.Fatalf("person preview describes a claim that was not sent: err %v out %q", r.err, r.out)
	}
}
