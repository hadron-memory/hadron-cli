package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// hadron-cli#198 Slice A: the read cut of `hadron email` — messages, folders and
// the paged connection list. Fake HTTP only; no mailbox, no provider, no spend.
// Message text (subject, from, preview, body) is external data: these tests plant
// control sequences (an OSC 52 clipboard write, an OSC 8 hyperlink, a screen
// clear, a forged line) in it and check none reaches the terminal raw. What is
// asserted is the command's output; no log or trace stream is captured here.

const msgRow198 = `{"id":"m1","subject":"hello","from":"a@example.test","receivedDateTime":"2026-11-20T10:00:00Z","bodyPreview":"prev","body":"full body","hasAttachments":true,"isRead":false}`

// requests198 counts the GraphQL POSTs the last runEmail198 made: ops is keyed by
// operation NAME, so two requests of one operation would pass a len(ops) check.
var requests198 atomic.Int64

func runEmail198(t *testing.T, responses map[string]string, args ...string) (out string, ops map[string]json.RawMessage, err error) {
	t.Helper()
	requests198.Store(0)
	srv, ops := captureGraphQL(t, responses)
	counter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requests198.Add(1)
		}
		proxyTo198(t, w, r, srv.URL)
	}))
	t.Cleanup(counter.Close)
	f, o := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"email"}, args...), "--server", counter.URL))
	err = root.Execute()
	return o.String(), ops, err
}

func vars198(t *testing.T, ops map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	raw, ok := ops[op]
	if !ok {
		t.Fatalf("%s was not called (ops: %v)", op, ops)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// One request, exactly the arguments given: top and unreadOnly are OMITTED when
// unset (the server defaults), the deprecated raw filter is never sent, and --top
// is passed through unchecked — the server clamps (cor:api:240:01).
func TestEmailMessagesWire198(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantTop    any
		hasTop     bool
		wantUnread any
		hasUnread  bool
	}{
		{"bare", []string{"--connection", "conn-1"}, nil, false, nil, false},
		{"top", []string{"--connection", "conn-1", "--top", "5"}, float64(5), true, nil, false},
		{"unread", []string{"--connection", "conn-1", "--unread"}, nil, false, true, true},
		{"top beyond the server clamp is not refused", []string{"--connection", "conn-1", "--top", "500"}, float64(500), true, nil, false},
		{"top zero is a value", []string{"--connection", "conn-1", "--top", "0"}, float64(0), true, nil, false},
		{"negative top is the server's to judge", []string{"--connection", "conn-1", "--top", "-3"}, float64(-3), true, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ops, err := runEmail198(t, map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[]}}`}, append([]string{"messages", "--json"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if requests198.Load() != 1 || len(ops) != 1 {
				t.Fatalf("want exactly one request, got %d (%v)", requests198.Load(), ops)
			}
			v := vars198(t, ops, "EmailMessages")
			if v["connectionId"] != "conn-1" {
				t.Errorf("connectionId = %v", v["connectionId"])
			}
			if _, ok := v["filter"]; ok {
				t.Error("the deprecated raw filter must never be sent")
			}
			if got, ok := v["top"]; ok != tc.hasTop || (ok && got != tc.wantTop) {
				t.Errorf("top = %v (sent=%v), want %v (sent=%v)", got, ok, tc.wantTop, tc.hasTop)
			}
			if got, ok := v["unreadOnly"]; ok != tc.hasUnread || (ok && got != tc.wantUnread) {
				t.Errorf("unreadOnly = %v (sent=%v), want %v (sent=%v)", got, ok, tc.wantUnread, tc.hasUnread)
			}
		})
	}
}

// The stable DTO: every ExchangeMessage field under its own name, nullable ones
// stay null, an empty list is [].
func TestEmailMessagesJSON198(t *testing.T) {
	out, _, err := runEmail198(t, map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[` + msgRow198 + `,{"id":"m2","subject":null,"from":null,"receivedDateTime":null,"bodyPreview":null,"body":null,"hasAttachments":null,"isRead":null}]}}`},
		"messages", "--connection", "c", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, k := range []string{"id", "subject", "from", "receivedDateTime", "bodyPreview", "body", "hasAttachments", "isRead"} {
		if _, ok := rows[0][k]; !ok {
			t.Errorf("row 0 lacks %q: %v", k, rows[0])
		}
		if _, ok := rows[1][k]; !ok {
			t.Errorf("a null %q must stay present as null: %v", k, rows[1])
		}
	}
	if rows[0]["isRead"] != false || rows[0]["hasAttachments"] != true || rows[1]["isRead"] != nil || rows[1]["subject"] != nil {
		t.Errorf("values or nulls changed: %v", rows)
	}
	empty, _, err := runEmail198(t, map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[]}}`}, "messages", "--connection", "c", "--json")
	if err != nil || strings.TrimSpace(empty) != "[]" {
		t.Errorf("empty list = %q, %v; want []", empty, err)
	}
}

// Message text is data: control and escape sequences are neutralised in text mode
// (an OSC 8 link, an OSC 52 clipboard write, a forged line), the body is not
// printed in the table at all, and the table never claims completeness.
func TestEmailMessagesTextTreatsMailAsData198(t *testing.T) {
	evil := `{"id":"m1","subject":"x\u001b]52;c;SECRET\u0007y\nforged: line","from":"\u001b[31mred@example.test","receivedDateTime":null,"bodyPreview":"p\u001b[2Jq","body":"BODY-MUST-NOT-PRINT","hasAttachments":false,"isRead":true}`
	out, _, err := runEmail198(t, map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[` + evil + `]}}`}, "messages", "--connection", "c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\a') {
		t.Errorf("a raw escape reached the terminal:\n%q", out)
	}
	if strings.Contains(out, "\nforged: line") {
		t.Errorf("an embedded newline forged a line:\n%s", out)
	}
	if strings.Contains(out, "BODY-MUST-NOT-PRINT") {
		t.Errorf("the text table printed the message body:\n%s", out)
	}
	if !strings.Contains(out, "m1") || !strings.Contains(out, "red@example.test") {
		t.Errorf("the row is not shown:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "complete") {
		t.Errorf("a bounded list must not claim completeness:\n%s", out)
	}
}

