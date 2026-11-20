package cmd

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hadron-cli#198 Slice C: the mailbox effects. Fake HTTP only: no mailbox, no
// send, no provider spend. Gate and FIT constraints pinned here: every command
// is exactly one request and is never retried; reply goes to the sender only
// (no --all, no --to); reply, draft send and delete ask first and, without a
// terminal, need --yes with ZERO requests otherwise; --idempotency-key goes on
// draft save and send only and is passed unchanged, never invented; an
// ambiguous send is "outcome unknown, check the Sent folder", never "failed";
// the server's refusals pass through; the body is never echoed.

const secretBody198 = "BODY-SECRET-<b>hi</b>"

// runEffect198 runs one `hadron email` command with an optional stdin and
// returns output, the captured variables per operation, the HTTP request count
// and the error.
func runEffect198(t *testing.T, responses map[string]string, stdin string, args ...string) (string, map[string]json.RawMessage, int, error) {
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
	f.IOStreams.In = strings.NewReader(stdin)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"email"}, args...), "--server", counter.URL))
	err := root.Execute()
	return o.String(), ops, int(requests198.Load()), err
}

func bodyFile198(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.html")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	ok198  = true
	no198  = false
	err198 = `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`
	unk198 = `{"errors":[{"message":"provider said something","extensions":{"code":"EMAIL_TOOL_ERROR","emailErrorCode":"unknown"}}]}`
)

func boolResp198(field string, v bool) string {
	if v {
		return `{"data":{"` + field + `":true}}`
	}
	return `{"data":{"` + field + `":false}}`
}

// effect198 describes one command: how to call it, which operation it sends and
// the variables it must carry.
type effect198 struct {
	name   string
	op     string
	field  string
	args   []string // after "email", before --server
	vars   map[string]any
	absent []string // variables that must NOT be sent
	yes    bool     // asks for confirmation
}

func effects198(t *testing.T) []effect198 {
	body := bodyFile198(t, secretBody198)
	return []effect198{
		{"reply", "EmailReply", "exchangeReply", []string{"reply", "--connection", "c1", "m1", "--body-file", body, "--yes"},
			map[string]any{"connectionId": "c1", "messageId": "m1", "body": secretBody198}, nil, true},
		{"draft update", "EmailUpdateDraft", "emailUpdateDraft", []string{"draft", "update", "--connection", "c1", "d1", "--body-file", body},
			map[string]any{"connectionRef": "c1", "draftId": "d1", "bodyHtml": secretBody198}, []string{"idempotencyKey"}, false},
		{"draft send", "EmailSendDraft", "emailSendDraft", []string{"draft", "send", "--connection", "c1", "d1", "--yes"},
			map[string]any{"connectionRef": "c1", "draftId": "d1"}, []string{"idempotencyKey"}, true},
		{"mark-read", "EmailMarkRead", "emailMarkRead", []string{"mark-read", "--connection", "c1", "m1"},
			map[string]any{"connectionRef": "c1", "messageId": "m1"}, []string{"isRead"}, false},
		{"flag", "EmailFlagMessage", "emailFlagMessage", []string{"flag", "--connection", "c1", "m1"},
			map[string]any{"connectionRef": "c1", "messageId": "m1"}, []string{"flag"}, false},
		{"categorize", "EmailCategorizeMessage", "emailCategorizeMessage", []string{"categorize", "--connection", "c1", "m1", "--category", "Red", "--category", "Blue"},
			map[string]any{"connectionRef": "c1", "messageId": "m1", "categories": []any{"Red", "Blue"}}, nil, false},
		{"delete", "EmailDeleteMessage", "emailDeleteMessage", []string{"delete", "--connection", "c1", "m1", "--yes"},
			map[string]any{"connectionRef": "c1", "messageId": "m1"}, nil, true},
		{"move", "EmailMoveMessage", "exchangeMoveMessage", []string{"move", "--connection", "c1", "m1", "--folder", "f9"},
			map[string]any{"connectionId": "c1", "messageId": "m1", "folderId": "f9"}, nil, false},
	}
}

