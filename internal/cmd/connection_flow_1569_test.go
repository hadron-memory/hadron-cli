package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmd/connection"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1569: `connection connect` waits on connectionFlow(flowId) instead of
// diffing the org's connection list, `--no-wait` carries the flowId, and
// `connection flow <id>` reads the flow once. Red on main: connectionFlow is
// reached by no command, the wait lists connections, `connection flow` is an
// unknown command.

func flowResp(status, connID, failure string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	return `{"data":{"connectionFlow":{"status":"` + status + `","connectionId":` + q(connID) + `,"failureCode":` + q(failure) + `}}}`
}

// runFlowConnect drives `connection connect` against a scripted flow: flows
// answers each ConnectionFlow call in order (the last repeats); fill answers
// the one EmailConnections read after CONNECTED.
func runFlowConnect(t *testing.T, flows []string, fill string, args ...string) *connectRun {
	t.Helper()
	r := &connectRun{}
	clock := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	prev := connection.Hooks
	t.Cleanup(func() { connection.Hooks = prev })
	connection.Hooks.Now = func() time.Time { return clock }
	connection.Hooks.Sleep = func(_ context.Context, d time.Duration) error { clock = clock.Add(d); r.slept += d; return nil }
	connection.Hooks.Open = func(u string) error { r.opened = append(r.opened, u); return nil }
	calls := 0
	srv, vars := captureGraphQLFunc(t, func(op string) string {
		r.ops = append(r.ops, op)
		switch op {
		case "StartConnection":
			return connectStart
		case "ConnectionFlow":
			i := min(calls, len(flows)-1)
			calls++
			return flows[i]
		case "EmailConnections":
			return fill
		}
		return ""
	})
	r.vars = vars
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, args...))
	r.err = root.Execute()
	if r.err != nil {
		r.code = renderError(f, r.err)
	}
	r.out = out.String()
	r.errOut = f.IOStreams.ErrOut.(*strings.Builder).String()
	return r
}

func count(ops []string, op string) int {
	n := 0
	for _, o := range ops {
		if o == op {
			n++
		}
	}
	return n
}

// The wait polls the flow, correlates on its connectionId, and never lists
// connections to find "the new one" or asks who the caller is.
func TestConnectWaitsOnTheFlowNotTheConnectionList(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("PENDING", "", ""), flowResp("PENDING", "", ""), flowResp("CONNECTED", "c9", "")},
		connRows(connRow("c9"), connRow("c1")), "connection", "connect", "gmail", "--org", "org_1", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if n := count(r.ops, "ConnectionFlow"); n != 3 {
		t.Fatalf("want 3 flow reads, ops %v", r.ops)
	}
	if count(r.ops, "AuthContext") != 0 || count(r.ops, "EmailConnections") != 1 {
		t.Fatalf("no baseline/self lookup, one fill read; ops %v", r.ops)
	}
	var v struct{ FlowID string }
	_ = json.Unmarshal(r.vars["ConnectionFlow"], &v)
	if v.FlowID != "flow_1" {
		t.Fatalf("flow variables %s", r.vars["ConnectionFlow"])
	}
	var dto struct {
		Status     string `json:"status"`
		FlowID     string `json:"flowId"`
		Connection struct {
			ID           string `json:"id"`
			MailboxEmail string `json:"mailboxEmail"`
		} `json:"connection"`
	}
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.Status != "connected" || dto.FlowID != "flow_1" || dto.Connection.ID != "c9" || dto.Connection.MailboxEmail != "c9@acme.com" {
		t.Fatalf("dto %+v err %v out %q", dto, err, r.out)
	}
}

// A reconnect resolves to the existing row's id (the old wait timed out: the
// row predated the baseline), and a sibling flow's connection is not taken.
func TestConnectReconnectResolvesToTheExistingConnection(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("CONNECTED", "old", "")},
		connRows(connRow("old"), connRow("other-flow")), "connection", "connect", "gmail", "--org", "org_1", "--json")
	if r.err != nil || !strings.Contains(r.out, `"id": "old"`) {
		t.Fatalf("err %v out %q", r.err, r.out)
	}
}

