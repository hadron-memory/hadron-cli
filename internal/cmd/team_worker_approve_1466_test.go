package cmd

// cli#1466: `hadron team worker approve` approves the App's compiled worker
// boot prompts in one run. Approval only (hadron-server#2973 ruling (a)): one
// pinned approveNode per selected worker, the server deciding authority per
// item; tamper classification comes only from the server's verifyNode; one
// serving snapshot per approved worker; a lost response is read once, never
// resent. Pins are numbered as in the issue's acceptance.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type approveWorker struct {
	id, name string
	retired  bool
	pointer  string // compiled node URN; "" = no pointer
	deleted  bool
	revision int
	// state is the compiled node's approvalStatus.state; "null" = no
	// approvalStatus, "absent" = the node is unavailable to the reader.
	state string
	// verify is verifyNode's state: SUPERSEDED, TAMPERED, INTACT, NOT_APPROVED,
	// "error" (a refused read) or "null" (an empty result).
	verify string
	// approve is the approveNode outcome: "ok", "null" (empty success), "502"
	// (lost response) or a server error code.
	approve string
	// after is the status read after a lost response: "landed", "same", "error".
	after string
	// serve is the bootPromptServe read after a successful approve: "same",
	// "older", "refused", "error", "null".
	serve string
}

type approveCall struct {
	Op      string
	Vars    map[string]any
	Session string // X-Hadron-Session as sent (cli#1872)
}

type approveFake struct {
	onOp    func(op string) // runs as each operation arrives (cli#1872)
	mu      sync.Mutex
	calls   []approveCall
	workers []approveWorker
	// mode shapes the PREVIEW's approval-state reads: "" (whole pages),
	// "truncate-half" (the first page returns half its refs and omits the
	// rest), "truncate-empty" (truncated with no node), "fail" (502).
	mode string
}

func (a *approveFake) ops(op string) []approveCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []approveCall
	for _, c := range a.calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func (a *approveFake) byPointer(ref string) *approveWorker {
	for i := range a.workers {
		if a.workers[i].pointer == ref && ref != "" {
			return &a.workers[i]
		}
	}
	return nil
}

func (a *approveFake) byID(id string) *approveWorker {
	for i := range a.workers {
		if a.workers[i].id == id {
			return &a.workers[i]
		}
	}
	return nil
}

func approvalStatusJSON(state string, rev int) string {
	switch state {
	case "null":
		return "null"
	case "APPROVED":
		return fmt.Sprintf(`{"state":"APPROVED","approval":{"revision":%d,"approvedAt":"2026-11-10T00:00:00Z","approvedBy":{"handle":"holger","urn":null},"approvedByInfo":null,"hash":"h"}}`, rev)
	case "SUPERSEDED":
		return fmt.Sprintf(`{"state":"SUPERSEDED","approval":{"revision":%d,"approvedAt":"2026-11-09T00:00:00Z","approvedBy":{"handle":"holger","urn":null},"approvedByInfo":null,"hash":"h"}}`, rev-1)
	default:
		return fmt.Sprintf(`{"state":%q,"approval":null}`, state)
	}
}

