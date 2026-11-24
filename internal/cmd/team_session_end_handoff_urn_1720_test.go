package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// cli#1720: `session end --handoff` shows where the handoff landed. The end is
// committed (and, with a handoff, the server wrote it first, cor:agt:020:10), so
// a read-back that fails or does not match must change one line and nothing
// else: not the exit code, not the success line, not the JSON keys.

const handoffURN1720 = "hrn:node:acme.com:team:handoffs:s-new"

func readBackJSON1720(status, previousID, urn string) string {
	handoff := "null"
	if urn != "" {
		handoff = `{"urn":"` + urn + `"}`
	}
	prev := "null"
	if previousID != "" {
		prev = `{"id":"` + previousID + `"}`
	}
	return `{"data":{"worker":{"continuity":{"status":"` + status + `","handoff":` + handoff + `,"previousSession":` + prev + `}}}}`
}

type end1720 struct {
	out, errOut string
	err         error
	captured    map[string]json.RawMessage
	calls       []handoffRequest1720
}

type handoffRequest1720 struct {
	Operation string          `json:"operationName"`
	Variables json.RawMessage `json:"variables"`
}

type handoffRoundTrip1720 func(*http.Request) (*http.Response, error)

// handoffFault1720 names the fault a run injects (cli#2258 F5), instead of a bare string selector
// compared in several places. A fault is either injected at the HTTP boundary of the post-commit
// read-back (the transport, cancel, 500, GraphQL-error and malformed-JSON faults) or edits the end
// answer so no read-back is made (the missing/empty worker id faults).
type handoffFault1720 string

const (
	faultNone1720          handoffFault1720 = ""
	faultTransport1720     handoffFault1720 = "transport"
	faultCancel1720        handoffFault1720 = "cancel"
	faultHTTP500_1720      handoffFault1720 = "http-500"
	faultGraphQLErrors1720 handoffFault1720 = "graphql-errors"
	faultMalformedJSON1720 handoffFault1720 = "malformed-json"
	faultMissingWorker1720 handoffFault1720 = "missing-worker"
	faultEmptyWorker1720   handoffFault1720 = "empty-worker"
)

// injectsAtReadBack reports whether the fault is injected when the read-back reaches the HTTP boundary.
func (f handoffFault1720) injectsAtReadBack() bool {
	switch f {
	case faultTransport1720, faultCancel1720, faultHTTP500_1720, faultGraphQLErrors1720, faultMalformedJSON1720:
		return true
	}
	return false
}

// editsEndAnswer reports whether the fault changes the end answer so the worker id is absent or empty,
// which leaves no read-back to make.
func (f handoffFault1720) editsEndAnswer() bool {
	return f == faultMissingWorker1720 || f == faultEmptyWorker1720
}

// withWorkerIDFault1720 edits the end answer structurally (cli#2258 F6): decode, drop or blank the
// session's workerId, encode. A drifted fixture (no endSession object or no workerId key) fails the
// test loudly, as the old guarded literal replacement did.
func withWorkerIDFault1720(t *testing.T, endAnswer string, fault handoffFault1720) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(endAnswer), &doc); err != nil {
		t.Fatalf("end fixture is not JSON: %v", err)
	}
	data, _ := doc["data"].(map[string]any)
	session, _ := data["endSession"].(map[string]any)
	if _, ok := session["workerId"]; !ok {
		t.Fatal("worker-id fixture shape changed: no data.endSession.workerId")
	}
	if fault == faultEmptyWorker1720 {
		session["workerId"] = ""
	} else {
		delete(session, "workerId")
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func (fn handoffRoundTrip1720) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func runEnd1720(t *testing.T, readBack string, extra ...string) end1720 {
	t.Helper()
	return runEndFault1720(t, readBack, faultNone1720, extra...)
}

// Inject faults only when the post-commit read reaches the HTTP boundary.
// Record every GraphQL attempt, including repeats; retain the loopback guard.
func runEndFault1720(t *testing.T, readBack string, fault handoffFault1720, extra ...string) end1720 {
	t.Helper()
	bindWorktree(t)
	stubs := endStubs()
	if fault.editsEndAnswer() {
		stubs["EndTeamSession"] = withWorkerIDFault1720(t, stubs["EndTeamSession"], fault)
	}
	if readBack != "" {
		stubs["GetWorkerHandoffReadBack"] = readBack
	}
	gql, captured := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if f.HTTPClient == nil {
		t.Fatal("test factory must provide an HTTP client")
	}
	base := f.HTTPClient.Transport
	if base == nil {
		t.Fatal("test factory must retain its loopback transport guard")
	}
	var mu sync.Mutex
	var calls []handoffRequest1720
	f.HTTPClient.Transport = handoffRoundTrip1720(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			return base.RoundTrip(req)
		}
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		defer req.Body.Close()
		var call handoffRequest1720
		if err := json.Unmarshal(body, &call); err != nil {
			return nil, err
		}
		mu.Lock()
		calls = append(calls, call)
		inject := call.Operation == "GetWorkerHandoffReadBack" && fault.injectsAtReadBack()
		mu.Unlock()
		if inject {
			if fault == faultCancel1720 {
				cancel()
				if !errors.Is(req.Context().Err(), context.Canceled) {
					t.Error("read-back request did not inherit command cancellation")
				}
				// An explicit sentinel (cli#2258 F3), never req.Context().Err(): that is nil when the
				// inherited-context assertion above has already failed, and a fixture must not hand
				// RoundTrip a (nil, nil).
				return nil, context.Canceled
			}
			if fault == faultTransport1720 {
				return nil, errors.New("fixture read-back transport failure")
			}
			status, body := http.StatusOK, `{"errors":[{"message":"fixture read-back refused"}]}`
			if fault == faultHTTP500_1720 {
				status = http.StatusInternalServerError
			}
			if fault == faultMalformedJSON1720 {
				body = `{`
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		}
		return base.RoundTrip(req)
	})
	root := NewRootCmd(f)
	args := append([]string{"team", "session", "end", "--server", gql.URL}, extra...)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	mu.Lock()
	defer mu.Unlock()
	return end1720{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err, captured: captured, calls: append([]handoffRequest1720(nil), calls...)}
}