// The success is never lost: a failed fill read still reports the id.
func TestConnectedFillFailureStillReportsTheID(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")},
		`{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, "connection", "connect", "gmail", "--org", "org_1", "--json")
	if r.err != nil || !strings.Contains(r.out, `"id": "c9"`) || !strings.Contains(r.out, `"status": "connected"`) {
		t.Fatalf("err %v out %q", r.err, r.out)
	}
}

// FAILED is the flow's outcome: the stable code is the result, exit 1 (not 13,
// not 8), no further polling.
func TestConnectFailedFlowReportsTheCodeAndExits1(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("PENDING", "", ""), flowResp("FAILED", "", "access_denied")}, connRows(),
		"connection", "connect", "gmail", "--org", "org_1", "--json")
	if r.code != exitcode.Error {
		t.Fatalf("exit %d (%v), want %d", r.code, r.err, exitcode.Error)
	}
	var dto struct {
		Status      string `json:"status"`
		FailureCode string `json:"failureCode"`
	}
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.Status != "failed" || dto.FailureCode != "access_denied" {
		t.Fatalf("dto %+v err %v out %q", dto, err, r.out)
	}
	if count(r.ops, "ConnectionFlow") != 2 {
		t.Fatalf("polled past the outcome: %v", r.ops)
	}
}

// PENDING is an answer: only the deadline ends the wait, with the unchanged 13.
func TestConnectPendingUntilDeadlineExits13(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("PENDING", "", "")}, connRows(), "connection", "connect", "gmail", "--org", "org_1", "--wait", "10s")
	if r.code != exitcode.WaitTimeout || !strings.Contains(r.err.Error(), "not found yet") || r.slept > 10*time.Second {
		t.Fatalf("exit %d err %v slept %s", r.code, r.err, r.slept)
	}
}

// One refusal: unknown, expired and foreign flow ids carry different server
// messages but the CLI's stderr and exit are byte-identical, mid-wait included
// (ends the command, is not retried, is not "not found yet").
func TestFlowRefusalIsOneMessageOneExit(t *testing.T) {
	msgs := []string{"flow not found", "flow expired 2h ago", "flow belongs to user u7"}
	var errs, codes []string
	for _, m := range msgs {
		refuse := `{"errors":[{"message":"` + m + `","extensions":{"code":"FORBIDDEN"}}],"data":null}`
		r := runFlowConnect(t, []string{flowResp("PENDING", "", ""), refuse}, connRows(), "connection", "connect", "gmail", "--org", "org_1")
		if r.code != exitcode.Forbidden {
			t.Fatalf("%q: exit %d (%v), want %d", m, r.code, r.err, exitcode.Forbidden)
		}
		if count(r.ops, "ConnectionFlow") != 2 {
			t.Fatalf("%q: retried a refusal: %v", m, r.ops)
		}
		errs = append(errs, r.err.Error())
		codes = append(codes, r.errOut)

		g := runFlowConnect(t, []string{refuse}, connRows(), "connection", "flow", "flow_1")
		if g.code != exitcode.Forbidden {
			t.Fatalf("flow %q: exit %d (%v)", m, g.code, g.err)
		}
		errs = append(errs, g.err.Error())
		codes = append(codes, g.errOut)
	}
	for i := 2; i < len(errs); i += 2 {
		if errs[i] != errs[0] || codes[i] != codes[0] || errs[i+1] != errs[1] || codes[i+1] != codes[1] {
			t.Fatalf("refusals distinguishable:\n%q\n%q", errs, codes)
		}
	}
	for _, e := range errs {
		for _, leak := range msgs {
			if strings.Contains(e, leak) {
				t.Fatalf("server's case-specific text leaked: %q", e)
			}
		}
	}
}

// --no-wait carries the flowId, so the caller can follow it.
func TestConnectNoWaitCarriesTheFlowID(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("PENDING", "", "")}, connRows(), "connection", "connect", "gmail", "--org", "org_1", "--no-wait", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var dto struct {
		FlowID string `json:"flowId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.FlowID != "flow_1" || dto.Status != "started" {
		t.Fatalf("dto %+v err %v out %q", dto, err, r.out)
	}
	if count(r.ops, "ConnectionFlow") != 0 {
		t.Fatalf("--no-wait polled: %v", r.ops)
	}
	h := runFlowConnect(t, nil2(), connRows(), "connection", "connect", "gmail", "--org", "org_1", "--no-wait", "--no-browser")
	if h.err != nil || !strings.Contains(h.out+h.errOut, "flow_1") {
		t.Fatalf("human line lacks the flow id: %q %q", h.out, h.errOut)
	}
}