func newApproveFake(t *testing.T, workers []approveWorker) (*httptest.Server, *approveFake) {
	t.Helper()
	fake := &approveFake{workers: workers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		op := body.OperationName
		fake.mu.Lock()
		fake.calls = append(fake.calls, approveCall{Op: op, Vars: body.Variables, Session: r.Header.Get("X-Hadron-Session")})
		fake.mu.Unlock()
		if fake.onOp != nil {
			fake.onOp(op)
		}
		w.Header().Set("Content-Type", "application/json")
		switch op {
		case "TeamAppIdentity":
			_, _ = w.Write([]byte(teamAppIdentityJSON))
		case "GetBoundSessionEnd", "ChannelReadState":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "WorkersBootPointers":
			items := []string{}
			for _, wk := range fake.workers {
				if wk.retired {
					continue
				}
				ptr := "null"
				if wk.pointer != "" {
					ptr = fmt.Sprintf("%q", wk.pointer)
				}
				items = append(items, fmt.Sprintf(`{"id":%q,"urn":"hrn:worker:acme.com:eng-team:%s","name":%q,"retiredAt":null,"bootPromptRef":%s,"bootPromptTargetDeleted":%v}`,
					wk.id, strings.ToLower(wk.name), wk.name, ptr, wk.deleted))
			}
			fmt.Fprintf(w, `{"data":{"workers":{"total":%d,"items":[%s]}}}`, len(items), strings.Join(items, ","))
		case "BootPromptApprovalBatch":
			refs, _ := body.Variables["refs"].([]any)
			// The read after a lost response asks for ONE ref, after an approveNode.
			lostRead := len(refs) == 1 && len(fake.ops("ApproveNode")) > 0
			if !lostRead {
				previewReads := len(fake.ops("BootPromptApprovalBatch"))
				switch {
				case fake.mode == "fail":
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte("bad gateway"))
					return
				case fake.mode == "truncate-empty":
					quoted := []string{}
					for _, ref := range refs {
						quoted = append(quoted, fmt.Sprintf("%q", ref))
					}
					fmt.Fprintf(w, `{"data":{"nodeBatch":{"truncated":true,"omitted":[%s],"unavailable":[],"nodes":[]}}}`, strings.Join(quoted, ","))
					return
				case fake.mode == "truncate-half" && previewReads == 1 && len(refs) > 1:
					half := len(refs) / 2
					nodes, omitted := []string{}, []string{}
					for k, ref := range refs {
						if k >= half {
							omitted = append(omitted, fmt.Sprintf("%q", ref))
							continue
						}
						wk := fake.byPointer(fmt.Sprint(ref))
						nodes = append(nodes, fmt.Sprintf(`{"id":"n-%s","urn":%q,"revision":%d,"approvalStatus":%s}`, wk.id, wk.pointer, wk.revision, approvalStatusJSON(wk.state, wk.revision)))
					}
					fmt.Fprintf(w, `{"data":{"nodeBatch":{"truncated":true,"omitted":[%s],"unavailable":[],"nodes":[%s]}}}`, strings.Join(omitted, ","), strings.Join(nodes, ","))
					return
				}
			}
			nodes, unavailable := []string{}, []string{}
			for _, ref := range refs {
				wk := fake.byPointer(fmt.Sprint(ref))
				if wk == nil || wk.state == "absent" {
					unavailable = append(unavailable, fmt.Sprintf("%q", ref))
					continue
				}
				status := approvalStatusJSON(wk.state, wk.revision)
				if lostRead {
					switch wk.after {
					case "error":
						w.WriteHeader(http.StatusBadGateway)
						_, _ = w.Write([]byte("bad gateway"))
						return
					case "landed":
						status = approvalStatusJSON("APPROVED", wk.revision)
					case "landed-other":
						// An approval is on record, but at another revision.
						status = approvalStatusJSON("APPROVED", wk.revision+1)
					}
				}
				nodes = append(nodes, fmt.Sprintf(`{"id":"n-%s","urn":%q,"revision":%d,"approvalStatus":%s}`, wk.id, wk.pointer, wk.revision, status))
			}
			fmt.Fprintf(w, `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[%s],"nodes":[%s]}}}`, strings.Join(unavailable, ","), strings.Join(nodes, ","))
		case "VerifyNode":
			wk := fake.byPointer(fmt.Sprint(body.Variables["nodeRef"]))
			switch {
			case wk == nil || wk.verify == "error":
				_, _ = w.Write([]byte(batchGQLErr("FORBIDDEN")))
			case wk.verify == "null":
				_, _ = w.Write([]byte(`{"data":{"verifyNode":null}}`))
			default:
				fmt.Fprintf(w, `{"data":{"verifyNode":{"nodeId":"n-%s","urn":%q,"loc":"boot:workers:%s:compiled","state":%q,"revision":%d,"approvedRevision":%d,"expectedHash":"e","actualHash":"a"}}}`,
					wk.id, wk.pointer, wk.id, wk.verify, wk.revision, wk.revision-1)
			}
		case "ApproveNode":
			wk := fake.byPointer(fmt.Sprint(body.Variables["nodeRef"]))
			switch {
			case wk == nil:
				_, _ = w.Write([]byte(batchGQLErr("NOT_FOUND")))
			case wk.approve == "502":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			case wk.approve == "null":
				_, _ = w.Write([]byte(`{"data":{"approveNode":null}}`))
			case wk.approve == "ok" || wk.approve == "":
				rev := int(body.Variables["revision"].(float64))
				fmt.Fprintf(w, `{"data":{"approveNode":{"nodeId":"n-%s","urn":%q,"loc":"boot:workers:%s:compiled","approval":{"revision":%d,"approvedAt":"2026-11-10T00:00:00Z","approvedBy":{"handle":"holger","urn":null},"approvedByInfo":null,"hash":"h"}}}}`,
					wk.id, wk.pointer, wk.id, rev)
			default:
				_, _ = w.Write([]byte(batchGQLErr(wk.approve)))
			}
		case "GetWorkerBootPromptServe":
			wk := fake.byID(fmt.Sprint(body.Variables["id"]))
			switch {
			case wk == nil || wk.serve == "error":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			case wk.serve == "null":
				_, _ = w.Write([]byte(`{"data":{"worker":null}}`))
			case wk.serve == "refused":
				fmt.Fprintf(w, `{"data":{"worker":{"bootPromptRef":%q,"bootPromptTargetDeleted":false,"bootPromptServe":{"served":null,"refusal":"BOOT_PROMPT_NOT_SIGNED"}}}}`, wk.pointer)
			default:
				served := wk.revision
				if wk.serve == "older" {
					served = wk.revision - 2
				}
				fmt.Fprintf(w, `{"data":{"worker":{"bootPromptRef":%q,"bootPromptTargetDeleted":false,"bootPromptServe":{"served":{"pointerLevel":"WORKER","compiledNodeId":"n-%s","servedRevSeq":%d},"refusal":null}}}}`, wk.pointer, wk.id, served)
			}
		default:
			t.Errorf("unexpected operation %q", op)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fake
}

// The standard team: one worker per bucket.
func approveTeam() []approveWorker {
	return []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok", serve: "same"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "APPROVED"},
		{id: "w-cal", name: "Cal"},
		{id: "w-dee", name: "Dee", pointer: cptr("w-dee"), revision: 4, state: "SUPERSEDED", verify: "SUPERSEDED", approve: "ok", serve: "same"},
		{id: "w-eve", name: "Eve", pointer: cptr("w-eve"), revision: 2, deleted: true},
		{id: "w-fay", name: "Fay", retired: true, pointer: cptr("w-fay"), revision: 1, state: "NOT_APPROVED"},
	}
}

