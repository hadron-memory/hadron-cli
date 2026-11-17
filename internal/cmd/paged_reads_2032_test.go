package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// cli#2032: web-poll list and node signing-request list adopt the paged reads
// (server#4123 / #4193). The fake below plays the server's page contract:
// default 20, clamp 1-100, an opaque `after` cursor the CLI must pass back
// verbatim, and an exact hasMore/nextCursor. Its filter and clamp are the
// FIXTURE's: these tests prove what the CLI sends and shows, not the served
// schema (the QA seat's disposable-stack leg covers that).

type pagedCall2032 struct {
	op   string
	vars map[string]any
}

type pagedServer2032 struct {
	mu    sync.Mutex
	calls []pagedCall2032
	// rows are the full lists the fake pages over, by operation.
	polls    []string
	requests []string // every signing request of the node, any effective state
	reply    func(op string, vars map[string]any) (string, bool)
}

func cursor2032(n int) string { return "opaque-" + strconv.Itoa(n) }

func start2032(after any) int {
	s, ok := after.(string)
	if !ok || s == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(s, "opaque-"))
	return n
}

func pageOf2032(rows []string, vars map[string]any) (items []string, hasMore bool, next string) {
	first := 20
	if f, ok := vars["first"].(float64); ok {
		first = int(f)
	}
	if first > 100 {
		first = 100
	}
	from := start2032(vars["after"])
	if from > len(rows) {
		from = len(rows)
	}
	to := from + first
	if to > len(rows) {
		to = len(rows)
	}
	return rows[from:to], to < len(rows), cursor2032(to)
}

func envelope2032(items []string, hasMore bool, next string) string {
	nc := "null"
	if hasMore {
		nc = `"` + next + `"`
	}
	return `{"items":[` + strings.Join(items, ",") + `],"hasMore":` + strconv.FormatBool(hasMore) + `,"nextCursor":` + nc + `}`
}

func (p *pagedServer2032) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		p.calls = append(p.calls, pagedCall2032{op: body.OperationName + "|" + body.Query, vars: body.Variables})
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if p.reply != nil {
			if resp, ok := p.reply(body.OperationName, body.Variables); ok {
				_, _ = w.Write([]byte(resp))
				return
			}
		}
		switch body.OperationName {
		case "ResolveUrn":
			_, _ = w.Write([]byte(resolveNodeJSON))
		case "WebPollsPage":
			items, more, next := pageOf2032(p.polls, body.Variables)
			_, _ = w.Write([]byte(`{"data":{"webPollsPage":` + envelope2032(items, more, next) + `}}`))
		case "NodeSigningRequests":
			rows := p.requests
			if st, ok := body.Variables["state"].(string); ok {
				var keep []string
				for _, row := range rows {
					if strings.Contains(row, `"state":"`+st+`"`) {
						keep = append(keep, row)
					}
				}
				rows = keep
			}
			items, more, next := pageOf2032(rows, body.Variables)
			_, _ = w.Write([]byte(`{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","signingRequestsPage":` + envelope2032(items, more, next) + `}}}`))
		default:
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation ` + body.OperationName + `"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (p *pagedServer2032) listCalls(prefix string) []pagedCall2032 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []pagedCall2032
	for _, c := range p.calls {
		if strings.HasPrefix(c.op, prefix+"|") {
			out = append(out, c)
		}
	}
	return out
}

func run2032(t *testing.T, srv *httptest.Server, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, args...), "--server", srv.URL))
	err = root.Execute()
	if err != nil {
		renderError(f, err)
	}
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func poll2032(i int) string {
	return strings.Replace(webPollJSON, `"jobId":"job_1"`, fmt.Sprintf(`"jobId":"w%03d"`, i), 1)
}

func polls2032(n int) []string {
	rows := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, poll2032(i))
	}
	return rows
}

func ids2032(t *testing.T, out string, key string) []string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, out)
	}
	var items []map[string]any
	if err := json.Unmarshal(doc[key], &items); err != nil {
		t.Fatalf("%s is not an array: %v\n%s", key, err, out)
	}
	var ids []string
	for _, it := range items {
		for _, k := range []string{"jobId", "id"} {
			if v, ok := it[k].(string); ok {
				ids = append(ids, v)
			}
		}
	}
	return ids
}