// Exactly one request, the exact variables, and the unset optionals omitted.
func TestEmailEffectsWire198(t *testing.T) {
	for _, e := range effects198(t) {
		t.Run(e.name, func(t *testing.T) {
			_, ops, n, err := runEffect198(t, map[string]string{e.op: boolResp198(e.field, ok198)}, "", append(e.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 || len(ops) != 1 {
				t.Fatalf("want exactly one request, got %d (%v)", n, ops)
			}
			v := vars198(t, ops, e.op)
			for k, want := range e.vars {
				got, ok := v[k]
				if !ok || !jsonEqual198(got, want) {
					t.Errorf("%s = %v (sent=%v), want %v", k, got, ok, want)
				}
			}
			for _, k := range e.absent {
				if _, ok := v[k]; ok {
					t.Errorf("%s was sent but must be omitted when unset: %v", k, v[k])
				}
			}
		})
	}
}

func jsonEqual198(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Refusals pass through unchanged; a false answer is "not confirmed" (exit 7),
// never success; unknown tool failures keep the fallback; and no failure is
// retried.
func TestEmailEffectsErrors198(t *testing.T) {
	for _, e := range effects198(t) {
		for _, tc := range []struct {
			name, wire string
			want       int
		}{
			{"forbidden passes through", err198, 8},
			{"unknown tool error keeps the fallback", unk198, 1},
			{"false is not confirmed", boolResp198(e.field, no198), 7},
		} {
			t.Run(e.name+" "+tc.name, func(t *testing.T) {
				out, _, n, err := runEffect198(t, map[string]string{e.op: tc.wire}, "", e.args...)
				if exitCodeFor(err) != tc.want {
					t.Fatalf("exit %d (%v), want %d", exitCodeFor(err), err, tc.want)
				}
				if n != 1 {
					t.Errorf("%d requests: a failed call is never retried", n)
				}
				all := strings.ToLower(out + errString198(err))
				if strings.Contains(all, "body-secret") {
					t.Errorf("the message body was echoed: %s %v", out, err)
				}
				if tc.want == 7 && (strings.Contains(all, "sent") && !strings.Contains(all, "not")) {
					t.Errorf("a false answer reads as success: %s %v", out, err)
				}
			})
		}
	}
}

func errString198(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// No effect without confirmation: reply, draft send and delete need --yes off a
// terminal, with ZERO requests otherwise.
func TestEmailEffectsNeedConfirmation198(t *testing.T) {
	for _, e := range effects198(t) {
		if !e.yes {
			continue
		}
		t.Run(e.name, func(t *testing.T) {
			var args []string
			for _, a := range e.args {
				if a != "--yes" {
					args = append(args, a)
				}
			}
			_, _, n, err := runEffect198(t, map[string]string{e.op: boolResp198(e.field, ok198)}, "", args...)
			if exitCodeFor(err) != 2 || n != 0 {
				t.Errorf("without --yes: exit %d (%v) with %d requests; want exit 2 and none", exitCodeFor(err), err, n)
			}
		})
	}
}

// A missing or blank connection or id is a usage error with zero requests.
func TestEmailEffectsNeedTheirIDs198(t *testing.T) {
	body := bodyFile198(t, "x")
	for _, args := range [][]string{
		{"reply", "m1", "--body-file", body, "--yes"},
		{"reply", "--connection", "", "m1", "--body-file", body, "--yes"},
		{"reply", "--connection", "c", "  ", "--body-file", body, "--yes"},
		{"draft", "update", "--connection", "c", "--body-file", body},
		{"draft", "send", "--connection", "c", "--yes"},
		{"mark-read", "--connection", "c"},
		{"flag", "--connection", "c"},
		{"categorize", "--connection", "c", "--category", "x"},
		{"delete", "--connection", "c", "--yes"},
		{"move", "--connection", "c", "m1"},
		{"move", "--connection", "c", "m1", "--folder", " "},
	} {
		_, _, n, err := runEffect198(t, map[string]string{}, "", args...)
		if exitCodeFor(err) != 2 || n != 0 {
			t.Errorf("email %v: exit %d (%v) with %d requests; want a usage error and none sent", args, exitCodeFor(err), err, n)
		}
	}
}

// Reply goes to the sender only: there is no --all and no --to to ask for more.
func TestEmailReplyHasNoRecipientFlags198(t *testing.T) {
	body := bodyFile198(t, "x")
	for _, extra := range [][]string{{"--all"}, {"--to", "x@example.test"}, {"--cc", "x@example.test"}} {
		args := append([]string{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"}, extra...)
		_, _, n, err := runEffect198(t, map[string]string{}, "", args...)
		if exitCodeFor(err) != 2 || n != 0 {
			t.Errorf("reply %v: exit %d (%v), %d requests; the flag must not exist", extra, exitCodeFor(err), err, n)
		}
	}
}

// The body comes from a file or stdin, is required, and is never echoed.
func TestEmailBodySources198(t *testing.T) {
	resp := map[string]string{"EmailReply": boolResp198("exchangeReply", ok198)}
	_, ops, n, err := runEffect198(t, resp, secretBody198+"\n", "reply", "--connection", "c", "m1", "--body-file", "-", "--yes")
	if err != nil || n != 1 || vars198(t, ops, "EmailReply")["body"] != secretBody198+"\n" {
		t.Errorf("stdin body: %v, %d requests, vars %v", err, n, ops)
	}
	for _, args := range [][]string{
		{"reply", "--connection", "c", "m1", "--yes"},
		{"reply", "--connection", "c", "m1", "--body-file", filepath.Join(t.TempDir(), "missing"), "--yes"},
		{"reply", "--connection", "c", "m1", "--body-file", bodyFile198(t, ""), "--yes"},
	} {
		_, _, n, err := runEffect198(t, resp, "", args...)
		if exitCodeFor(err) != 2 || n != 0 {
			t.Errorf("email %v: exit %d (%v), %d requests; want a usage error and none sent", args, exitCodeFor(err), err, n)
		}
	}
	out, _, _, err := runEffect198(t, resp, "", "reply", "--connection", "c", "m1", "--body-file", bodyFile198(t, secretBody198), "--yes")
	if err != nil || strings.Contains(out, "BODY-SECRET") {
		t.Errorf("the body was echoed (or the call failed): %v\n%s", err, out)
	}
}

func TestEmailDraftSave198(t *testing.T) {
	resp := map[string]string{"EmailSaveDraft": `{"data":{"emailSaveDraft":{"draftId":"d7","conversationId":null}}}`}
	body := bodyFile198(t, secretBody198)
	out, ops, n, err := runEffect198(t, resp, "", "draft", "save", "--connection", "c1", "--reply-to", "m1", "--body-file", body, "--json")
	if err != nil || n != 1 {
		t.Fatalf("%v, %d requests", err, n)
	}
	v := vars198(t, ops, "EmailSaveDraft")
	if v["connectionRef"] != "c1" || v["replyToMessageId"] != "m1" || v["bodyHtml"] != secretBody198 {
		t.Errorf("vars = %v", v)
	}
	for _, k := range []string{"to", "subject", "idempotencyKey"} {
		if _, ok := v[k]; ok {
			t.Errorf("%s sent though unset: %v", k, v[k])
		}
	}
	if !strings.Contains(out, `"draftId": "d7"`) || !strings.Contains(out, `"conversationId": null`) || strings.Contains(out, "BODY-SECRET") {
		t.Errorf("DTO = %s", out)
	}
	_, ops, _, err = runEffect198(t, resp, "", "draft", "save", "--connection", "c1", "--to", "a@example.test", "--to", "b@example.test", "--subject", "Hi", "--body-file", body)
	if err != nil {
		t.Fatal(err)
	}
	v = vars198(t, ops, "EmailSaveDraft")
	if !jsonEqual198(v["to"], []any{"a@example.test", "b@example.test"}) || v["subject"] != "Hi" {
		t.Errorf("fresh draft vars = %v", v)
	}
}

// --idempotency-key lives on draft save and draft send ONLY, goes through
// unchanged, and is never invented; draft update has no such flag.
func TestEmailIdempotencyKeyPassThrough198(t *testing.T) {
	body := bodyFile198(t, "x")
	saveResp := map[string]string{"EmailSaveDraft": `{"data":{"emailSaveDraft":{"draftId":"d","conversationId":null}}}`}
	_, ops, _, err := runEffect198(t, saveResp, "", "draft", "save", "--connection", "c", "--reply-to", "m", "--body-file", body, "--idempotency-key", " k-1 ")
	if err != nil || vars198(t, ops, "EmailSaveDraft")["idempotencyKey"] != " k-1 " {
		t.Errorf("save key: %v %v", err, ops)
	}
	_, ops, _, err = runEffect198(t, map[string]string{"EmailSendDraft": boolResp198("emailSendDraft", ok198)}, "", "draft", "send", "--connection", "c", "d1", "--idempotency-key", "k-2", "--yes")
	if err != nil || vars198(t, ops, "EmailSendDraft")["idempotencyKey"] != "k-2" {
		t.Errorf("send key: %v %v", err, ops)
	}
	_, _, n, err := runEffect198(t, map[string]string{}, "", "draft", "update", "--connection", "c", "d1", "--body-file", body, "--idempotency-key", "k")
	if exitCodeFor(err) != 2 || n != 0 {
		t.Errorf("draft update accepted an idempotency key the server does not have: exit %d (%v)", exitCodeFor(err), err)
	}
	_, _, n, err = runEffect198(t, map[string]string{}, "", "reply", "--connection", "c", "m", "--body-file", body, "--idempotency-key", "k", "--yes")
	if exitCodeFor(err) != 2 || n != 0 {
		t.Errorf("reply accepted an idempotency key the server does not have: exit %d (%v)", exitCodeFor(err), err)
	}
}

func TestEmailMarkReadFlagCategorize198(t *testing.T) {
	_, ops, _, err := runEffect198(t, map[string]string{"EmailMarkRead": boolResp198("emailMarkRead", ok198)}, "", "mark-read", "--connection", "c", "m1", "--unread")
	if err != nil || vars198(t, ops, "EmailMarkRead")["isRead"] != false {
		t.Errorf("--unread must send isRead:false: %v %v", err, ops)
	}
	for _, v := range []string{"FLAGGED", "NONE", "COMPLETE"} {
		_, ops, _, err := runEffect198(t, map[string]string{"EmailFlagMessage": boolResp198("emailFlagMessage", ok198)}, "", "flag", "--connection", "c", "m1", "--flag", v)
		if err != nil || vars198(t, ops, "EmailFlagMessage")["flag"] != v {
			t.Errorf("--flag %s: %v %v", v, err, ops)
		}
	}
	_, _, n, err := runEffect198(t, map[string]string{}, "", "flag", "--connection", "c", "m1", "--flag", "BOGUS")
	if exitCodeFor(err) != 2 || n != 0 {
		t.Errorf("an unknown flag value reached the server: exit %d (%v), %d requests", exitCodeFor(err), err, n)
	}
	_, ops, _, err = runEffect198(t, map[string]string{"EmailCategorizeMessage": boolResp198("emailCategorizeMessage", ok198)}, "", "categorize", "--connection", "c", "m1", "--clear")
	if err != nil || !jsonEqual198(vars198(t, ops, "EmailCategorizeMessage")["categories"], []any{}) {
		t.Errorf("--clear must send an explicit empty list: %v %v", err, ops)
	}
	_, _, n, err = runEffect198(t, map[string]string{}, "", "categorize", "--connection", "c", "m1")
	if exitCodeFor(err) != 2 || n != 0 {
		t.Errorf("categorize with neither --category nor --clear: exit %d (%v), %d requests", exitCodeFor(err), err, n)
	}
	_, _, n, err = runEffect198(t, map[string]string{}, "", "categorize", "--connection", "c", "m1", "--category", "x", "--clear")
	if exitCodeFor(err) != 2 || n != 0 {
		t.Errorf("--category with --clear: exit %d (%v), %d requests", exitCodeFor(err), err, n)
	}
}

// An ambiguous send (no answer from the server at all) is "outcome unknown,
// check the Sent folder" — never "failed" — and is not retried. A server
// refusal keeps the server's own words.
func TestEmailAmbiguousSendIsOutcomeUnknown198(t *testing.T) {
	body := bodyFile198(t, "x")
	for _, args := range [][]string{
		{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"},
		{"draft", "send", "--connection", "c", "d1", "--yes"},
	} {
		var hits int
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"capabilities":[]}`))
				return
			}
			hits++
			conn, buf, _ := w.(http.Hijacker).Hijack()
			_ = bufio.NewReader(buf)
			_ = conn.Close() // the connection dies with no answer
		})}}
		srv.Start()
		f, o := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"email"}, args...), "--server", srv.URL))
		err = root.Execute()
		srv.Close()
		if err == nil {
			t.Errorf("email %v: a dropped connection reported success", args)
			continue
		}
		msg := strings.ToLower(err.Error() + o.String())
		if !strings.Contains(msg, "outcome unknown") || !strings.Contains(msg, "sent folder") {
			t.Errorf("email %v: want \"outcome unknown\" and \"Sent folder\", got: %v", args, err)
		}
		if strings.Contains(msg, "failed") {
			t.Errorf("email %v: an ambiguous send must never read as failed: %v", args, err)
		}
		if hits != 1 {
			t.Errorf("email %v: %d requests; an ambiguous send is never retried", args, hits)
		}
	}
}