type approveOut struct {
	App             string           `json:"app"`
	Applied         bool             `json:"applied"`
	Authority       string           `json:"authority"`
	Limits          []string         `json:"limits"`
	ToApprove       []map[string]any `json:"toApprove"`
	Approved        []map[string]any `json:"approved"`
	AlreadyApproved []map[string]any `json:"alreadyApproved"`
	Tampered        []map[string]any `json:"tampered"`
	Skipped         []map[string]any `json:"skipped"`
	Failed          []map[string]any `json:"failed"`
	Unknown         []map[string]any `json:"unknown"`
}

func runApprove(t *testing.T, workers []approveWorker, args ...string) (*approveFake, string, error) {
	t.Helper()
	return runApproveMode(t, workers, "", args...)
}

func runApproveMode(t *testing.T, workers []approveWorker, mode string, args ...string) (*approveFake, string, error) {
	t.Helper()
	srv, fake := newApproveFake(t, workers)
	fake.mode = mode
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "--app", batchApp, "team", "worker", "approve"}, args...))
	err := root.Execute()
	return fake, out.String(), err
}

func decodeApprove(t *testing.T, out string) approveOut {
	t.Helper()
	var d approveOut
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("--json output is not one JSON object: %v\n%s", err, out)
	}
	return d
}