func want2032(prefix string, from, to int) []string {
	var ids []string
	for i := from; i <= to; i++ {
		ids = append(ids, fmt.Sprintf("%s%03d", prefix, i))
	}
	return ids
}

func eq2032(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// --- web-poll list ----------------------------------------------------------

// A 45-watch fixture, three explicit invocations: default 20 + cursor, --after
// continues, the last page ends with hasMore false and a null cursor. One query
// each, the cursor forwarded verbatim, never decoded or built.
func TestWebPollListPagesByExplicitCursor2032(t *testing.T) {
	p := &pagedServer2032{polls: polls2032(45)}
	srv := p.serve(t)
	type step struct {
		args    []string
		ids     []string
		more    bool
		next    string
		wantVar any
	}
	for i, s := range []step{
		{[]string{"web-poll", "list", "--org", "acme.com", "--json"}, want2032("w", 1, 20), true, cursor2032(20), nil},
		{[]string{"web-poll", "list", "--org", "acme.com", "--after", cursor2032(20), "--json"}, want2032("w", 21, 40), true, cursor2032(40), cursor2032(20)},
		{[]string{"web-poll", "list", "--org", "acme.com", "--after", cursor2032(40), "--json"}, want2032("w", 41, 45), false, "", cursor2032(40)},
	} {
		out, _, err := run2032(t, srv, s.args...)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if got := ids2032(t, out, "items"); !eq2032(got, s.ids) {
			t.Fatalf("step %d: ids %v want %v", i, got, s.ids)
		}
		var doc struct {
			HasMore    bool    `json:"hasMore"`
			NextCursor *string `json:"nextCursor"`
		}
		_ = json.Unmarshal([]byte(out), &doc)
		if doc.HasMore != s.more || (s.more && (doc.NextCursor == nil || *doc.NextCursor != s.next)) || (!s.more && doc.NextCursor != nil) {
			t.Fatalf("step %d: hasMore %v cursor %v\n%s", i, doc.HasMore, doc.NextCursor, out)
		}
		calls := p.listCalls("WebPollsPage")
		if len(calls) != i+1 {
			t.Fatalf("step %d: %d WebPollsPage calls, want one per invocation", i, len(calls))
		}
		last := calls[i].vars
		if last["orgRef"] != "acme.com" || last["after"] != s.wantVar {
			t.Fatalf("step %d: variables %v", i, last)
		}
	}
	if strings.Contains(strings.Join(callOps2032(p), ","), "WebPolls|") {
		t.Fatal("the deprecated unpaged webPolls read was used")
	}
}

func callOps2032(p *pagedServer2032) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ops []string
	for _, c := range p.calls {
		ops = append(ops, strings.SplitN(c.op, "|", 2)[0]+"|")
	}
	return ops
}

// The query document selects the page field and not the deprecated array.
func TestWebPollListQueriesThePageField2032(t *testing.T) {
	p := &pagedServer2032{polls: polls2032(1)}
	srv := p.serve(t)
	if _, _, err := run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--json"); err != nil {
		t.Fatal(err)
	}
	calls := p.listCalls("WebPollsPage")
	if len(calls) != 1 || !strings.Contains(calls[0].op, "webPollsPage(") || strings.Contains(calls[0].op, " webPolls(") || strings.Contains(calls[0].op, "total") {
		t.Fatalf("calls %v", calls)
	}
}

// --limit: positive values through int32 max are forwarded and the server
// clamps; 101 is never a client refusal. 0, negatives, non-integers and
// overflow are refused locally (exit 2) before any request.
func TestWebPollListLimit2032(t *testing.T) {
	p := &pagedServer2032{polls: polls2032(150)}
	srv := p.serve(t)
	for _, tc := range []struct {
		limit string
		want  int
	}{{"1", 1}, {"20", 20}, {"100", 100}, {"101", 100}} {
		out, _, err := run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--limit", tc.limit, "--json")
		if err != nil {
			t.Fatalf("--limit %s: %v", tc.limit, err)
		}
		if got := len(ids2032(t, out, "items")); got != tc.want {
			t.Fatalf("--limit %s: %d rows, want %d", tc.limit, got, tc.want)
		}
		calls := p.listCalls("WebPollsPage")
		sent, _ := strconv.Atoi(tc.limit)
		if v, _ := calls[len(calls)-1].vars["first"].(float64); int(v) != sent {
			t.Fatalf("--limit %s forwarded as %v", tc.limit, calls[len(calls)-1].vars["first"])
		}
	}
	before := len(p.listCalls("WebPollsPage"))
	for _, bad := range []string{"0", "-1", "abc", "2147483648", "1.5"} {
		_, _, err := run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--limit", bad)
		if exitCodeFor(err) != 2 {
			t.Fatalf("--limit %s: exit %d, want 2 (%v)", bad, exitCodeFor(err), err)
		}
	}
	if _, _, err := run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--after", ""); exitCodeFor(err) != 2 {
		t.Fatalf("an empty --after must be a usage error, got %v", err)
	}
	if len(p.listCalls("WebPollsPage")) != before {
		t.Fatal("a refused flag still sent a request")
	}
}

