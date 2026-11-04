package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#985: deleted-node recovery — `node recovery ls|get|graph` and `node
// restore` against the compiled command tree and a fake GraphQL server.

const (
	recMemory  = "hrn:mem:acme.com:kb"
	recNodeURN = "hrn:node:acme.com:kb:findings:flaky-ci"
)

// recCall is one request the fake received.
type recCall struct {
	op   string
	vars map[string]any
	raw  string // the whole query document
}

// recoveryServer answers by operation name; respond may return "" to fall
// through to an unexpected-operation failure. Every request is recorded.
func recoveryServer(t *testing.T, respond func(c recCall) string) (*httptest.Server, func() []recCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []recCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		c := recCall{op: body.OperationName, vars: body.Variables, raw: body.Query}
		if c.op == "GetBoundSessionEnd" {
			_, _ = w.Write([]byte(`{"data":{"session":{"id":"s","endedAt":null,"endReason":null}}}`))
			return
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		resp := respond(c)
		if resp == "" {
			t.Errorf("unexpected operation %q", c.op)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]recCall(nil), calls...)
	}
}

func recRun(t *testing.T, srv *httptest.Server, args ...string) (stdout, stderr string, code int, err error) {
	t.Helper()
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	err = root.Execute()
	if err != nil {
		renderError(f, err)
		code = exitCodeFor(err)
	}
	return out.String(), errOut.String(), code, err
}

func recSummary(i int) string {
	return fmt.Sprintf(`{"id":"n%d","urn":"hrn:node:acme.com:kb:loc%d","memoryId":"m1","loc":"loc%d","name":"Node %d","nodeType":"info","deletedAt":"2026-10-01T00:00:00Z"}`, i, i, i, i)
}

// recPage builds a recoveryNodes reply of count rows starting at index start.
func recPage(start, count, total int) string {
	rows := make([]string, 0, count)
	for i := start; i < start+count; i++ {
		rows = append(rows, recSummary(i))
	}
	return fmt.Sprintf(`{"data":{"recoveryNodes":{"total":%d,"items":[%s]}}}`, total, strings.Join(rows, ","))
}

func recOnly(t *testing.T, calls []recCall, op string) []recCall {
	t.Helper()
	var out []recCall
	for _, c := range calls {
		if c.op == op {
			out = append(out, c)
		}
	}
	return out
}