func TestSessionEndShowsTheSavedHandoffURN1720(t *testing.T) {
	r := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), "--handoff", "text")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "✓ ended session") || !strings.Contains(r.out, "handoff saved: "+handoffURN1720) {
		t.Errorf("text output must carry the URN under the ✓ line:\n%s", r.out)
	}
	var vars map[string]any
	_ = json.Unmarshal(r.captured["GetWorkerHandoffReadBack"], &vars)
	if vars["ref"] != "wkr1" {
		t.Errorf("the read-back must ask the ended session's worker: %v", vars)
	}
	j := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), "--handoff", "text", "--json")
	var dto map[string]any
	if err := json.Unmarshal([]byte(j.out), &dto); err != nil {
		t.Fatalf("json: %v\n%s", err, j.out)
	}
	if dto["handoff"] != handoffURN1720 || dto["sessionId"] != "s-new" {
		t.Errorf("--json must add handoff and keep its keys: %v", dto)
	}
}

func TestSessionEndWithoutAHandoffPrintsNothingNew1720(t *testing.T) {
	r := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), "--summary", "x")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, asked := r.captured["GetWorkerHandoffReadBack"]; asked {
		t.Errorf("no handoff was sent: there is nothing to read back")
	}
	if strings.Contains(r.out, "handoff") {
		t.Errorf("output must be unchanged:\n%s", r.out)
	}
	j := runEnd1720(t, "", "--summary", "x", "--json")
	var dto map[string]json.RawMessage
	_ = json.Unmarshal([]byte(j.out), &dto)
	if _, has := dto["handoff"]; has {
		t.Errorf("no handoff key without a handoff: %s", j.out)
	}
}

func TestSessionEndReadBackFailureChangesOneLine1720(t *testing.T) {
	cases := map[string]string{
		"graphql error":      `{"errors":[{"message":"boom"}]}`,
		"null worker":        `{"data":{"worker":null}}`,
		"null continuity":    `{"data":{"worker":{"continuity":null}}}`,
		"status MISSING":     readBackJSON1720("MISSING", "s-new", handoffURN1720),
		"different session":  readBackJSON1720("CURRENT", "s-other", handoffURN1720),
		"no previousSession": readBackJSON1720("CURRENT", "", handoffURN1720),
		"no handoff":         readBackJSON1720("CURRENT", "s-new", ""),
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			r := runEnd1720(t, resp, "--handoff", "text")
			if r.err != nil {
				t.Fatalf("a failed read-back must not change the exit code: %v", r.err)
			}
			if !strings.Contains(r.out, "✓ ended session") || !strings.Contains(r.out, "handoff saved; URN not read back (") {
				t.Errorf("success line plus the not-read-back line expected:\n%s", r.out)
			}
			if strings.Contains(r.out+r.errOut, handoffURN1720) {
				t.Errorf("a non-matching read-back must never print a URN:\n%s", r.out)
			}
			j := runEnd1720(t, resp, "--handoff", "text", "--json")
			if j.err != nil {
				t.Fatal(j.err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(j.out), &dto); err != nil {
				t.Fatalf("json must stay clean: %v\n%s", err, j.out)
			}
			if _, has := dto["handoff"]; has || dto["sessionId"] != "s-new" {
				t.Errorf("--json omits handoff and keeps the rest: %v", dto)
			}
		})
	}
}