// Exact JSON shape: the envelope keys, items as the existing watch DTO, empty
// arrays present, the cursor null (present) on the last page.
func TestWebPollListJSONEnvelopeShape2032(t *testing.T) {
	p := &pagedServer2032{polls: nil}
	srv := p.serve(t)
	out, _, err := run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "hasMore,items,nextCursor" || string(doc["items"]) != "[]" || string(doc["hasMore"]) != "false" || string(doc["nextCursor"]) != "null" {
		t.Fatalf("envelope = %s", out)
	}
	p2 := &pagedServer2032{polls: polls2032(1)}
	out, _, err = run2032(t, p2.serve(t), "web-poll", "list", "--org", "acme.com", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"jobId": "w001"`) || !strings.Contains(out, `"lastTriggeredAt": null`) {
		t.Fatalf("the watch DTO changed: %s", out)
	}
}

// Human output: the table plus `has more`, and `next cursor` only when there is
// one. An empty page says it is empty, not that no watches exist.
func TestWebPollListHumanPageLines2032(t *testing.T) {
	p := &pagedServer2032{polls: polls2032(25)}
	srv := p.serve(t)
	out, _, err := run2032(t, srv, "web-poll", "list", "--org", "acme.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "w020") || strings.Contains(out, "w021") || !strings.Contains(out, "has more: true") || !strings.Contains(out, "next cursor: "+cursor2032(20)) {
		t.Fatalf("first page:\n%s", out)
	}
	out, _, err = run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--after", cursor2032(20))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "has more: false") || strings.Contains(out, "next cursor") {
		t.Fatalf("last page:\n%s", out)
	}
	out, _, err = run2032(t, srv, "web-poll", "list", "--org", "acme.com", "--after", cursor2032(25))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no watches on this page") || !strings.Contains(out, "has more: false") || strings.Contains(out, "no watches exist") {
		t.Fatalf("past-end page:\n%s", out)
	}
}

// A page whose metadata cannot be trusted is an error with no partial output,
// never an empty success: a missing page, hasMore with no usable cursor, and a
// cursor that did not advance.
func TestWebPollListRefusesUntrustworthyPageMetadata2032(t *testing.T) {
	row := poll2032(1)
	for name, reply := range map[string]string{
		"nil page":            `{"data":{"webPollsPage":null}}`,
		"hasMore no cursor":   `{"data":{"webPollsPage":{"items":[` + row + `],"hasMore":true,"nextCursor":null}}}`,
		"hasMore empty":       `{"data":{"webPollsPage":{"items":[` + row + `],"hasMore":true,"nextCursor":""}}}`,
		"cursor not advanced": `{"data":{"webPollsPage":{"items":[` + row + `],"hasMore":true,"nextCursor":"opaque-7"}}}`,
	} {
		reply := reply
		p := &pagedServer2032{reply: func(op string, _ map[string]any) (string, bool) { return reply, op == "WebPollsPage" }}
		out, _, err := run2032(t, p.serve(t), "web-poll", "list", "--org", "acme.com", "--after", "opaque-7", "--json")
		if exitCodeFor(err) != 1 || strings.TrimSpace(out) != "" {
			t.Fatalf("%s: exit %d stdout %q err %v", name, exitCodeFor(err), out, err)
		}
	}
}

// A server that lacks the page fields is a failure, never an empty list, and
// the deprecated array is not the fallback.
func TestWebPollListOlderServerIsAnErrorNotAnEmptyList2032(t *testing.T) {
	p := &pagedServer2032{reply: func(op string, _ map[string]any) (string, bool) {
		if op == "WebPollsPage" {
			return `{"errors":[{"message":"Cannot query field \"webPollsPage\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, true
		}
		return "", false
	}}
	out, _, err := run2032(t, p.serve(t), "web-poll", "list", "--org", "acme.com", "--json")
	if err == nil || strings.Contains(out, `"items"`) {
		t.Fatalf("out %q err %v", out, err)
	}
	if strings.Contains(strings.Join(callOps2032(p), ","), "WebPolls|") {
		t.Fatal("fell back to the deprecated read")
	}
}

// --- node signing-request list ---------------------------------------------

func signingRow2032(id, state string) string {
	r := strings.Replace(signingRequestJSON, `"id":"sr1"`, `"id":"`+id+`"`, 1)
	return strings.Replace(r, `"state":"OPEN"`, `"state":"`+state+`"`, 1)
}

// 30 OPEN requests interleaved with EXPIRED ones. The filter runs server side
// (the fake plays it), the CLI forwards state and the cursor and walks nothing.
func signingFixture2032() []string {
	var rows []string
	for i := 1; i <= 30; i++ {
		rows = append(rows, signingRow2032(fmt.Sprintf("ro%03d", i), "OPEN"), signingRow2032(fmt.Sprintf("rx%03d", i), "EXPIRED"))
	}
	return rows
}

func TestSigningRequestListPagesByExplicitCursor2032(t *testing.T) {
	p := &pagedServer2032{requests: signingFixture2032()}
	srv := p.serve(t)
	type step struct {
		extra []string
		ids   []string
		more  bool
		next  string
		after any
	}
	for i, s := range []step{
		{nil, want2032("ro", 1, 20), true, cursor2032(20), nil},
		{[]string{"--after", cursor2032(20)}, want2032("ro", 21, 30), false, "", cursor2032(20)},
	} {
		args := append([]string{"node", "signing-request", "list", nodeURN, "--state", "open", "--json"}, s.extra...)
		out, _, err := run2032(t, srv, args...)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if got := ids2032(t, out, "requests"); !eq2032(got, s.ids) {
			t.Fatalf("step %d: ids %v want %v", i, got, s.ids)
		}
		var doc struct {
			NodeID     string  `json:"nodeId"`
			URN        string  `json:"urn"`
			Loc        string  `json:"loc"`
			HasMore    bool    `json:"hasMore"`
			NextCursor *string `json:"nextCursor"`
		}
		_ = json.Unmarshal([]byte(out), &doc)
		if doc.NodeID != "n1" || doc.Loc != "findings:flaky-ci" || doc.HasMore != s.more || (s.more && (doc.NextCursor == nil || *doc.NextCursor != s.next)) || (!s.more && doc.NextCursor != nil) {
			t.Fatalf("step %d: %s", i, out)
		}
		calls := p.listCalls("NodeSigningRequests")
		if len(calls) != i+1 || calls[i].vars["state"] != "OPEN" || calls[i].vars["after"] != s.after || calls[i].vars["ref"] != "n1" {
			t.Fatalf("step %d: calls %v", i, calls)
		}
	}
	// state=expired reads the effective-expired rows on their own pages.
	out, _, err := run2032(t, srv, "node", "signing-request", "list", nodeURN, "--state", "expired", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids2032(t, out, "requests"); !eq2032(got, want2032("rx", 1, 20)) {
		t.Fatalf("expired page: %v", got)
	}
}

func TestSigningRequestListQueriesThePageField2032(t *testing.T) {
	p := &pagedServer2032{requests: signingFixture2032()}
	if _, _, err := run2032(t, p.serve(t), "node", "signing-request", "list", nodeURN, "--json"); err != nil {
		t.Fatal(err)
	}
	calls := p.listCalls("NodeSigningRequests")
	if len(calls) != 1 || !strings.Contains(calls[0].op, "signingRequestsPage(") || strings.Contains(calls[0].op, " signingRequests(") || strings.Contains(calls[0].op, "total") {
		t.Fatalf("calls %v", calls)
	}
}

// --limit: the CLI's old >100 refusal is gone (the server clamps); <1,
// non-integers and overflow stay local usage errors.
func TestSigningRequestListLimit2032(t *testing.T) {
	var rows []string
	for i := 1; i <= 150; i++ {
		rows = append(rows, signingRow2032(fmt.Sprintf("ro%03d", i), "OPEN"))
	}
	p := &pagedServer2032{requests: rows}
	srv := p.serve(t)
	for _, tc := range []struct {
		limit string
		want  int
	}{{"1", 1}, {"20", 20}, {"100", 100}, {"101", 100}} {
		out, _, err := run2032(t, srv, "node", "signing-request", "list", nodeURN, "--limit", tc.limit, "--json")
		if err != nil {
			t.Fatalf("--limit %s: %v", tc.limit, err)
		}
		if got := len(ids2032(t, out, "requests")); got != tc.want {
			t.Fatalf("--limit %s: %d rows, want %d", tc.limit, got, tc.want)
		}
		calls := p.listCalls("NodeSigningRequests")
		sent, _ := strconv.Atoi(tc.limit)
		if v, _ := calls[len(calls)-1].vars["first"].(float64); int(v) != sent {
			t.Fatalf("--limit %s forwarded as %v", tc.limit, calls[len(calls)-1].vars["first"])
		}
	}
	before := len(p.listCalls("NodeSigningRequests"))
	for _, bad := range []string{"0", "-1", "abc", "2147483648"} {
		if _, _, err := run2032(t, srv, "node", "signing-request", "list", nodeURN, "--limit", bad); exitCodeFor(err) != 2 {
			t.Fatalf("--limit %s: exit %d (%v)", bad, exitCodeFor(err), err)
		}
	}
	if _, _, err := run2032(t, srv, "node", "signing-request", "list", nodeURN, "--after", ""); exitCodeFor(err) != 2 {
		t.Fatalf("an empty --after must be a usage error, got %v", err)
	}
	if len(p.listCalls("NodeSigningRequests")) != before {
		t.Fatal("a refused flag still sent a request")
	}
}

// Exact JSON keys: the existing four plus hasMore and nextCursor; the empty
// list is [] and the last cursor is null, both present.
func TestSigningRequestListJSONShape2032(t *testing.T) {
	p := &pagedServer2032{}
	out, _, err := run2032(t, p.serve(t), "node", "signing-request", "list", nodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "hasMore,loc,nextCursor,nodeId,requests,urn" || string(doc["requests"]) != "[]" || string(doc["nextCursor"]) != "null" || string(doc["hasMore"]) != "false" {
		t.Fatalf("shape = %s", out)
	}
}

func TestSigningRequestListHumanPageLines2032(t *testing.T) {
	p := &pagedServer2032{requests: signingFixture2032()}
	srv := p.serve(t)
	out, _, err := run2032(t, srv, "node", "signing-request", "list", nodeURN, "--state", "open")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ro020") || strings.Contains(out, "ro021") || !strings.Contains(out, "has more: true") || !strings.Contains(out, "next cursor: "+cursor2032(20)) {
		t.Fatalf("first page:\n%s", out)
	}
	out, _, err = run2032(t, srv, "node", "signing-request", "list", nodeURN, "--state", "open", "--after", cursor2032(20))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "has more: false") || strings.Contains(out, "next cursor") {
		t.Fatalf("last page:\n%s", out)
	}
	out, _, err = run2032(t, srv, "node", "signing-request", "list", nodeURN, "--state", "open", "--after", cursor2032(30))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no signing requests on this page") || !strings.Contains(out, "has more: false") {
		t.Fatalf("past-end page:\n%s", out)
	}
}

func TestSigningRequestListRefusesUntrustworthyPageMetadata2032(t *testing.T) {
	node := func(page string) string {
		return `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","signingRequestsPage":` + page + `}}}`
	}
	row := signingRow2032("ro001", "OPEN")
	for name, reply := range map[string]string{
		"nil page":            node(`null`),
		"hasMore no cursor":   node(`{"items":[` + row + `],"hasMore":true,"nextCursor":null}`),
		"hasMore empty":       node(`{"items":[` + row + `],"hasMore":true,"nextCursor":""}`),
		"cursor not advanced": node(`{"items":[` + row + `],"hasMore":true,"nextCursor":"opaque-7"}`),
	} {
		reply := reply
		p := &pagedServer2032{reply: func(op string, _ map[string]any) (string, bool) { return reply, op == "NodeSigningRequests" }}
		out, _, err := run2032(t, p.serve(t), "node", "signing-request", "list", nodeURN, "--after", "opaque-7", "--json")
		if exitCodeFor(err) != 1 || strings.TrimSpace(out) != "" {
			t.Fatalf("%s: exit %d stdout %q err %v", name, exitCodeFor(err), out, err)
		}
	}
}
