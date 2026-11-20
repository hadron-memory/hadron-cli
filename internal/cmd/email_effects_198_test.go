package cmd

import (
	"bufio"
	"encoding/json"
	"io"
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
	text, _, _, err := runEffect198(t, resp, "", "draft", "save", "--connection", "c1", "--reply-to", "m1", "--body-file", body)
	if err != nil || strings.Contains(text, "BODY-SECRET") || !strings.Contains(text, "d7") {
		t.Errorf("text mode echoed the body (or lost the draft id): %v\n%s", err, text)
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

// An absent answer ({"data":{}}) to any effect is NOT a success: it reads as not
// confirmed (exit 7), exactly like a false, and prints no ok:true.
func TestEmailEffectsAbsentAnswerIsNotConfirmed198(t *testing.T) {
	for _, e := range effects198(t) {
		t.Run(e.name, func(t *testing.T) {
			out, _, n, err := runEffect198(t, map[string]string{e.op: `{"data":{}}`}, "", append(e.args, "--json")...)
			if exitCodeFor(err) != 7 || n != 1 {
				t.Fatalf("exit %d (%v) with %d requests; want exit 7 and one request", exitCodeFor(err), err, n)
			}
			if strings.Contains(out, `"ok": true`) {
				t.Errorf("an absent answer printed ok:true: %s", out)
			}
		})
	}
}

// runEffectTTY198 runs a command on an interactive terminal that answers with the
// given lines.
func runEffectTTY198(t *testing.T, responses map[string]string, answers string, args ...string) (stderr string, n int, ops map[string]json.RawMessage, err error) {
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
	f, _, errOut := testFactoryTTY(t, answers)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"email"}, args...), "--server", counter.URL))
	err = root.Execute()
	return errOut.String(), int(requests198.Load()), ops, err
}

// The prompts of reply, draft send and delete show the id made visible, never raw
// (a control-bearing id could clear the screen or forge a prompt line), while the
// id SENT is unchanged; y sends exactly one request and anything else sends none.
func TestEmailEffectPromptsAreSafeAndAnswered198(t *testing.T) {
	evil := "id\x1b[2J\nforged: send it? (y/N)"
	body := bodyFile198(t, "x")
	for _, tc := range []struct {
		name, op, field, varName string
		args                     []string
	}{
		{"reply", "EmailReply", "exchangeReply", "messageId", []string{"reply", evil, "--connection", "c", "--body-file", body}},
		{"draft send", "EmailSendDraft", "emailSendDraft", "draftId", []string{"draft", "send", evil, "--connection", "c"}},
		{"delete", "EmailDeleteMessage", "emailDeleteMessage", "messageId", []string{"delete", evil, "--connection", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := map[string]string{tc.op: boolResp198(tc.field, ok198)}
			stderr, n, _, err := runEffectTTY198(t, resp, "n\n", tc.args...)
			if err == nil || n != 0 {
				t.Fatalf("declining must send nothing: err %v, %d requests", err, n)
			}
			if strings.ContainsRune(stderr, '\x1b') || strings.Contains(stderr, "\nforged:") {
				t.Errorf("a control-bearing id reached the terminal raw:\n%q", stderr)
			}
			_, n, ops, err := runEffectTTY198(t, resp, "y\n", tc.args...)
			if err != nil || n != 1 {
				t.Fatalf("accepting must send exactly one request: err %v, %d requests", err, n)
			}
			if got := vars198(t, ops, tc.op)[tc.varName]; got != evil {
				t.Errorf("the id sent was changed: %q", got)
			}
			for _, ans := range []string{"\n", "no\n", ""} {
				if _, n, _, err := runEffectTTY198(t, resp, ans, tc.args...); err == nil || n != 0 {
					t.Errorf("answer %q: err %v, %d requests; anything but y/yes sends nothing", ans, err, n)
				}
			}
		})
	}
}