func nil2() []string { return []string{flowResp("PENDING", "", "")} }

// `connection flow <id>` reads once; any resolved state exits 0; the --json DTO
// carries every field with explicit null for the unused ones.
func TestConnectionFlowReadsOnceAndExits0ForEveryResolvedState(t *testing.T) {
	cases := []struct {
		name, resp, want string
	}{
		{"pending", flowResp("PENDING", "", ""), `"status": "PENDING"`},
		{"connected", flowResp("CONNECTED", "c9", ""), `"connectionId": "c9"`},
		{"failed", flowResp("FAILED", "", "access_denied"), `"failureCode": "access_denied"`},
	}
	for _, tc := range cases {
		r := runFlowConnect(t, []string{tc.resp}, connRows(), "connection", "flow", "flow_1", "--json")
		if r.err != nil || r.code != 0 {
			t.Fatalf("%s: exit %d err %v", tc.name, r.code, r.err)
		}
		if !strings.Contains(r.out, tc.want) || !strings.Contains(r.out, `"flowId": "flow_1"`) {
			t.Fatalf("%s: out %q", tc.name, r.out)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(r.out), &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"flowId", "status", "connectionId", "failureCode"} {
			if _, ok := m[k]; !ok {
				t.Fatalf("%s: %q omitted from %q", tc.name, k, r.out)
			}
		}
		if len(r.ops) != 1 || r.ops[0] != "ConnectionFlow" {
			t.Fatalf("%s: want one read, ops %v", tc.name, r.ops)
		}
	}
}

// The "known limit" is gone: the flow id correlates now.
func TestConnectHelpNoLongerStatesTheConcurrentFlowLimit(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "connect", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	h := out.String()
	for _, stale := range []string{"Known limit", "cannot be told apart", "carries no flow id"} {
		if strings.Contains(h, stale) {
			t.Errorf("help still says %q", stale)
		}
	}
	if !strings.Contains(h, "flow") {
		t.Errorf("help does not mention the flow id")
	}
}

// A timeout still hands over the record: the flow may yet finish, and its flowId
// is how a --json caller (or a person) follows it. The start notice names it too.
func TestConnectTimeoutPrintsTheRecordWithTheFlowIDBeforeTheError(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("PENDING", "", "")}, connRows(), "connection", "connect", "gmail", "--org", "org_1", "--wait", "10s", "--json")
	if r.code != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v)", r.code, r.err)
	}
	var dto struct {
		FlowID string `json:"flowId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(r.out[:strings.Index(r.out, "}\n")+1]), &dto); err != nil || dto.FlowID != "flow_1" || dto.Status != "pending" {
		t.Fatalf("dto %+v err %v out %q", dto, err, r.out)
	}
	h := runFlowConnect(t, []string{flowResp("PENDING", "", "")}, connRows(), "connection", "connect", "gmail", "--org", "org_1", "--wait", "10s")
	if !strings.Contains(h.errOut, "flow flow_1") {
		t.Fatalf("start notice lacks the flow id: %q", h.errOut)
	}
}