func TestRecoveryLsSendsExplicitLimitAndOmitsZeroOffset(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return recPage(0, 2, 2) })
	out, _, _, err := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory, "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := recOnly(t, calls(), "RecoveryNodes")
	if len(got) != 1 {
		t.Fatalf("want one request, got %d", len(got))
	}
	if got[0].vars["limit"] != float64(20) {
		t.Errorf("limit must be sent explicitly as 20, got %v", got[0].vars["limit"])
	}
	if _, present := got[0].vars["offset"]; present {
		t.Errorf("a zero offset must be omitted, not sent: %v", got[0].vars)
	}
	if got[0].vars["memoryRef"] != recMemory {
		t.Errorf("memoryRef = %v", got[0].vars["memoryRef"])
	}
	var dto struct {
		Items   []map[string]any `json:"items"`
		Total   int              `json:"total"`
		Limit   *int             `json:"limit"`
		Offset  int              `json:"offset"`
		HasMore bool             `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(dto.Items) != 2 || dto.Total != 2 || dto.Limit == nil || *dto.Limit != 20 || dto.HasMore {
		t.Errorf("unexpected dto: %+v", dto)
	}
}

func TestRecoveryLsEmptyIsExitZeroAndNonNullItems(t *testing.T) {
	srv, _ := recoveryServer(t, func(c recCall) string { return recPage(0, 0, 0) })
	out, _, code, err := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory, "--json")
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	if !strings.Contains(out, `"items": []`) {
		t.Errorf("items must render [] not null: %s", out)
	}
	text, _, _, _ := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory)
	if !strings.Contains(text, "No deleted nodes in "+recMemory) {
		t.Errorf("text empty message: %q", text)
	}
}

func TestRecoveryLsMoreHintGoesToStderrOnly(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return recPage(0, 20, 45) })
	out, errOut, _, err := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "showing 1-20 of 45; more: --offset 20") {
		t.Errorf("stderr must carry the continuation hint: %q", errOut)
	}
	if strings.Contains(out, "showing") {
		t.Errorf("stdout must not carry the hint: %q", out)
	}
	if !strings.Contains(out, `"hasMore": true`) {
		t.Errorf("json hasMore: %s", out)
	}
	if n := len(recOnly(t, calls(), "RecoveryNodes")); n != 1 {
		t.Errorf("no unprompted second request, got %d", n)
	}
}

func TestRecoveryLsOffsetSentWhenNonZero(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return recPage(20, 5, 25) })
	_, errOut, _, err := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory, "--limit", "5", "--offset", "20")
	if err != nil {
		t.Fatal(err)
	}
	v := recOnly(t, calls(), "RecoveryNodes")[0].vars
	if v["limit"] != float64(5) || v["offset"] != float64(20) {
		t.Errorf("vars = %v", v)
	}
	if strings.Contains(errOut, "more:") {
		t.Errorf("last page must not claim more: %q", errOut)
	}
}

func TestRecoveryLsAllDrainsEveryPageByRowsReceived(t *testing.T) {
	// A short middle page (a lower enforced cap): advancing by the asked-for
	// limit would skip rows, advancing by rows received does not.
	pages := map[int]string{0: recPage(0, 200, 450), 200: recPage(200, 150, 450), 350: recPage(350, 100, 450)}
	srv, calls := recoveryServer(t, func(c recCall) string {
		off := 0
		if v, ok := c.vars["offset"].(float64); ok {
			off = int(v)
		}
		return pages[off]
	})
	out, _, _, err := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory, "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := recOnly(t, calls(), "RecoveryNodes")
	if len(got) != 3 {
		t.Fatalf("want 3 pages, got %d", len(got))
	}
	for _, c := range got {
		if c.vars["limit"] != float64(200) {
			t.Errorf("--all must page at the explicit cap 200, got %v", c.vars["limit"])
		}
	}
	var dto struct {
		Items   []map[string]any `json:"items"`
		Limit   *int             `json:"limit"`
		HasMore bool             `json:"hasMore"`
	}
	_ = json.Unmarshal([]byte(out), &dto)
	if len(dto.Items) != 450 || dto.Limit != nil || dto.HasMore {
		t.Errorf("--all dto: items=%d limit=%v hasMore=%v", len(dto.Items), dto.Limit, dto.HasMore)
	}
}

func TestRecoveryLsAllLaterPageFailurePrintsNothing(t *testing.T) {
	srv, _ := recoveryServer(t, func(c recCall) string {
		if _, ok := c.vars["offset"]; ok {
			return `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
		}
		return recPage(0, 200, 450)
	})
	out, _, code, err := recRun(t, srv, "node", "recovery", "ls", "-m", recMemory, "--all")
	if err == nil || code == 0 {
		t.Fatalf("a failed later page must fail the command, code %d", code)
	}
	if out != "" {
		t.Errorf("no partial list may be printed, got %q", out)
	}
}

func TestRecoveryPagingFlagRefusals(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return recPage(0, 0, 0) })
	for _, args := range [][]string{
		{"--all", "--limit", "5"},
		{"--all", "--offset", "5"},
		{"--limit", "0"},
		{"--limit", "201"},
		{"--offset", "-1"},
	} {
		_, _, code, err := recRun(t, srv, append([]string{"node", "recovery", "ls", "-m", recMemory}, args...)...)
		if err == nil || code != exitcode.Usage {
			t.Errorf("%v: want usage refusal, got code %d err %v", args, code, err)
		}
	}
	if _, _, code, _ := recRun(t, srv, "node", "recovery", "ls"); code != exitcode.Usage {
		t.Errorf("a missing --memory is a usage refusal, got %d", code)
	}
	if n := len(calls()); n != 0 {
		t.Errorf("refusals are local: %d requests were sent", n)
	}
}