func approveRefs(calls []approveCall) []string {
	out := []string{}
	for _, c := range calls {
		out = append(out, fmt.Sprintf("%v@%v", c.Vars["nodeRef"], c.Vars["revision"]))
	}
	return out
}

// Pin 1 lives in internal/api/errors_test.go (the mapping) and here (the
// command a user runs today).
func TestNodeApproveApprovalRefusalsExit8(t *testing.T) {
	for _, code := range []string{"BOOT_PROMPT_APPROVAL_FORBIDDEN", "BOOT_PROMPT_WORKER_SESSION_REFUSED"} {
		t.Run(code, func(t *testing.T) {
			srv := fakeGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "ApproveNode": batchGQLErr(code)})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "node", "approve", nodeURN, "--revision", "3"})
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Forbidden {
				t.Fatalf("node approve on %s: want exit %d, got %d (%v)", code, exitcode.Forbidden, got, err)
			}
		})
	}
}

// Pin 2: the preview sends no mutation and lists exactly the own-pointer
// NOT_APPROVED / SUPERSEDED workers.
func TestTeamWorkerApprovePreviewIsReadOnly(t *testing.T) {
	fake, out, err := runApprove(t, approveTeam(), "--json")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if n := len(fake.ops("ApproveNode")); n != 0 {
		t.Fatalf("the preview sent %d approveNode", n)
	}
	d := decodeApprove(t, out)
	if d.Applied {
		t.Error("preview reports applied=true")
	}
	if got := batchNames(d.ToApprove); got != "Ann,Dee" {
		t.Errorf("toApprove = %s, want Ann,Dee", got)
	}
	if got := batchNames(d.AlreadyApproved); got != "Bob" {
		t.Errorf("alreadyApproved = %s, want Bob", got)
	}
	if got := batchNames(d.Skipped); got != "Cal,Eve" {
		t.Errorf("skipped = %s, want Cal,Eve (no pointer, deleted target); retired Fay is not listed", got)
	}
	for _, row := range d.ToApprove {
		if row["revision"] == nil || row["compiledNodeRef"] == nil {
			t.Errorf("a toApprove row must carry its pinned revision and ref: %v", row)
		}
	}
	for _, b := range []string{`"approved":[]`, `"tampered":[]`, `"failed":[]`, `"unknown":[]`} {
		if !strings.Contains(strings.ReplaceAll(out, " ", ""), b) {
			t.Errorf("empty bucket must render as []: missing %s in %s", b, out)
		}
	}
}

func TestTeamWorkerApproveOnlyNarrowsAndDeduplicates(t *testing.T) {
	fake, out, err := runApprove(t, approveTeam(), "--only", "dee,Dee,w-dee", "--apply", "--json")
	if err != nil {
		t.Fatalf("--only: %v", err)
	}
	d := decodeApprove(t, out)
	if got := batchNames(d.Approved); got != "Dee" {
		t.Errorf("approved = %s, want Dee", got)
	}
	if got := approveRefs(fake.ops("ApproveNode")); len(got) != 1 {
		t.Errorf("a worker named three ways is approved once, got %v", got)
	}
}

func TestTeamWorkerApproveOnlyRefusesBeforeAnyWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want int
	}{
		"unknown worker":  {[]string{"--only", "Ann,Zed", "--apply"}, exitcode.NotFound},
		"empty --only":    {[]string{"--only=", "--apply"}, exitcode.Usage},
		"blank entries":   {[]string{"--only", " , ", "--apply"}, exitcode.Usage},
		"worker argument": {[]string{"Ann", "--apply"}, exitcode.Usage},
	} {
		t.Run(name, func(t *testing.T) {
			fake, _, err := runApprove(t, approveTeam(), tc.args...)
			if got := exitCodeFor(err); got != tc.want {
				t.Fatalf("want exit %d, got %d (%v)", tc.want, got, err)
			}
			if n := len(fake.ops("ApproveNode")); n != 0 {
				t.Errorf("a refused selection sent %d approveNode", n)
			}
		})
	}
	// Discriminating control: the same valid --only does write.
	fake, _, err := runApprove(t, approveTeam(), "--only", "Ann", "--apply")
	if err != nil || len(fake.ops("ApproveNode")) != 1 {
		t.Fatalf("control: --only Ann --apply must send one approveNode (err %v, sent %d)", err, len(fake.ops("ApproveNode")))
	}
}

