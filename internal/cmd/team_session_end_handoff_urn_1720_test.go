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

func (fn handoffRoundTrip1720) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func runEnd1720(t *testing.T, readBack string, extra ...string) end1720 {
	t.Helper()
	return runEndFault1720(t, readBack, "", extra...)
}

// Inject faults only when the post-commit read reaches the HTTP boundary.
// Record every GraphQL attempt, including repeats; retain the loopback guard.
func runEndFault1720(t *testing.T, readBack, fault string, extra ...string) end1720 {
	t.Helper()
	bindWorktree(t)
	stubs := endStubs()
	if fault == "missing-worker" || fault == "empty-worker" {
		const old = `"workerId":"wkr1",`
		if strings.Count(stubs["EndTeamSession"], old) != 1 {
			t.Fatal("worker-id fixture anchor changed")
		}
		replacement := ""
		if fault == "empty-worker" {
			replacement = `"workerId":"",`
		}
		stubs["EndTeamSession"] = strings.Replace(stubs["EndTeamSession"], old, replacement, 1)
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
		inject := call.Operation == "GetWorkerHandoffReadBack" && (fault == "transport" || fault == "cancel" || fault == "http-500" || fault == "graphql-errors" || fault == "malformed-json")
		mu.Unlock()
		if inject {
			if fault == "cancel" {
				cancel()
				if !errors.Is(req.Context().Err(), context.Canceled) {
					t.Error("read-back request did not inherit command cancellation")
				}
				return nil, req.Context().Err()
			}
			if fault == "transport" {
				return nil, errors.New("fixture read-back transport failure")
			}
			status, body := http.StatusOK, `{"errors":[{"message":"fixture read-back refused"}]}`
			if fault == "http-500" {
				status = http.StatusInternalServerError
			}
			if fault == "malformed-json" {
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
	for _, fault := range []string{"transport", "cancel", "http-500", "graphql-errors", "malformed-json", "missing-worker", "empty-worker"} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fault+map[bool]string{false: "/text", true: "/json"}[jsonOutput], func(t *testing.T) {
				args := []string{"--handoff", "text"}
				if jsonOutput {
					args = append(args, "--json")
				}
				r := runEndFault1720(t, readBackJSON1720("CURRENT", "s-new", handoffURN1720), fault, args...)
				if r.err != nil || r.errOut != "" {
					t.Fatalf("committed end must remain successful: err=%v stderr=%q stdout=%q", r.err, r.errOut, r.out)
				}
				wantReads := 1
				if fault == "missing-worker" || fault == "empty-worker" {
					wantReads = 0
				}
				assertHandoffRequests1799(t, r.calls, wantReads)
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

func assertHandoffRequests1799(t *testing.T, calls []handoffRequest1720, reads int) {
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
		assertHandoffRequests1799(t, r.calls, 1)
		if jsonOutput {
			var dto map[string]any
			if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["handoff"] != handoffURN1720 {
				t.Fatalf("positive-control handoff absent: %s", r.out)
			}
		} else if !strings.Contains(r.out, "handoff saved: "+handoffURN1720) {
			t.Fatalf("positive-control handoff absent: %q", r.out)
		}
	}
}