func TestRecoveryGraphJSONAndTable(t *testing.T) {
	edges := `{"data":{"recoveryEdges":{"total":1,"items":[{"id":"e1","name":"routes-to",
		"source":{"id":"a","urn":"hrn:node:acme.com:kb:a","loc":"a","name":"A","deletedAt":null},
		"target":{"id":"b","urn":"hrn:node:acme.com:kb:b","loc":"b","name":"B","deletedAt":"2026-10-01T00:00:00Z"}}]}}}`
	srv, calls := recoveryServer(t, func(c recCall) string { return edges })
	out, _, _, err := recRun(t, srv, "node", "recovery", "graph", "-m", recMemory, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := recOnly(t, calls(), "RecoveryEdges")[0].vars; v["limit"] != float64(20) {
		t.Errorf("limit: %v", v)
	}
	for _, want := range []string{`"routes-to"`, `"deletedAt": null`, `"total": 1`} {
		if !strings.Contains(out, want) {
			t.Errorf("json missing %s: %s", want, out)
		}
	}
	text, _, _, _ := recRun(t, srv, "node", "recovery", "graph", "-m", recMemory)
	if !strings.Contains(text, "b (deleted)") || strings.Contains(text, "a (deleted)") {
		t.Errorf("table must mark only the deleted endpoint: %q", text)
	}
}

const recNodeJSON = `{"id":"n1","urn":"` + recNodeURN + `","memoryId":"m1","loc":"findings:flaky-ci","name":"Flaky CI","nodeType":"finding","description":"d","deletedAt":"2026-10-01T00:00:00Z"}`

func TestRecoveryGetSendsRefDirectlyWithNoResolve(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string {
		return `{"data":{"recoveryNode":` + recNodeJSON + `}}`
	})
	for _, args := range [][]string{
		{"node", "recovery", "get", recNodeURN},
		{"node", "recovery", "get", "findings:flaky-ci", "-m", recMemory},
		{"node", "recovery", "get", "01a165f9681b7c069430c642d5e7ba5d"},
	} {
		if _, _, _, err := recRun(t, srv, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, c := range calls() {
		if c.op != "RecoveryNode" {
			t.Errorf("only RecoveryNode may be sent (an ordinary resolve omits tombstones), saw %s", c.op)
		}
	}
	if got := calls()[0].vars["nodeRef"]; got != recNodeURN {
		t.Errorf("nodeRef = %v", got)
	}
	if got := calls()[1].vars["nodeRef"]; got != recNodeURN {
		t.Errorf("a bare loc with -m must compose the flat URN, got %v", got)
	}
}

func TestRecoveryGetDefaultNeverSelectsContent(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string {
		return `{"data":{"recoveryNode":` + recNodeJSON + `}}`
	})
	out, _, _, err := recRun(t, srv, "node", "recovery", "get", recNodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls()[0].raw, "content") {
		t.Errorf("the default point read must not select content: %s", calls()[0].raw)
	}
	if !strings.Contains(out, `"contentIncluded": false`) || !strings.Contains(out, `"content": null`) {
		t.Errorf("not-requested must be distinguishable from empty: %s", out)
	}
}

func TestRecoveryGetContentIsASeparateOperationAndRawBytes(t *testing.T) {
	const body = "{{=<% %>=}}\nliteral {{x}} body, no trailing newline"
	bodyJSON, _ := json.Marshal(body)
	srv, calls := recoveryServer(t, func(c recCall) string {
		if c.op == "RecoveryNodeContent" {
			return `{"data":{"recoveryNode":{"id":"n1","content":` + string(bodyJSON) + `}}}`
		}
		return `{"data":{"recoveryNode":` + recNodeJSON + `}}`
	})
	out, _, _, err := recRun(t, srv, "node", "recovery", "get", recNodeURN, "--content")
	if err != nil {
		t.Fatal(err)
	}
	if out != body {
		t.Errorf("text --content must be the body byte for byte, with no header\n got %q\nwant %q", out, body)
	}
	if n := len(recOnly(t, calls(), "RecoveryNodeContent")); n != 1 {
		t.Errorf("content is its own operation, got %d", n)
	}
	// The body is read by the stable id of the first read, never by the mutable
	// ref, which could re-resolve to another tombstone.
	if got := recOnly(t, calls(), "RecoveryNodeContent")[0].vars["nodeRef"]; got != "n1" {
		t.Errorf("content must be read by the node id from the first read, got %v", got)
	}
	jsonOut, _, _, _ := recRun(t, srv, "node", "recovery", "get", recNodeURN, "--content", "--json")
	var dto struct {
		Content         *string `json:"content"`
		ContentIncluded bool    `json:"contentIncluded"`
	}
	_ = json.Unmarshal([]byte(jsonOut), &dto)
	if dto.Content == nil || *dto.Content != body || !dto.ContentIncluded {
		t.Errorf("json content: %+v", dto)
	}
}

func TestRecoveryGetUnreadableContentIsAnErrorNotAnEmptyBody(t *testing.T) {
	srv, _ := recoveryServer(t, func(c recCall) string {
		if c.op == "RecoveryNodeContent" {
			return `{"errors":[{"message":"memory is locked","extensions":{"code":"MEMORY_LOCKED"}}]}`
		}
		return `{"data":{"recoveryNode":` + recNodeJSON + `}}`
	})
	out, _, code, err := recRun(t, srv, "node", "recovery", "get", recNodeURN, "--content")
	if err == nil || code == 0 {
		t.Fatalf("an unreadable body must fail, code %d", code)
	}
	if out != "" {
		t.Errorf("nothing may be written for a failed body read: %q", out)
	}
}

func TestRecoveryGetMissIsOneFixedSentence(t *testing.T) {
	srv, _ := recoveryServer(t, func(c recCall) string { return `{"data":{"recoveryNode":null}}` })
	_, errOut, code, err := recRun(t, srv, "node", "recovery", "get", recNodeURN)
	if err == nil || code != exitcode.NotFound {
		t.Fatalf("want exit 4, got %d", code)
	}
	want := "no recoverable deleted node at that reference; it may be live or absent, or this account may not recover it"
	if !strings.Contains(errOut, want) {
		t.Errorf("stderr must carry the fixed sentence: %q", errOut)
	}
}

func TestRecoveryGetBareLocWithoutMemoryIsUsage(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return "" })
	if _, _, code, _ := recRun(t, srv, "node", "recovery", "get", "findings:flaky-ci"); code != exitcode.Usage {
		t.Errorf("want usage, got %d", code)
	}
	if len(calls()) != 0 {
		t.Errorf("a local refusal sends nothing")
	}
}