// Pin 3: one approveNode per selected worker, in roster order, each pinned to
// the revision the preview read.
func TestTeamWorkerApproveApplyPinsEachRevision(t *testing.T) {
	fake, out, err := runApprove(t, approveTeam(), "--apply", "--json")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []string{cptr("w-ann") + "@3", cptr("w-dee") + "@4"}
	if got := approveRefs(fake.ops("ApproveNode")); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("approveNode calls = %v, want %v", got, want)
	}
	d := decodeApprove(t, out)
	if !d.Applied || batchNames(d.Approved) != "Ann,Dee" {
		t.Errorf("approved = %s (applied %v), want Ann,Dee", batchNames(d.Approved), d.Applied)
	}
}

// Pin 4: every outcome is reported; the batch exits with the maximum.
func TestTeamWorkerApproveMixedRunReportsAllAndExits8(t *testing.T) {
	team := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok", serve: "same"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "NOT_APPROVED", approve: "REVISION_MISMATCH"},
		{id: "w-cal", name: "Cal", pointer: cptr("w-cal"), revision: 2, state: "NOT_APPROVED", approve: "BOOT_PROMPT_APPROVAL_FORBIDDEN"},
	}
	fake, out, err := runApprove(t, team, "--apply", "--json")
	if got := exitCodeFor(err); got != exitcode.Forbidden {
		t.Fatalf("want exit %d, got %d (%v)", exitcode.Forbidden, got, err)
	}
	if n := len(fake.ops("ApproveNode")); n != 3 {
		t.Fatalf("every selected worker is sent once even after a refusal: got %d", n)
	}
	d := decodeApprove(t, out)
	if batchNames(d.Approved) != "Ann" || batchNames(d.Failed) != "Bob,Cal" {
		t.Fatalf("approved %s, failed %s; want Ann / Bob,Cal", batchNames(d.Approved), batchNames(d.Failed))
	}
	codes := map[string]float64{}
	for _, r := range d.Failed {
		codes[fmt.Sprint(r["code"])] = r["exitCode"].(float64)
	}
	if codes["REVISION_MISMATCH"] != exitcode.Conflict || codes["BOOT_PROMPT_APPROVAL_FORBIDDEN"] != exitcode.Forbidden {
		t.Errorf("per-item exit codes = %v", codes)
	}
}

// Pin 11: a uniform refusal (the caller does not manage the App) still sends
// and reports every worker; the CLI refuses nothing itself.
func TestTeamWorkerApproveUniformRefusalStillReportsEveryWorker(t *testing.T) {
	team := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "BOOT_PROMPT_WORKER_SESSION_REFUSED"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "NOT_APPROVED", approve: "BOOT_PROMPT_WORKER_SESSION_REFUSED"},
	}
	fake, out, err := runApprove(t, team, "--apply", "--json")
	if got := exitCodeFor(err); got != exitcode.Forbidden {
		t.Fatalf("want exit %d, got %d (%v)", exitcode.Forbidden, got, err)
	}
	if n := len(fake.ops("ApproveNode")); n != 2 {
		t.Errorf("sent %d approveNode, want 2", n)
	}
	if got := batchNames(decodeApprove(t, out).Failed); got != "Ann,Bob" {
		t.Errorf("failed = %s, want Ann,Bob", got)
	}
}