// Refusals pass through with the usual exit codes; the unknown-tool fallback is
// unchanged (no reconnect advice from provider prose); a null list is unavailable;
// and no error text carries mail.
func TestEmailMessagesAndFoldersErrors198(t *testing.T) {
	for _, op := range []struct{ cmd, op, field string }{{"messages", "EmailMessages", "exchangeMessages"}, {"folders", "EmailFolders", "exchangeFolders"}} {
		for _, tc := range []struct {
			name, wire string
			want       int
		}{
			{"owner refusal", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`, 8},
			{"unknown tool error keeps the fallback", `{"errors":[{"message":"reconnect your account now","extensions":{"code":"EMAIL_TOOL_ERROR","emailErrorCode":"unknown"}}]}`, 1},
			{"null list", `{"data":{"` + op.field + `":null}}`, 7},
			{"null row", `{"data":{"` + op.field + `":[null]}}`, 7},
		} {
			t.Run(op.cmd+" "+tc.name, func(t *testing.T) {
				out, _, err := runEmail198(t, map[string]string{op.op: tc.wire}, op.cmd, "--connection", "c", "--json")
				if exitCodeFor(err) != tc.want {
					t.Fatalf("exit %d (%v), want %d", exitCodeFor(err), err, tc.want)
				}
				if out != "" && strings.Contains(out, "id") && tc.want != 0 {
					t.Errorf("a failed read printed rows: %s", out)
				}
				if err != nil && strings.Contains(strings.ToLower(err.Error()), "run hadron connection") {
					t.Errorf("the error invents a reconnect remedy from provider prose: %v", err)
				}
			})
		}
	}
}

// A missing or blank --connection is a usage error with ZERO requests; so is a
// stray positional.
func TestEmailMessagesAndFoldersNeedAConnection198(t *testing.T) {
	for _, cmd := range []string{"messages", "folders"} {
		for _, args := range [][]string{{}, {"--connection", ""}, {"--connection", "  "}, {"--connection", "c", "extra"}} {
			_, ops, err := runEmail198(t, map[string]string{}, append([]string{cmd}, args...)...)
			if exitCodeFor(err) != 2 {
				t.Errorf("email %s %v: exit %d (%v), want 2", cmd, args, exitCodeFor(err), err)
			}
			if requests198.Load() != 0 || len(ops) != 0 {
				t.Errorf("email %s %v sent a request: %v", cmd, args, ops)
			}
		}
	}
}

func TestEmailFolders198(t *testing.T) {
	out, ops, err := runEmail198(t, map[string]string{"EmailFolders": `{"data":{"exchangeFolders":[{"id":"f1","displayName":"Inbox","totalItemCount":10,"unreadItemCount":2},{"id":"f2","displayName":"Odd\u001b[31m","totalItemCount":null,"unreadItemCount":null}]}}`},
		"folders", "--connection", " conn-1 ", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if requests198.Load() != 1 || len(ops) != 1 || vars198(t, ops, "EmailFolders")["connectionId"] != "conn-1" {
		t.Fatalf("ops = %v", ops)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(rows[0]) != 4 || rows[1]["totalItemCount"] != nil || rows[1]["unreadItemCount"] != nil || rows[0]["unreadItemCount"] != float64(2) {
		t.Errorf("rows = %v", rows)
	}
	text, _, err := runEmail198(t, map[string]string{"EmailFolders": `{"data":{"exchangeFolders":[{"id":"f2","displayName":"Odd\u001b[31m","totalItemCount":null,"unreadItemCount":null}]}}`}, "folders", "--connection", "c")
	if err != nil || strings.ContainsRune(text, '\x1b') {
		t.Errorf("text mode leaked an escape: %v\n%q", err, text)
	}
	empty, _, err := runEmail198(t, map[string]string{"EmailFolders": `{"data":{"exchangeFolders":[]}}`}, "folders", "--connection", "c", "--json")
	if err != nil || strings.TrimSpace(empty) != "[]" {
		t.Errorf("empty = %q, %v", empty, err)
	}
}

const connRow198 = `{"id":"c1","organizationId":"o","userId":"u","mailboxEmail":"m@example.test","provider":"gmail","displayName":null,"syncEnabled":true,"syncStatus":"IDLE","lastSyncAt":null,"createdAt":"c","updatedAt":"u"}`

// With --limit or --offset the paged operation is used ONCE; unset ones are
// omitted; the bare form stays on the unpaged list; the paged JSON is an object
// carrying total and hasMore, and the text names the next --offset, never
// "complete".
func TestEmailConnectionsPaging198(t *testing.T) {
	page := func(more bool) string {
		m := "false"
		if more {
			m = "true"
		}
		return `{"data":{"exchangeConnectionsPage":{"items":[` + connRow198 + `],"total":7,"hasMore":` + m + `}}}`
	}
	t.Run("bare form stays unpaged", func(t *testing.T) {
		_, ops, err := runEmail198(t, map[string]string{"EmailConnections": `{"data":{"exchangeConnections":[]}}`}, "connections", "--org", "o", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if _, paged := ops["EmailConnectionsPage"]; paged || requests198.Load() != 1 || len(ops) != 1 {
			t.Fatalf("the bare form must not use the paged operation: %v", ops)
		}
	})
	for _, tc := range []struct {
		name        string
		args        []string
		limit, offs any
		hasL, hasO  bool
	}{
		{"limit only", []string{"--limit", "3"}, float64(3), nil, true, false},
		{"offset only", []string{"--offset", "6"}, nil, float64(6), false, true},
		{"both", []string{"--limit", "3", "--offset", "0"}, float64(3), float64(0), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, ops, err := runEmail198(t, map[string]string{"EmailConnectionsPage": page(true)}, append([]string{"connections", "--org", "o", "--json"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if requests198.Load() != 1 || len(ops) != 1 {
				t.Fatalf("want one request, got %d (%v)", requests198.Load(), ops)
			}
			v := vars198(t, ops, "EmailConnectionsPage")
			if v["orgId"] != "o" {
				t.Errorf("orgId = %v", v["orgId"])
			}
			if got, ok := v["limit"]; ok != tc.hasL || (ok && got != tc.limit) {
				t.Errorf("limit = %v (sent=%v)", got, ok)
			}
			if got, ok := v["offset"]; ok != tc.hasO || (ok && got != tc.offs) {
				t.Errorf("offset = %v (sent=%v)", got, ok)
			}
			var page struct {
				Items   []map[string]any `json:"items"`
				Total   int              `json:"total"`
				HasMore bool             `json:"hasMore"`
			}
			if err := json.Unmarshal([]byte(out), &page); err != nil {
				t.Fatalf("paged --json is not the {items,total,hasMore} object: %v\n%s", err, out)
			}
			if len(page.Items) != 1 || page.Total != 7 || !page.HasMore || len(page.Items[0]) != 11 {
				t.Errorf("page = %+v", page)
			}
		})
	}
	t.Run("text names the next offset and never says complete", func(t *testing.T) {
		out, _, err := runEmail198(t, map[string]string{"EmailConnectionsPage": page(true)}, "connections", "--org", "o", "--limit", "1", "--offset", "2")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "total 7") || !strings.Contains(out, "--offset 3") || strings.Contains(strings.ToLower(out), "complete") {
			t.Errorf("paging footer wrong:\n%s", out)
		}
		last, _, err := runEmail198(t, map[string]string{"EmailConnectionsPage": page(false)}, "connections", "--org", "o", "--limit", "1", "--offset", "6")
		if err != nil || strings.Contains(last, "--offset") {
			t.Errorf("the last page must not suggest a next offset: %v\n%s", err, last)
		}
	})
	t.Run("a null page and a refusal", func(t *testing.T) {
		if _, _, err := runEmail198(t, map[string]string{"EmailConnectionsPage": `{"data":{"exchangeConnectionsPage":null}}`}, "connections", "--org", "o", "--limit", "1"); exitCodeFor(err) != 7 {
			t.Errorf("null page exit = %d (%v)", exitCodeFor(err), err)
		}
		if _, _, err := runEmail198(t, map[string]string{"EmailConnectionsPage": `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`}, "connections", "--org", "o", "--limit", "1"); exitCodeFor(err) != 8 {
			t.Errorf("admin refusal exit = %d (%v)", exitCodeFor(err), err)
		}
	})
}

// The documents select only what the DTOs carry: no credentials, no lastError,
// and messages never ask for the deprecated filter.
func TestEmailReadDocuments198(t *testing.T) {
	for _, tc := range []struct {
		name, doc, root string
		args            []string
		fields          []string
	}{
		{"EmailMessages", gen.EmailMessages_Operation, "exchangeMessages", []string{"connectionId", "top", "unreadOnly"}, []string{"id", "subject", "from", "receivedDateTime", "bodyPreview", "body", "hasAttachments", "isRead"}},
		{"EmailFolders", gen.EmailFolders_Operation, "exchangeFolders", []string{"connectionId"}, []string{"id", "displayName", "totalItemCount", "unreadItemCount"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.ParseQuery(&ast.Source{Input: tc.doc})
			if err != nil {
				t.Fatal(err)
			}
			op := doc.Operations.ForName(tc.name)
			f := op.SelectionSet[0].(*ast.Field)
			if len(op.SelectionSet) != 1 || f.Name != tc.root {
				t.Fatalf("root = %v", op.SelectionSet)
			}
			if len(f.Arguments) != len(tc.args) {
				t.Errorf("arguments = %v, want %v", f.Arguments, tc.args)
			}
			for _, a := range tc.args {
				if f.Arguments.ForName(a) == nil {
					t.Errorf("missing argument %s", a)
				}
			}
			if len(f.SelectionSet) != len(tc.fields) {
				t.Fatalf("selection = %d fields, want %v", len(f.SelectionSet), tc.fields)
			}
			for i, s := range f.SelectionSet {
				if s.(*ast.Field).Name != tc.fields[i] {
					t.Errorf("field %d = %s, want %s", i, s.(*ast.Field).Name, tc.fields[i])
				}
			}
		})
	}
}

// proxyTo198 forwards a request to the capturing fake and relays its answer.
func proxyTo198(t *testing.T, w http.ResponseWriter, r *http.Request, target string) {
	t.Helper()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		t.Errorf("test proxy: relaying the fake server's answer failed: %v", err)
	}
}

// F4 (Cedar): "one request" is counted in HTTP requests, not in distinct
// operation names.
func TestEmailReadsMakeOneHTTPRequest198(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp map[string]string
		args []string
	}{
		{"messages", map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[]}}`}, []string{"messages", "--connection", "c"}},
		{"folders", map[string]string{"EmailFolders": `{"data":{"exchangeFolders":[]}}`}, []string{"folders", "--connection", "c"}},
		{"paged connections", map[string]string{"EmailConnectionsPage": `{"data":{"exchangeConnectionsPage":{"items":[],"total":0,"hasMore":false}}}`}, []string{"connections", "--org", "o", "--limit", "5"}},
		{"bare connections", map[string]string{"EmailConnections": `{"data":{"exchangeConnections":[]}}`}, []string{"connections", "--org", "o"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := runEmail198(t, tc.resp, tc.args...); err != nil {
				t.Fatal(err)
			}
			if requests198.Load() != 1 {
				t.Errorf("made %d GraphQL requests, want exactly 1", requests198.Load())
			}
		})
	}
}