// Jane's #2740 P2 F1: a missing draft result is not a saved draft. A nil wrapper,
// a null emailSaveDraft and a blank draftId are all "the server returned no
// draft" (exit 7), never a success line or a panic.
func TestEmailDraftSaveMissingResult198(t *testing.T) {
	body := bodyFile198(t, "x")
	for _, wire := range []string{`{"data":{}}`, `{"data":{"emailSaveDraft":null}}`, `{"data":{"emailSaveDraft":{"draftId":"","conversationId":null}}}`, `{"data":{"emailSaveDraft":{"draftId":"  ","conversationId":null}}}`} {
		t.Run(wire, func(t *testing.T) {
			out, _, n, err := runEffect198(t, map[string]string{"EmailSaveDraft": wire}, "", "draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", body)
			if exitCodeFor(err) != 7 || n != 1 {
				t.Fatalf("exit %d (%v) with %d requests; want exit 7 and one request", exitCodeFor(err), err, n)
			}
			if strings.Contains(strings.ToLower(out), "saved") {
				t.Errorf("a missing result printed a success line: %s", out)
			}
		})
	}
}

// Jane's P2 F2: a GraphQL error is not proof the mail was never sent. Only the
// documented pre-send refusals (authorization, authentication, validation) are
// definitive; any other error keeps the server's words AND says the outcome is
// unknown, so the caller checks the Sent folder before trying again. A mixed list
// is ambiguous too.
func TestEmailSendExecutionErrorsKeepOutcomeUnknown198(t *testing.T) {
	body := bodyFile198(t, "x")
	sends := []struct {
		name, op string
		args     []string
	}{
		{"reply", "EmailReply", []string{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"}},
		{"draft send", "EmailSendDraft", []string{"draft", "send", "--connection", "c", "d1", "--yes"}},
	}
	for _, s := range sends {
		for _, tc := range []struct {
			name, wire    string
			wantUnknown   bool
			wantServerTxt string
			wantCode      string
		}{
			{"forbidden is a definitive refusal", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`, false, "Forbidden", "FORBIDDEN"},
			{"unauthenticated is definitive", `{"errors":[{"message":"sign in","extensions":{"code":"UNAUTHENTICATED"}}]}`, false, "sign in", "UNAUTHENTICATED"},
			{"a provider tool error may have sent", `{"errors":[{"message":"provider hiccup","extensions":{"code":"EMAIL_TOOL_ERROR","emailErrorCode":"unknown"}}]}`, true, "provider hiccup", "EMAIL_TOOL_ERROR"},
			{"an internal error may have sent", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, true, "internal", "INTERNAL_SERVER_ERROR"},
			{"an uncoded error may have sent", `{"errors":[{"message":"odd"}]}`, true, "odd", ""},
			{"a mixed list is ambiguous", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}},{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, true, "internal error", "FORBIDDEN"},
		} {
			t.Run(s.name+" "+tc.name, func(t *testing.T) {
				_, _, n, err := runEffect198(t, map[string]string{s.op: tc.wire}, "", s.args...)
				if err == nil || n != 1 {
					t.Fatalf("err %v with %d requests; want an error and exactly one request", err, n)
				}
				msg := strings.ToLower(err.Error())
				// reply submits a body, so its error text is the CLI's own plus the
				// server's CODE (never the server's message); draft send has no body and
				// keeps the server's words.
				want := tc.wantServerTxt
				if s.name == "reply" {
					want = tc.wantCode
				}
				if want != "" && !strings.Contains(msg, strings.ToLower(want)) {
					t.Errorf("the expected text %q is missing: %v", want, err)
				}
				unknown := strings.Contains(msg, "outcome unknown") && strings.Contains(msg, "sent folder")
				if unknown != tc.wantUnknown {
					t.Errorf("outcome-unknown guidance = %v, want %v: %v", unknown, tc.wantUnknown, err)
				}
			})
		}
	}
}

// Jane's P2 F3: reply's help must not promise a recipient-choosing workflow the
// draft commands do not offer (a reply-draft has the provider's recipients; a
// fresh draft is a different mode).
func TestEmailReplyHelpPromisesNoRecipientWorkflow198(t *testing.T) {
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"email", "reply"})
	if err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(cmd.Long), " ")
	if strings.Contains(help, "draft save --reply-to") || strings.Contains(help, "chosen recipients") {
		t.Errorf("reply help promises a recipient-choosing workflow: %q", help)
	}
	if !strings.Contains(help, "SENDER only") {
		t.Errorf("reply help no longer says it goes to the sender only: %q", help)
	}
}

// Jane's P3 F4: draft save has two exclusive forms; anything else is a usage error
// with zero requests, before the body is read.
func TestEmailDraftSaveFormsAreExclusive198(t *testing.T) {
	body := bodyFile198(t, "x")
	resp := map[string]string{"EmailSaveDraft": `{"data":{"emailSaveDraft":{"draftId":"d","conversationId":null}}}`}
	base := []string{"draft", "save", "--connection", "c", "--body-file", body}
	for _, bad := range [][]string{
		{},
		{"--to", "a@example.test"},
		{"--subject", "Hi"},
		{"--reply-to", "m1", "--to", "a@example.test"},
		{"--reply-to", "m1", "--subject", "Hi"},
		{"--reply-to", "  "},
		{"--to", " ", "--subject", "Hi"},
	} {
		_, _, n, err := runEffect198(t, resp, "", append(append([]string{}, base...), bad...)...)
		if exitCodeFor(err) != 2 || n != 0 {
			t.Errorf("draft save %v: exit %d (%v), %d requests; want a usage error and none sent", bad, exitCodeFor(err), err, n)
		}
	}
	for _, good := range [][]string{
		{"--reply-to", "m1"},
		{"--to", "a@example.test", "--subject", "Hi"},
		{"--to", "a@example.test", "--to", "b@example.test", "--subject", "Hi"},
	} {
		if _, _, n, err := runEffect198(t, resp, "", append(append([]string{}, base...), good...)...); err != nil || n != 1 {
			t.Errorf("draft save %v: err %v, %d requests", good, err, n)
		}
	}
}

// Joost's and Cedar's #2740 P2: the body must not come back out of the CLI even when
// the SERVER echoes it, whole, as an excerpt, or HTML-escaped. A command that submits
// a body therefore never prints the server's error MESSAGE: it prints its own words
// plus the server's error CODE, keeps the exit class, and for a send keeps the
// unknown-outcome guidance. A result id is the server's own value and is printed as
// returned (see the id checks below).
func TestEmailBodyIsWithheldFromServerEchoes198(t *testing.T) {
	multi := "line one SECRET-A\nline two SECRET-B <b>bold</b>"
	body := bodyFile198(t, multi)
	for _, msg := range []string{
		`provider rejected the whole body: ` + `line one SECRET-A\nline two SECRET-B <b>bold</b>`,
		`provider rejected SECRET-B`,
		`provider rejected &lt;b&gt;bold&lt;/b&gt; SECRET-B`,
		`provider rejected \u003cb\u003ebold SECRET-A`,
	} {
		echoErr := `{"errors":[{"message":"` + strings.ReplaceAll(msg, `"`, `\"`) + `","extensions":{"code":"EMAIL_TOOL_ERROR","emailErrorCode":"unknown"}}]}`
		for _, tc := range []struct {
			name, op string
			args     []string
			sends    bool
		}{
			{"reply", "EmailReply", []string{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"}, true},
			{"draft save", "EmailSaveDraft", []string{"draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", body}, false},
			{"draft update", "EmailUpdateDraft", []string{"draft", "update", "--connection", "c", "d1", "--body-file", body}, false},
		} {
			t.Run(tc.name+" "+msg, func(t *testing.T) {
				out, _, n, err := runEffect198(t, map[string]string{tc.op: echoErr}, "", tc.args...)
				if err == nil || n != 1 {
					t.Fatalf("err %v with %d requests", err, n)
				}
				all := out + err.Error()
				for _, leak := range []string{"SECRET-A", "SECRET-B", "bold"} {
					if strings.Contains(all, leak) {
						t.Errorf("part of the submitted body (%q) came back out through the server's error: %s", leak, all)
					}
				}
				if !strings.Contains(all, "EMAIL_TOOL_ERROR") {
					t.Errorf("the server's error code is missing: %s", all)
				}
				if exitCodeFor(err) != 1 {
					t.Errorf("exit %d, want the mapped class 1", exitCodeFor(err))
				}
				if tc.sends && !strings.Contains(strings.ToLower(all), "outcome unknown") {
					t.Errorf("the send lost its unknown-outcome guidance: %s", all)
				}
			})
		}
	}
	// A result id is the server's own value and is printed as returned, whatever it
	// looks like: a draft was CREATED, so hiding or failing on an id that happens to
	// equal (or contain) the submitted body would lose the handle and invite a
	// duplicate on retry (Dara's #2740 F1/F2). No guess is made about id contents.
	for _, jsonOut := range []bool{false, true} {
		args := []string{"draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", bodyFile198(t, "short")}
		if jsonOut {
			args = append(args, "--json")
		}
		echoID := `{"data":{"emailSaveDraft":{"draftId":"short","conversationId":"short"}}}`
		out, _, n, err := runEffect198(t, map[string]string{"EmailSaveDraft": echoID}, "", args...)
		if err != nil || n != 1 || !strings.Contains(out, "short") {
			t.Errorf("(json=%v) a valid id equal to the body was refused or hidden: err %v, %d requests, out %q", jsonOut, err, n, out)
		}
	}
	out, _, n, err := runEffect198(t, map[string]string{"EmailSaveDraft": `{"data":{"emailSaveDraft":{"draftId":"dx7","conversationId":"cx9"}}}`}, "", "draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", bodyFile198(t, "x"), "--json")
	if err != nil || n != 1 || !strings.Contains(out, `"draftId": "dx7"`) || !strings.Contains(out, `"conversationId": "cx9"`) {
		t.Errorf("a valid id containing a short body was altered: err %v, out %s", err, out)
	}
}

// Cedar's #2740 P2 F3: a body that is not valid UTF-8 would be altered (U+FFFD) by
// JSON encoding, so it is refused before any request instead of being sent changed.
// And the #2747 pins: the size cap, and --body-file - on an interactive terminal.
func TestEmailBodyValidationBeforeAnyRequest198(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "latin1.txt")
	if err := os.WriteFile(bad, []byte("caf\xe9 au lait"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := bodyFile198(t, strings.Repeat("a", (1<<20)+1))
	for _, body := range []struct{ name, path string }{{"invalid UTF-8", bad}, {"over the size cap", big}} {
		for _, args := range [][]string{
			{"reply", "--connection", "c", "m1", "--body-file", body.path, "--yes"},
			{"draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", body.path},
			{"draft", "update", "--connection", "c", "d1", "--body-file", body.path},
		} {
			_, _, n, err := runEffect198(t, map[string]string{}, "", args...)
			if exitCodeFor(err) != 2 || n != 0 {
				t.Errorf("%s, email %v: exit %d (%v), %d requests; want a usage error and none sent", body.name, args[:2], exitCodeFor(err), err, n)
			}
		}
	}
	_, n, _, err := runEffectTTY198(t, map[string]string{}, "typed\n", "reply", "--connection", "c", "m1", "--body-file", "-", "--yes")
	if exitCodeFor(err) != 2 || n != 0 {
		t.Errorf("--body-file - on a terminal: exit %d (%v), %d requests; want a usage error and none sent", exitCodeFor(err), err, n)
	}
	// Valid multi-byte UTF-8 is untouched.
	ok := bodyFile198(t, "caf\u00e9 \u2713 <b>ok</b>")
	_, ops, n2, err := runEffect198(t, map[string]string{"EmailReply": boolResp198("exchangeReply", ok198)}, "", "reply", "--connection", "c", "m1", "--body-file", ok, "--yes")
	if err != nil || n2 != 1 || vars198(t, ops, "EmailReply")["body"] != "caf\u00e9 \u2713 <b>ok</b>" {
		t.Errorf("valid UTF-8 was altered or refused: %v %v", err, ops)
	}
}

// runEffectRendered198 runs a command with --json and returns what the real
// renderError wrote for its failure: the envelope a script would see.
func runEffectRendered198(t *testing.T, responses map[string]string, args ...string) (rendered string, err error) {
	t.Helper()
	srv, _ := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"email"}, args...), "--server", srv.URL, "--json"))
	err = root.Execute()
	if err == nil {
		return "", nil
	}
	renderError(f, err)
	return out.String() + f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// Joost's #2740 P2: the server's extensions.code is DATA from outside; a body-
// submitting command must not carry a body-bearing code into the --json envelope's
// serverCode (or the message). A well-formed code (UPPER_SNAKE) is kept for
// scripts; anything else is dropped.
func TestEmailBodyCommandsDoNotLeakThroughServerCode198(t *testing.T) {
	body := bodyFile198(t, "line one SECRET-A\nline two SECRET-B")
	cmds := []struct {
		name, op string
		args     []string
	}{
		{"reply", "EmailReply", []string{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"}},
		{"draft save", "EmailSaveDraft", []string{"draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", body}},
		{"draft update", "EmailUpdateDraft", []string{"draft", "update", "--connection", "c", "d1", "--body-file", body}},
	}
	for _, c := range cmds {
		for _, code := range []string{"line one SECRET-A", "SECRET-B", "bad code with spaces SECRET-A"} {
			t.Run(c.name+" "+code, func(t *testing.T) {
				wire := `{"errors":[{"message":"x","extensions":{"code":"` + code + `"}}]}`
				out, err := runEffectRendered198(t, map[string]string{c.op: wire}, c.args...)
				if err == nil {
					t.Fatal("expected an error")
				}
				if strings.Contains(out, "SECRET-A") || strings.Contains(out, "SECRET-B") {
					t.Errorf("a body-bearing server code reached the rendered error: %s", out)
				}
			})
		}
		t.Run(c.name+" a well-formed code is kept", func(t *testing.T) {
			wire := `{"errors":[{"message":"x","extensions":{"code":"EMAIL_TOOL_ERROR"}}]}`
			out, err := runEffectRendered198(t, map[string]string{c.op: wire}, c.args...)
			if err == nil || !strings.Contains(out, `"serverCode": "EMAIL_TOOL_ERROR"`) {
				t.Errorf("a well-formed server code was dropped: %v\n%s", err, out)
			}
		})
	}
}

// Andi's #2740 P2 F1: only a code from a fixed allowlist of protocol codes is ever
// shown or carried; a shape-valid but body-derived code (PRIVATE_DRAFT, SECRET_B) is
// dropped, in the message and in the real --json envelope.
func TestEmailBodyCommandsCarryOnlyKnownServerCodes198(t *testing.T) {
	body := bodyFile198(t, "PRIVATE_DRAFT and SECRET_B")
	for _, code := range []string{"PRIVATE_DRAFT", "SECRET_B", "BODY_DERIVED_X"} {
		for _, c := range []struct {
			name, op string
			args     []string
		}{
			{"reply", "EmailReply", []string{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"}},
			{"draft save", "EmailSaveDraft", []string{"draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", body}},
			{"draft update", "EmailUpdateDraft", []string{"draft", "update", "--connection", "c", "d1", "--body-file", body}},
		} {
			t.Run(c.name+" "+code, func(t *testing.T) {
				wire := `{"errors":[{"message":"x","extensions":{"code":"` + code + `"}}]}`
				out, err := runEffectRendered198(t, map[string]string{c.op: wire}, c.args...)
				if err == nil {
					t.Fatal("expected an error")
				}
				if strings.Contains(out, code) {
					t.Errorf("an unknown server code reached the rendered error: %s", out)
				}
			})
		}
	}
	for _, code := range []string{"FORBIDDEN", "UNAUTHENTICATED", "BAD_USER_INPUT", "EMAIL_TOOL_ERROR", "EMAIL_TOOL_UNREACHABLE", "INTERNAL_SERVER_ERROR"} {
		wire := `{"errors":[{"message":"x","extensions":{"code":"` + code + `"}}]}`
		out, err := runEffectRendered198(t, map[string]string{"EmailUpdateDraft": wire}, "draft", "update", "--connection", "c", "d1", "--body-file", body)
		if err == nil || !strings.Contains(out, `"serverCode": "`+code+`"`) {
			t.Errorf("the known code %s was dropped: %v\n%s", code, err, out)
		}
	}
}

// readerSpy198 records whether it was read.
type readerSpy198 struct {
	touched bool
	data    string
}

func (r *readerSpy198) Read(p []byte) (int, error) {
	r.touched = true
	if r.data == "" {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// Andi's P2 F2 and P3 F5: everything that can be refused BEFORE reading is refused
// before stdin is touched: a send with no --yes off a terminal, and an invalid
// draft-save flag combination.
func TestEmailRefusalsHappenBeforeStdinIsRead198(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"reply without --yes", []string{"reply", "--connection", "c", "m1", "--body-file", "-"}},
		{"draft save with no form", []string{"draft", "save", "--connection", "c", "--body-file", "-"}},
		{"draft save with both forms", []string{"draft", "save", "--connection", "c", "--reply-to", "m1", "--to", "a@example.test", "--subject", "S", "--body-file", "-"}},
		{"draft save with --to only", []string{"draft", "save", "--connection", "c", "--to", "a@example.test", "--body-file", "-"}},
		{"reply without a connection", []string{"reply", "m1", "--body-file", "-", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests198.Store(0)
			srv, _ := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			spy := &readerSpy198{data: "the body"}
			f.IOStreams.In = spy
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"email"}, tc.args...), "--server", srv.URL))
			err := root.Execute()
			if exitCodeFor(err) != 2 {
				t.Errorf("exit %d (%v), want a usage error", exitCodeFor(err), err)
			}
			if spy.touched {
				t.Error("stdin was read before the command was refused")
			}
		})
	}
}

// Andi's P3 F3: the reply help does not promise the server's refusal text.
func TestEmailReplyHelpDescribesWithheldRefusals198(t *testing.T) {
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"email", "reply"})
	if err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(cmd.Long), " ")
	if strings.Contains(help, "shown as it comes") {
		t.Errorf("reply help promises the server's refusal text: %q", help)
	}
	if !strings.Contains(help, "withheld") {
		t.Errorf("reply help does not say the server's message is withheld: %q", help)
	}
}

// Rodolfo's #2740 pre-fold P3: a non-200 answer whose JSON has no errors list
// (data or extensions carrying body text) must not be printed by a body-submitting
// command; the command says what happened (the HTTP status) without the body, and
// keeps a nonzero exit.
func TestEmailBodyCommandsWithholdNon200Envelopes198(t *testing.T) {
	body := bodyFile198(t, "line one SECRET-A")
	for _, answer := range []struct {
		name   string
		status int
		body   string
	}{
		{"extensions echo", 400, `{"data":null,"extensions":{"echo":"SECRET-A"}}`},
		{"data echo", 400, `{"data":{"x":"SECRET-A"}}`},
		{"plain echo", 413, `line one SECRET-A is too large`},
		{"html echo", 502, `<html>SECRET-A</html>`},
	} {
		for _, c := range []struct {
			name string
			args []string
		}{
			{"draft save", []string{"draft", "save", "--connection", "c", "--reply-to", "m1", "--body-file", body}},
			{"draft update", []string{"draft", "update", "--connection", "c", "d1", "--body-file", body}},
			{"reply", []string{"reply", "--connection", "c", "m1", "--body-file", body, "--yes"}},
		} {
			t.Run(c.name+" "+answer.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"capabilities":[]}`))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(answer.status)
					_, _ = w.Write([]byte(answer.body))
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{"email"}, c.args...), "--server", srv.URL, "--json"))
				err := root.Execute()
				if err == nil {
					t.Fatal("a non-200 answer reported success")
				}
				renderError(f, err)
				all := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String() + err.Error()
				if strings.Contains(all, "SECRET-A") {
					t.Errorf("a non-200 envelope echoing the body was printed: %s", all)
				}
				if exitCodeFor(err) == 0 {
					t.Errorf("exit 0 for a failed request")
				}
				if c.name == "reply" && !strings.Contains(strings.ToLower(all), "outcome unknown") {
					t.Errorf("a reply that got no usable answer lost its unknown-outcome guidance: %s", all)
				}
			})
		}
	}
}