// Pin 5: a lost response gets one status read and is never resent.
func TestTeamWorkerApproveLostResponseReadsOnceNeverResends(t *testing.T) {
	for after, outcome := range map[string]string{"landed": "landed", "landed-other": "not-landed", "same": "not-landed", "error": "unknown"} {
		t.Run(after, func(t *testing.T) {
			team := []approveWorker{{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "502", after: after}}
			fake, out, err := runApprove(t, team, "--apply", "--json")
			if got := exitCodeFor(err); got != exitcode.Unavailable {
				t.Fatalf("want exit %d, got %d (%v)", exitcode.Unavailable, got, err)
			}
			if n := len(fake.ops("ApproveNode")); n != 1 {
				t.Fatalf("a lost approval was sent %d times", n)
			}
			if n := len(fake.ops("BootPromptApprovalBatch")); n != 2 {
				t.Errorf("want the preview read plus exactly one status read, got %d reads", n)
			}
			if n := len(fake.ops("GetWorkerBootPromptServe")); n != 0 {
				t.Errorf("an unconfirmed approval must not be reported as served (%d serve reads)", n)
			}
			d := decodeApprove(t, out)
			if len(d.Unknown) != 1 || d.Unknown[0]["outcome"] != outcome {
				t.Fatalf("unknown = %v, want one row with outcome %s", d.Unknown, outcome)
			}
		})
	}
}

// Pins 7 and 10: tamper comes only from verifyNode; a tampered row is never
// sent, and the batch exits 5.
func TestTeamWorkerApproveTamperedFromVerifyOnly(t *testing.T) {
	team := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "SUPERSEDED", verify: "SUPERSEDED", approve: "ok", serve: "same"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "SUPERSEDED", verify: "TAMPERED", approve: "ok"},
	}
	fake, out, err := runApprove(t, team, "--apply", "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("want exit %d, got %d (%v)", exitcode.Conflict, got, err)
	}
	if got := approveRefs(fake.ops("ApproveNode")); len(got) != 1 || !strings.HasPrefix(got[0], cptr("w-ann")) {
		t.Fatalf("only the legitimately superseded row is sent: %v", got)
	}
	d := decodeApprove(t, out)
	if batchNames(d.Tampered) != "Bob" || batchNames(d.Approved) != "Ann" {
		t.Errorf("tampered %s, approved %s; want Bob / Ann", batchNames(d.Tampered), batchNames(d.Approved))
	}
	// verifyNode is asked only about SUPERSEDED rows.
	if n := len(fake.ops("VerifyNode")); n != 2 {
		t.Errorf("verifyNode calls = %d, want 2", n)
	}
	// The preview alone exits 5 too, so a script reading the preview stops.
	_, _, err = runApprove(t, team)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Errorf("preview with a tampered row: want exit %d, got %d", exitcode.Conflict, got)
	}
}

// Gate: an unknown, refused or malformed verify result is never promoted to
// an eligible row.
func TestTeamWorkerApproveUnclearVerifyIsNotEligible(t *testing.T) {
	for _, v := range []string{"error", "null", "INTACT", "NOT_APPROVED"} {
		t.Run(v, func(t *testing.T) {
			team := []approveWorker{{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "SUPERSEDED", verify: v, approve: "ok"}}
			fake, out, err := runApprove(t, team, "--apply", "--json")
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("an unclear verify result: want exit %d, got %d (%v)", exitcode.Error, got, err)
			}
			if n := len(fake.ops("ApproveNode")); n != 0 {
				t.Fatalf("verify %s: the row was sent to approveNode", v)
			}
			if got := batchNames(decodeApprove(t, out).Unknown); got != "Ann" {
				t.Errorf("unknown = %s, want Ann", got)
			}
		})
	}
}