// F1 (Cedar, P2): GraphQL Int is signed 32-bit, so a value that does not fit
// fails coercion before the resolver could apply its clamp. The CLI refuses only
// what the wire type cannot carry, with zero requests; every value inside the
// range is the server's to judge (cor:api:240:01).
func TestEmailIntFlagsMustFitGraphQLInt198(t *testing.T) {
	const max32, min32 = "2147483647", "-2147483648"
	for _, tc := range []struct {
		flag, value string
		refuse      bool
	}{
		{"--top", max32, false}, {"--top", "2147483648", true}, {"--top", min32, false}, {"--top", "-2147483649", true},
		{"--limit", max32, false}, {"--limit", "2147483648", true}, {"--limit", min32, false}, {"--limit", "-2147483649", true},
		{"--offset", max32, false}, {"--offset", "2147483648", true}, {"--offset", min32, false}, {"--offset", "-2147483649", true},
	} {
		t.Run(tc.flag+" "+tc.value, func(t *testing.T) {
			args := []string{"messages", "--connection", "c", tc.flag, tc.value}
			resp := map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[]}}`}
			if tc.flag != "--top" {
				args = []string{"connections", "--org", "o", tc.flag, tc.value}
				resp = map[string]string{"EmailConnectionsPage": `{"data":{"exchangeConnectionsPage":{"items":[],"total":0,"hasMore":false}}}`}
			}
			_, ops, err := runEmail198(t, resp, args...)
			if tc.refuse {
				if exitCodeFor(err) != 2 || requests198.Load() != 0 {
					t.Errorf("exit %d (%v) with %d requests; want a usage error and none sent", exitCodeFor(err), err, requests198.Load())
				}
				return
			}
			if err != nil || requests198.Load() != 1 {
				t.Errorf("an in-range value was refused or not sent: %v (%d requests)", err, requests198.Load())
				return
			}
			// The value must reach the wire exactly as given (a CLI that dropped or
			// changed it would still succeed): compare the captured variable.
			op, name := "EmailMessages", "top"
			if tc.flag != "--top" {
				op, name = "EmailConnectionsPage", strings.TrimPrefix(tc.flag, "--")
			}
			want, _ := strconv.ParseFloat(tc.value, 64)
			if got := vars198(t, ops, op)[name]; got != want {
				t.Errorf("%s was sent as %v, want %v", tc.flag, got, want)
			}
		})
	}
}

// F3 (Cedar): an explicit --unread=false is sent as unreadOnly:false; only an
// unset flag is omitted.
func TestEmailMessagesExplicitUnreadFalseIsSent198(t *testing.T) {
	_, ops, err := runEmail198(t, map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[]}}`}, "messages", "--connection", "c", "--unread=false")
	if err != nil {
		t.Fatal(err)
	}
	v := vars198(t, ops, "EmailMessages")
	if got, ok := v["unreadOnly"]; !ok || got != false {
		t.Errorf("unreadOnly = %v (sent=%v), want an explicit false", got, ok)
	}
}