const recRestoredJSON = `{"id":"n1","urn":"` + recNodeURN + `","memoryId":"m1","loc":"findings:flaky-ci","name":"Flaky CI","nodeType":"finding","updatedAt":"2026-10-22T00:00:00Z","deletedAt":null}`

func TestRestoreSendsOneRequestAndSelectsNoBody(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string {
		return `{"data":{"restoreNode":` + recRestoredJSON + `}}`
	})
	out, _, _, err := recRun(t, srv, "node", "restore", recNodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 || got[0].op != "RestoreNode" {
		t.Fatalf("exactly one RestoreNode, got %+v", got)
	}
	for _, field := range []string{"content", "description", "outgoingEdges", "incomingEdges"} {
		if strings.Contains(got[0].raw, field) {
			t.Errorf("restore must not select %q (no template or include expansion may run): %s", field, got[0].raw)
		}
	}
	if strings.Contains(out, "changed") || strings.Contains(out, "idempotent") {
		t.Errorf("the receipt must not invent a changed/idempotent flag: %s", out)
	}
	if !strings.Contains(out, `"urn": "`+recNodeURN+`"`) {
		t.Errorf("receipt: %s", out)
	}
	text, _, _, _ := recRun(t, srv, "node", "restore", "findings:flaky-ci", "-m", recMemory)
	if strings.TrimSpace(text) != "Node "+recNodeURN+" is live." {
		t.Errorf("text receipt: %q", text)
	}
}