// Pin 8: exactly one serving snapshot per approved worker, reported as read.
func TestTeamWorkerApproveServingSnapshot(t *testing.T) {
	team := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok", serve: "same"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "NOT_APPROVED", approve: "ok", serve: "older"},
		{id: "w-cal", name: "Cal", pointer: cptr("w-cal"), revision: 2, state: "NOT_APPROVED", approve: "ok", serve: "refused"},
		{id: "w-dee", name: "Dee", pointer: cptr("w-dee"), revision: 4, state: "NOT_APPROVED", approve: "ok", serve: "error"},
		{id: "w-eve", name: "Eve", pointer: cptr("w-eve"), revision: 6, state: "NOT_APPROVED", approve: "REVISION_MISMATCH"},
	}
	fake, out, err := runApprove(t, team, "--apply")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("a serving read never changes the exit code: want %d (Eve's mismatch), got %d (%v)", exitcode.Conflict, got, err)
	}
	reads := map[string]int{}
	for _, c := range fake.ops("GetWorkerBootPromptServe") {
		reads[fmt.Sprint(c.Vars["id"])]++
	}
	for _, id := range []string{"w-ann", "w-bob", "w-cal", "w-dee"} {
		if reads[id] != 1 {
			t.Errorf("%s: %d serving reads, want exactly 1", id, reads[id])
		}
	}
	if reads["w-eve"] != 0 {
		t.Error("a failed approval gets no serving read")
	}
	// A failed serving read leaves the approval standing: never resent.
	if n := len(fake.ops("ApproveNode")); n != len(team) {
		t.Errorf("approveNode sent %d times for %d workers", n, len(team))
	}
	for _, want := range []string{
		"approved rev 3; a serve now would give rev 3",
		"approved rev 5; a serve now would still give rev 3",
		"approved rev 2; a serve now would be refused: BOOT_PROMPT_NOT_SIGNED",
		"approved rev 4; serving state unread",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "now live") || strings.Contains(strings.ToLower(out), "next bind serves") {
		t.Errorf("the copy must not promise what the next bind serves:\n%s", out)
	}

	_, jout, _ := runApprove(t, team, "--apply", "--json")
	d := decodeApprove(t, jout)
	serve := map[string]map[string]any{}
	for _, r := range d.Approved {
		s, _ := r["serve"].(map[string]any)
		serve[fmt.Sprint(r["name"])] = s
	}
	if serve["Bob"]["servedRevSeq"] != float64(3) || serve["Cal"]["refusal"] != "BOOT_PROMPT_NOT_SIGNED" || serve["Dee"]["unread"] != true {
		t.Errorf("serve objects = %v", serve)
	}
}

// Pins 6, 9 and 11: help and JSON say the server decides authority and no
// grant is checked; no signing or grant operation is ever sent.
func TestTeamWorkerApproveAuthorityIsTheServers(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "approve", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"server decides", "grant is not checked", "node approve", "--revision", "no rollback"} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q", want)
		}
	}
	fake, jout, _ := runApprove(t, approveTeam(), "--apply", "--json")
	d := decodeApprove(t, jout)
	if !strings.Contains(d.Authority, "server") || !strings.Contains(d.Authority, "grant") {
		t.Errorf("JSON authority = %q", d.Authority)
	}
	if len(d.Limits) == 0 {
		t.Error("JSON limits must name the verify/apply race and the point-in-time serve")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, c := range fake.calls {
		l := strings.ToLower(c.Op)
		if strings.Contains(l, "signature") || strings.Contains(l, "sign") || strings.Contains(l, "grant") || strings.Contains(l, "register") || c.Op == "ApproveMemoryNodes" {
			t.Errorf("the command sent %s", c.Op)
		}
	}
}

// LOCAL F1 / BLIND P2-1: a prompt in an encrypted memory is never sent, but
// the batch is not a success: exit 5 in the preview and under --apply, even
// beside a successful row.
func TestTeamWorkerApproveEncryptedRowExits5WithoutAWrite(t *testing.T) {
	team := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok", serve: "same"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "NOT_APPLICABLE", approve: "ok"},
	}
	for _, args := range [][]string{{"--json"}, {"--apply", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fake, out, err := runApprove(t, team, args...)
			if got := exitCodeFor(err); got != exitcode.Conflict {
				t.Fatalf("want exit %d, got %d (%v)", exitcode.Conflict, got, err)
			}
			for _, c := range fake.ops("ApproveNode") {
				if c.Vars["nodeRef"] == cptr("w-bob") {
					t.Fatal("the encrypted-memory prompt was sent to approveNode")
				}
			}
			d := decodeApprove(t, out)
			if batchNames(d.Skipped) != "Bob" {
				t.Errorf("skipped = %s, want Bob", batchNames(d.Skipped))
			}
			if len(args) == 2 && batchNames(d.Approved) != "Ann" {
				t.Errorf("approved = %s, want Ann beside the encrypted row", batchNames(d.Approved))
			}
		})
	}
}