// F2 (Cedar): the continuation hint must advance. An empty page that still says
// hasMore has no honest next offset, so none is suggested.
func TestEmailConnectionsPagingHintMustAdvance198(t *testing.T) {
	empty := `{"data":{"exchangeConnectionsPage":{"items":[],"total":7,"hasMore":true}}}`
	out, _, err := runEmail198(t, map[string]string{"EmailConnectionsPage": empty}, "connections", "--org", "o", "--limit", "5", "--offset", "2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--offset") {
		t.Errorf("an empty page with hasMore suggested an offset that cannot advance:\n%s", out)
	}
	if !strings.Contains(out, "total 7") {
		t.Errorf("the total is missing:\n%s", out)
	}
}

// F5: the OSC 8 hyperlink the test comment names is in the fixture.
func TestEmailMessagesOSC8Neutralised198(t *testing.T) {
	row := `{"id":"m1","subject":"\u001b]8;;http://evil.example/\u0007click\u001b]8;;\u0007","from":null,"receivedDateTime":null,"bodyPreview":null,"body":null,"hasAttachments":null,"isRead":null}`
	out, _, err := runEmail198(t, map[string]string{"EmailMessages": `{"data":{"exchangeMessages":[` + row + `]}}`}, "messages", "--connection", "c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\a') {
		t.Errorf("an OSC 8 hyperlink reached the terminal raw:\n%q", out)
	}
}

// Joost's F1 (P2): the server clamps a negative offset to zero (skip =
// Math.max(0, offset)), so the continuation counts from the EFFECTIVE offset; the
// outgoing argument is not changed.
func TestEmailConnectionsPagingHintUsesEffectiveOffset198(t *testing.T) {
	one := `{"data":{"exchangeConnectionsPage":{"items":[` + connRow198 + `],"total":7,"hasMore":true}}}`
	out, ops, err := runEmail198(t, map[string]string{"EmailConnectionsPage": one}, "connections", "--org", "o", "--limit", "1", "--offset", "-5")
	if err != nil {
		t.Fatal(err)
	}
	if got := vars198(t, ops, "EmailConnectionsPage")["offset"]; got != float64(-5) {
		t.Errorf("the outgoing offset was changed: %v", got)
	}
	if !strings.Contains(out, "--offset 1") || strings.Contains(out, "--offset -") {
		t.Errorf("a negative offset must continue from the clamped skip (--offset 1):\n%s", out)
	}
}