func TestRestoreRefusalsKeepServerMessageAndExitCode(t *testing.T) {
	cases := []struct {
		name, code, msg string
		want            int
	}{
		{"parent deleted", "NODE_PARENT_DELETED", "Restore the deleted ancestor hrn:node:acme.com:kb:findings first.", exitcode.Conflict},
		{"write conflict", "NODE_WRITE_CONFLICT", "node changed", exitcode.Conflict},
		{"governed", "ROLE_GOVERNED", "governed node", exitcode.Usage},
		{"protected", "LOC_PROTECTED", "reserved address", exitcode.Usage},
		{"boot prompt", "BOOT_PROMPT_COMPILED_WRITE", "compiled boot prompt", exitcode.Usage},
		{"role writer level", "ROLE_WRITER_LEVEL_REQUIRED", "writer level required", exitcode.Forbidden},
		{"role writers unsupported", "ROLE_WRITERS_UNSUPPORTED", "role writers not built", exitcode.Forbidden},
		{"concealed miss", "NOT_FOUND", "node not found", exitcode.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := recoveryServer(t, func(c recCall) string {
				return fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":%q}}],"data":null}`, tc.msg, tc.code)
			})
			_, errOut, code, err := recRun(t, srv, "node", "restore", recNodeURN)
			if err == nil || code != tc.want {
				t.Fatalf("want exit %d, got %d (%v)", tc.want, code, err)
			}
			if !strings.Contains(errOut, tc.msg) {
				t.Errorf("the server's message must be kept: %q", errOut)
			}
			if n := len(calls()); n != 1 {
				t.Errorf("a refusal is never retried: %d requests", n)
			}
		})
	}
}

func TestRestoreCommentRefusalPointsAtTheCommentCommand(t *testing.T) {
	srv, _ := recoveryServer(t, func(c recCall) string {
		return `{"errors":[{"message":"comment","extensions":{"code":"ROLE_GOVERNED","kind":"comment"}}],"data":null}`
	})
	_, errOut, code, _ := recRun(t, srv, "node", "restore", recNodeURN)
	if code != exitcode.Usage || !strings.Contains(errOut, "hadron comment") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestRestoreLostResponseIsNotRetriedAndSaysToVerify(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName == "GetBoundSessionEnd" {
			_, _ = w.Write([]byte(`{"data":{"session":{"id":"s","endedAt":null,"endReason":null}}}`))
			return
		}
		n++
		// Drop the connection mid-response: the outcome is unknown.
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	_, errOut, code, err := recRun(t, srv, "node", "restore", recNodeURN)
	if err == nil || code != exitcode.Unavailable {
		t.Fatalf("want exit 7, got %d (%v)", code, err)
	}
	if n != 1 {
		t.Errorf("exactly one restore request, got %d", n)
	}
	if !strings.Contains(errOut, "node recovery get") {
		t.Errorf("must tell the caller to verify before retrying: %q", errOut)
	}
}

func TestRecoveryNeverLeaksBodyOrDescriptionInErrors(t *testing.T) {
	const sentinel = "PRIVATE-BODY-SENTINEL"
	srv, _ := recoveryServer(t, func(c recCall) string {
		if c.op == "RecoveryNodeContent" {
			return `{"errors":[{"message":"cannot decrypt"}],"data":null}`
		}
		return `{"data":{"recoveryNode":` + strings.Replace(recNodeJSON, `"description":`, `"description":"`+sentinel+`","x":`, 1) + `}}`
	})
	out, errOut, _, err := recRun(t, srv, "node", "recovery", "get", recNodeURN, "--content")
	if err == nil {
		t.Fatal("an unreadable body must be an error")
	}
	if strings.Contains(out+errOut+err.Error(), sentinel) {
		t.Errorf("sentinel leaked")
	}
}

// The live-only reads must stay live-only: nothing in `node get|ls|rm` may send
// a recovery operation.
func TestOrdinaryNodeCommandsNeverSendRecoveryOperations(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string {
		switch c.op {
		case "ResolveUrn":
			return resolveNodeJSON
		case "GetNode":
			return `{"data":{"node":` + nodeDetailJSON + `}}`
		case "DeleteNode":
			return `{"data":{"deleteNode":{"id":"n1"}}}`
		case "FindNodes":
			return `{"data":{"findNodes":{"total":0,"degraded":null,"reason":null,"hits":[]}}}`
		}
		// Only the operation NAMES matter here; anything else is an empty answer.
		return `{"data":null}`
	})
	_, _, _, _ = recRun(t, srv, "node", "get", recNodeURN)
	_, _, _, _ = recRun(t, srv, "node", "ls", "-m", recMemory)
	_, _, _, _ = recRun(t, srv, "node", "rm", recNodeURN, "--yes")
	for _, c := range calls() {
		if strings.Contains(c.op, "Recovery") || c.op == "RestoreNode" {
			t.Errorf("an ordinary node command sent %s", c.op)
		}
	}
}