// Rook P3-1: a compiled prompt absent from nodeBatch (unavailable to the
// caller) is skipped and never written.
func TestTeamWorkerApproveUnavailablePromptIsSkippedNotSent(t *testing.T) {
	team := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok", serve: "same"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, state: "absent", approve: "ok"},
	}
	fake, out, err := runApprove(t, team, "--apply", "--json")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := approveRefs(fake.ops("ApproveNode")); len(got) != 1 || !strings.HasPrefix(got[0], cptr("w-ann")) {
		t.Fatalf("only Ann may be sent: %v", got)
	}
	if got := batchNames(decodeApprove(t, out).Skipped); got != "Bob" {
		t.Errorf("skipped = %s, want Bob", got)
	}
}

// BLIND P2-2: the approval-state read pages at the nodeBatch cap, re-queues a
// truncated page's omitted refs until every ref is read, refuses a truncated
// page that returns no node, and a failed read aborts before any approval.
func TestTeamWorkerApproveApprovalStateReadPaging(t *testing.T) {
	big := []approveWorker{}
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("w-%03d", i)
		big = append(big, approveWorker{id: id, name: fmt.Sprintf("W%03d", i), pointer: cptr(id), revision: 2, state: "APPROVED"})
	}
	t.Run("chunks at the cap", func(t *testing.T) {
		fake, out, err := runApprove(t, big, "--json")
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		sizes := []int{}
		for _, c := range fake.ops("BootPromptApprovalBatch") {
			refs, _ := c.Vars["refs"].([]any)
			sizes = append(sizes, len(refs))
		}
		if fmt.Sprint(sizes) != "[200 1]" {
			t.Errorf("read sizes = %v, want [200 1]", sizes)
		}
		if n := len(decodeApprove(t, out).AlreadyApproved); n != 201 {
			t.Errorf("alreadyApproved = %d, want 201", n)
		}
	})
	t.Run("truncated page re-queued", func(t *testing.T) {
		fake, out, err := runApproveMode(t, big, "truncate-half", "--json")
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		sizes := []int{}
		for _, c := range fake.ops("BootPromptApprovalBatch") {
			refs, _ := c.Vars["refs"].([]any)
			sizes = append(sizes, len(refs))
		}
		// 200 asked, 100 read, 100 omitted re-queued ahead of the last 1.
		if fmt.Sprint(sizes) != "[200 101]" {
			t.Errorf("read sizes = %v, want [200 101]", sizes)
		}
		if n := len(decodeApprove(t, out).AlreadyApproved); n != 201 {
			t.Errorf("alreadyApproved = %d, want every one of 201 read", n)
		}
	})
	for mode, want := range map[string]int{"truncate-empty": exitcode.Error, "fail": exitcode.Unavailable} {
		t.Run(mode, func(t *testing.T) {
			team := []approveWorker{{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok"}}
			fake, _, err := runApproveMode(t, team, mode, "--apply")
			if got := exitCodeFor(err); got != want {
				t.Fatalf("want exit %d, got %d (%v)", want, got, err)
			}
			if n := len(fake.ops("ApproveNode")); n != 0 {
				t.Errorf("a failed preview read must abort before any approval: %d sent", n)
			}
		})
	}
}

// BLIND P3: an empty approval success gets the same one status read as a
// lost response, and is never resent.
func TestTeamWorkerApproveEmptySuccessReadsOnce(t *testing.T) {
	team := []approveWorker{{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "null", after: "landed"}}
	fake, out, err := runApprove(t, team, "--apply", "--json")
	if got := exitCodeFor(err); got != exitcode.Error {
		t.Fatalf("want exit %d, got %d (%v)", exitcode.Error, got, err)
	}
	if n := len(fake.ops("ApproveNode")); n != 1 {
		t.Fatalf("approveNode sent %d times", n)
	}
	d := decodeApprove(t, out)
	if len(d.Unknown) != 1 || d.Unknown[0]["outcome"] != "landed" {
		t.Errorf("unknown = %v, want one row with outcome landed", d.Unknown)
	}
}