func TestSessionEndOnAServerWithoutContinuityIsToday1720(t *testing.T) {
	r := runEnd1720(t, "", "--handoff", "text")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.out != "✓ ended session s-new (worker Iris)\n" {
		t.Errorf("unexpected output: %q", r.out)
	}
	if strings.Contains(r.out, "handoff") {
		t.Errorf("an old server prints today's output, nothing more:\n%s", r.out)
	}
}

// Read-back is narration after a committed end: even cancellation must not
// turn the successful mutation into an error or cause a retry/second write.
func TestSessionEndReadBackFaults1799(t *testing.T) {
	for _, fault := range []handoffFault1720{faultTransport1720, faultCancel1720, faultHTTP500_1720, faultGraphQLErrors1720, faultMalformedJSON1720, faultMissingWorker1720, faultEmptyWorker1720} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(string(fault)+map[bool]string{false: "/text", true: "/json"}[jsonOutput], func(t *testing.T) {
				args := []string{"--handoff", "text"}
				if jsonOutput {
					args = append(args, "--json")
				}
				r := runEndFault1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), fault, args...)
				if r.err != nil || r.errOut != "" {
					t.Fatalf("committed end must remain successful: err=%v stderr=%q stdout=%q", r.err, r.errOut, r.out)
				}
				wantReads := 1
				if fault.editsEndAnswer() {
					wantReads = 0
				}
				assertHandoffRequests1720(t, r.calls, wantReads)
				if jsonOutput {
					var dto map[string]any
					if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
						t.Fatalf("invalid JSON: %v: %s", err, r.out)
					}
					want := map[string]any{"sessionId": "s-new", "workerName": "Iris", "endedAt": "2026-08-10T18:00:00Z", "resumeClaim": false}
					if !reflect.DeepEqual(dto, want) {
						t.Fatalf("degraded end payload=%v want=%v", dto, want)
					}
				} else {
					lines := strings.Split(strings.TrimSuffix(r.out, "\n"), "\n")
					if len(lines) != 2 || lines[0] != "✓ ended session s-new (worker Iris)" || !strings.HasPrefix(lines[1], "  handoff saved; URN not read back (") || !strings.HasSuffix(lines[1], ")") {
						t.Fatalf("degraded text: %q", r.out)
					}
					if strings.Contains(r.out, handoffURN1720) {
						t.Fatalf("unverified URN printed: %q", r.out)
					}
				}
			})
		}
	}
}

// assertHandoffRequests1720 checks the exact GraphQL traffic of one `session end --handoff`: the end
// first, then reads read-back attempts for the ended session's worker. The #1799 tests below share this
// file's helpers and keep the 1720 suffix of the file they live in.
func assertHandoffRequests1720(t *testing.T, calls []handoffRequest1720, reads int) {
	t.Helper()
	if len(calls) != 1+reads {
		t.Fatalf("want one end and %d read-back attempts, got %+v", reads, calls)
	}
	for i, call := range calls {
		wantOp := "EndTeamSession"
		wantVars := map[string]any{"id": "s-new", "handoff": "text"}
		if i == 1 {
			wantOp = "GetWorkerHandoffReadBack"
			wantVars = map[string]any{"ref": "wkr1"}
		}
		var vars map[string]any
		if err := json.Unmarshal(call.Variables, &vars); err != nil {
			t.Fatal(err)
		}
		if call.Operation != wantOp || !reflect.DeepEqual(vars, wantVars) {
			t.Fatalf("request %d: %s %s; want %s %v", i, call.Operation, call.Variables, wantOp, wantVars)
		}
	}
}

func TestSessionEndReadBackRequestShapeAndCounts1799(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		args := []string{"--handoff", "text"}
		if jsonOutput {
			args = append(args, "--json")
		}
		r := runEnd1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), args...)
		if r.err != nil || r.errOut != "" {
			t.Fatalf("end: %v %s", r.err, r.errOut)
		}
		assertHandoffRequests1720(t, r.calls, 1)
		if jsonOutput {
			var dto map[string]any
			if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
				t.Fatal(err)
			}
			// Compared whole, like the degraded payload above (cli#2258 F7): the success payload is
			// the degraded one plus the handoff URN, nothing else.
			want := map[string]any{"sessionId": "s-new", "workerName": "Iris", "endedAt": "2026-08-10T18:00:00Z", "resumeClaim": false, "handoff": handoffURN1720}
			if !reflect.DeepEqual(dto, want) {
				t.Fatalf("positive-control payload=%v want=%v", dto, want)
			}
		} else if !strings.Contains(r.out, "handoff saved: "+handoffURN1720) {
			t.Fatalf("positive-control handoff absent: %q", r.out)
		}
	}
}