// The "one request, no replay" guarantee has more than one path to it: besides a
// refusal and a lost connection, a proxy 502 leaves the outcome just as unknown,
// and a receipt that cannot be written must not read as a failed restore.
func TestRestoreProxyBadGatewayIsUnknownOutcomeAndSentOnce(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName == "GetBoundSessionEnd" {
			_, _ = w.Write([]byte(`{"data":{"session":{"id":"s","endedAt":null,"endReason":null}}}`))
			return
		}
		n++
		http.Error(w, "<html>bad gateway</html>", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	_, errOut, code, err := recRun(t, srv, "node", "restore", recNodeURN)
	if err == nil || code != exitcode.Unavailable || n != 1 {
		t.Fatalf("code %d, requests %d, err %v", code, n, err)
	}
	if !strings.Contains(errOut, "node recovery get") {
		t.Errorf("must say to verify: %q", errOut)
	}
}

func TestRestoreReceiptWriteFailureSaysItMayHaveSucceeded(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string {
		return `{"data":{"restoreNode":` + recRestoredJSON + `}}`
	})
	f, _ := testFactory(t)
	f.IOStreams.Out = failingWriter{}
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "restore", recNodeURN, "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a receipt that cannot be written must fail the command")
	}
	if !strings.Contains(err.Error(), "may have succeeded") || !strings.Contains(err.Error(), "do not blindly retry") {
		t.Errorf("message: %v", err)
	}
	if n := len(recOnly(t, calls(), "RestoreNode")); n != 1 {
		t.Errorf("one request, got %d", n)
	}
}

func TestRecoveryExplicitlyEmptyMemoryIsRefusedNotTreatedAsAbsent(t *testing.T) {
	srv, calls := recoveryServer(t, func(c recCall) string { return "" })
	for _, args := range [][]string{
		{"node", "recovery", "ls", "-m", ""},
		{"node", "recovery", "graph", "--memory="},
		{"node", "recovery", "get", "findings:flaky-ci", "-m", ""},
		{"node", "restore", "findings:flaky-ci", "--memory="},
	} {
		if _, _, code, _ := recRun(t, srv, args...); code != exitcode.Usage {
			t.Errorf("%v: want usage, got %d", args, code)
		}
	}
	if len(calls()) != 0 {
		t.Errorf("refusals are local, %d requests sent", len(calls()))
	}
}
