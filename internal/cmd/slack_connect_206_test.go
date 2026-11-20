package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#206 connect/disconnect (T2 + §10). Fake HTTP only: no Slack, no network.

const (
	botTok = "xoxb-TESTBOT-1234567890-secretbot"
	appTok = "xapp-TESTAPP-1234567890-secretapp"
)

const appID = "01a0a702033c7099b23cbd358cd0c269"

const connectedRow = `{"id":"c1","organizationId":"o1","defaultAppId":"a1","provider":"slack","status":"ACTIVE","teamId":"T1","teamName":"Acme","botId":"B1","botUserId":"U1","createdAt":"c","updatedAt":"u","lastError":"PRIVATE_ERR","botToken":"PRIVATE_TOK"}`

func tokenFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// runSlackWrite runs slack <args> against a fake that answers op with wire,
// counts every GraphQL request, and returns stdout, the captured variables,
// the request count and the command's error. stdin, when set, is what "-" reads.
func runSlackWrite(t *testing.T, op, wire, stdin string, args ...string) (string, map[string]json.RawMessage, int, error) {
	t.Helper()
	var n int32
	srv, ops := captureGraphQLFunc(t, func(o string) string {
		atomic.AddInt32(&n, 1)
		if o == op {
			return wire
		}
		return ""
	})
	f, out := testFactory(t)
	if stdin != "" {
		f.IOStreams.In = strings.NewReader(stdin)
	}
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"slack"}, args...), "--server", srv.URL))
	e := root.Execute()
	return out.String(), ops, int(atomic.LoadInt32(&n)), e
}

func noSecret(t *testing.T, where, s string) {
	t.Helper()
	for _, secret := range []string{botTok, appTok, "secretbot", "secretapp", "PRIVATE"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s carries %q:\n%s", where, secret, s)
		}
	}
}

func TestSlackConnectSendsOneRequestWithBothTokensAndShowsNoSecret(t *testing.T) {
	bot := tokenFile(t, "bot", botTok+"\n")
	app := tokenFile(t, "app", appTok+"\r\n")
	out, ops, n, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":`+connectedRow+`}}`, "",
		"connect", "--org", "hrn:org:acme.com", "--app", "hrn:app:acme.com:assistant", "--bot-token-file", bot, "--app-token-file", app, "--json")
	if e != nil {
		t.Fatal(e)
	}
	if n != 1 {
		t.Fatalf("one request exactly, got %d", n)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["CreateSlackConnection"], &vars)
	if vars["orgRef"] != "hrn:org:acme.com" || vars["appRef"] != "hrn:app:acme.com:assistant" || vars["botToken"] != botTok || vars["appToken"] != appTok {
		t.Fatalf("variables %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err, out)
	}
	if len(dto) != 11 || dto["id"] != "c1" || dto["status"] != "ACTIVE" {
		t.Fatalf("the connection shape is slack get's: %v", dto)
	}
	noSecret(t, "stdout", out)
	if strings.Contains(out, "lastError") {
		t.Errorf("lastError is never shown: %s", out)
	}
}

func TestSlackConnectTextOutputAndOneTokenFromStdin(t *testing.T) {
	app := tokenFile(t, "app", appTok)
	out, ops, n, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":`+connectedRow+`}}`, botTok+"\n",
		"connect", "--org", "o1", "--app", appID, "--bot-token-file", "-", "--app-token-file", app)
	if e != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["CreateSlackConnection"], &vars)
	if vars["botToken"] != botTok || vars["appToken"] != appTok {
		t.Fatalf("stdin must supply exactly the bot token: %v", vars)
	}
	if !strings.Contains(out, "connected Slack workspace") || !strings.Contains(out, `"c1"`) {
		t.Fatalf("text output: %s", out)
	}
	noSecret(t, "stdout", out)
}

func TestSlackConnectRefusesLocallyBeforeAnyRequest(t *testing.T) {
	good := tokenFile(t, "tok", botTok)
	empty := tokenFile(t, "empty", "\n")
	cases := map[string][]string{
		"both stdin":          {"connect", "--org", "o1", "--app", appID, "--bot-token-file", "-", "--app-token-file", "-"},
		"no bot flag":         {"connect", "--org", "o1", "--app", appID, "--app-token-file", good},
		"no app-token flag":   {"connect", "--org", "o1", "--app", appID, "--bot-token-file", good},
		"no org":              {"connect", "--app", appID, "--bot-token-file", good, "--app-token-file", good},
		"no app":              {"connect", "--org", "o1", "--bot-token-file", good, "--app-token-file", good},
		"blank org":           {"connect", "--org", "  ", "--app", appID, "--bot-token-file", good, "--app-token-file", good},
		"empty token file":    {"connect", "--org", "o1", "--app", appID, "--bot-token-file", empty, "--app-token-file", good},
		"missing token file":  {"connect", "--org", "o1", "--app", appID, "--bot-token-file", filepath.Join(t.TempDir(), "nope"), "--app-token-file", good},
		"token as a flag":     {"connect", "--org", "o1", "--app", appID, "--bot-token", botTok, "--app-token-file", good},
		"app token as a flag": {"connect", "--org", "o1", "--app", appID, "--bot-token-file", good, "--app-token", appTok},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":`+connectedRow+`}}`, "", args...)
			if e == nil || n != 0 {
				t.Fatalf("must refuse before any request: err=%v requests=%d", e, n)
			}
			if exitCodeFor(e) != exitcode.Usage {
				t.Errorf("exit %d, want usage: %v", exitCodeFor(e), e)
			}
			noSecret(t, "error", e.Error())
			noSecret(t, "stdout", out)
		})
	}
}

// The both-stdin refusal is its own check, with its own words, not an empty
// second read that happens to fail.
func TestSlackConnectBothStdinIsRefusedByName(t *testing.T) {
	_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{}`, botTok+"\n"+appTok+"\n",
		"connect", "--org", "o1", "--app", appID, "--bot-token-file", "-", "--app-token-file", "-")
	if e == nil || n != 0 || !strings.Contains(e.Error(), "only one of --bot-token-file and --app-token-file may be -") {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	noSecret(t, "error", e.Error())
}

// "-" on a terminal would have the token typed in the clear: refused, nothing sent.
func TestSlackConnectRefusesStdinFromATerminal(t *testing.T) {
	good := tokenFile(t, "tok", appTok)
	var n int32
	srv, _ := captureGraphQLFunc(t, func(string) string { atomic.AddInt32(&n, 1); return "" })
	f, _, _ := testFactoryTTY(t, botTok+"\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"slack", "connect", "--org", "o1", "--app", appID, "--bot-token-file", "-", "--app-token-file", good, "--server", srv.URL})
	e := root.Execute()
	if e == nil || atomic.LoadInt32(&n) != 0 || !strings.Contains(e.Error(), "which is a terminal") {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	noSecret(t, "error", e.Error())
}

// A token pasted where a path belongs must not come back in the error (#1490).
func TestSlackConnectNeverEchoesATokenPastedAsAPath(t *testing.T) {
	good := tokenFile(t, "tok", appTok)
	_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{}`, "",
		"connect", "--org", "o1", "--app", appID, "--bot-token-file", botTok, "--app-token-file", good)
	if e == nil || n != 0 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	noSecret(t, "error", e.Error())
}

func TestSlackConnectPassesServerRefusalsThroughAndWithholdsAnEchoedToken(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	args := []string{"connect", "--org", "o1", "--app", appID, "--bot-token-file", bot, "--app-token-file", app}
	for name, c := range map[string]struct {
		wire string
		want int
	}{
		"not an org admin":  {`{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}],"data":null}`, exitcode.Forbidden},
		"unknown org":       {`{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}],"data":null}`, exitcode.NotFound},
		"already connected": {`{"errors":[{"message":"workspace already connected","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`, exitcode.Usage},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, n, e := runSlackWrite(t, "CreateSlackConnection", c.wire, "", args...)
			if e == nil || n != 1 {
				t.Fatalf("one request, then the refusal: err=%v requests=%d", e, n)
			}
			if exitCodeFor(e) != c.want {
				t.Errorf("exit %d, want %d: %v", exitCodeFor(e), c.want, e)
			}
		})
	}
	// A server or proxy message that repeats the request's own token.
	echo := `{"errors":[{"message":"invalid_auth for ` + botTok + ` and ` + appTok + `","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`
	out, _, _, e := runSlackWrite(t, "CreateSlackConnection", echo, "", args...)
	if e == nil {
		t.Fatal("must refuse")
	}
	noSecret(t, "error", e.Error())
	noSecret(t, "stdout", out)
	if exitCodeFor(e) != exitcode.Usage {
		t.Errorf("the refusal keeps its class: exit %d", exitCodeFor(e))
	}
}

func TestSlackConnectAmbiguousFailureIsSentOnceAndPointsAtStatus(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, "",
		"connect", "--org", "acme.com", "--app", appID, "--bot-token-file", bot, "--app-token-file", app)
	if e == nil || n != 1 {
		t.Fatalf("sent once, never retried: err=%v requests=%d", e, n)
	}
	if !strings.Contains(e.Error(), "outcome unknown") || !strings.Contains(e.Error(), `hadron slack status --org acme.com`) {
		t.Errorf("must say the outcome is unknown and where to look: %v", e)
	}
	noSecret(t, "error", e.Error())
}

func TestSlackConnectEmptyAnswerIsNotSuccess(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	_, _, _, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":null}}`, "",
		"connect", "--org", "o1", "--app", appID, "--bot-token-file", bot, "--app-token-file", app)
	if e == nil || exitCodeFor(e) != exitcode.Unavailable {
		t.Fatalf("a null answer is Unavailable, never connected: %v", e)
	}
}

func TestSlackConnectHelpNamesNoTokenValue(t *testing.T) {
	h := slackHelp(t, "slack", "connect")
	for _, want := range []string{"--bot-token-file", "--app-token-file", "Only one of the two may be -", "never retried", "hadron slack status --org"} {
		if !strings.Contains(h, want) {
			t.Errorf("connect help missing %q: %s", want, h)
		}
	}
	if strings.Contains(h, "xoxb-TEST") || strings.Contains(h, "--bot-token ") || strings.Contains(h, "--app-token ") {
		t.Errorf("connect help must not offer a token as a flag value: %s", h)
	}
}

func TestSlackDisconnectNeedsYesWithoutATerminalAndSendsNothing(t *testing.T) {
	_, _, n, e := runSlackWrite(t, "DeleteSlackConnection", `{"data":{"deleteSlackConnection":true}}`, "", "disconnect", "c1")
	if e == nil || n != 0 || exitCodeFor(e) != exitcode.Usage {
		t.Fatalf("zero requests without --yes: err=%v requests=%d", e, n)
	}
}

func TestSlackDisconnectSendsOneRequestWithTheExactRef(t *testing.T) {
	out, ops, n, e := runSlackWrite(t, "DeleteSlackConnection", `{"data":{"deleteSlackConnection":true}}`, "", "disconnect", " c1 ", "--yes", "--json")
	if e != nil || n != 1 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["DeleteSlackConnection"], &vars)
	if vars["ref"] != "c1" {
		t.Fatalf("variables %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["connectionId"] != "c1" || dto["disconnected"] != true || len(dto) != 2 {
		t.Fatalf("json %s (%v)", out, err)
	}
}

func TestSlackDisconnectPassesNotFoundThroughAndNeverPreReads(t *testing.T) {
	_, _, n, e := runSlackWrite(t, "DeleteSlackConnection", `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}],"data":null}`, "", "disconnect", "c1", "--yes")
	if e == nil || n != 1 || exitCodeFor(e) != exitcode.NotFound {
		t.Fatalf("one mutation, no existence read, NOT_FOUND passed through: err=%v requests=%d", e, n)
	}
}

func TestSlackDisconnectFalseAnswerIsNotSuccessAndAmbiguousIsUnknown(t *testing.T) {
	_, _, _, e := runSlackWrite(t, "DeleteSlackConnection", `{"data":{"deleteSlackConnection":false}}`, "", "disconnect", "c1", "--yes")
	if e == nil || exitCodeFor(e) != exitcode.Unavailable || !strings.Contains(e.Error(), "did not confirm") {
		t.Fatalf("false is Unavailable: %v", e)
	}
	_, _, n, e := runSlackWrite(t, "DeleteSlackConnection", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, "", "disconnect", "c1", "--yes")
	if e == nil || n != 1 || !strings.Contains(e.Error(), "outcome unknown") {
		t.Fatalf("sent once, outcome unknown: err=%v requests=%d", e, n)
	}
}

func TestSlackDisconnectAsksOnATerminal(t *testing.T) {
	for answer, wantRequests := range map[string]int{"n\n": 0, "y\n": 1} {
		var n int32
		srv, _ := captureGraphQLFunc(t, func(op string) string {
			atomic.AddInt32(&n, 1)
			return `{"data":{"deleteSlackConnection":true}}`
		})
		f, _, errOut := testFactoryTTY(t, answer)
		root := NewRootCmd(f)
		root.SetArgs([]string{"slack", "disconnect", "c1", "--server", srv.URL})
		_ = root.Execute()
		if int(atomic.LoadInt32(&n)) != wantRequests {
			t.Errorf("answer %q: %d requests, want %d", answer, n, wantRequests)
		}
		if p := errOut.String(); !strings.Contains(p, "Disconnect Slack connection") || strings.Contains(p, "cannot be undone") || strings.Contains(p, "restore") ||
			!strings.Contains(p, "best effort") || !strings.Contains(p, "registers it anew") {
			t.Errorf("the prompt names a soft delete with best-effort teardown, no restore promise: %q", p)
		}
	}
}

// runSlackRendered runs slack connect through the same error renderer the binary
// uses and returns everything either stream carried, so a leak anywhere in the
// final envelope (message, serverCode, details) is measured, not just Error().
func runSlackRendered(t *testing.T, wire string, jsonMode bool, args ...string) (all string, ops int, err error) {
	t.Helper()
	var n int32
	srv, _ := captureGraphQLFunc(t, func(o string) string {
		atomic.AddInt32(&n, 1)
		if o == "CreateSlackConnection" {
			return wire
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	a := append(append([]string{"slack"}, args...), "--server", srv.URL)
	if jsonMode {
		a = append(a, "--json")
	}
	root.SetArgs(a)
	err = root.Execute()
	if err != nil {
		renderError(f, err)
	}
	stderr, _ := f.IOStreams.ErrOut.(fmt.Stringer)
	all = out.String()
	if stderr != nil {
		all += stderr.String()
	}
	return all, int(atomic.LoadInt32(&n)), err
}

func connectArgs(t *testing.T) []string {
	t.Helper()
	return []string{"connect", "--org", "o1", "--app", appID,
		"--bot-token-file", tokenFile(t, "bot", botTok), "--app-token-file", tokenFile(t, "app", appTok)}
}

// Blind P1 / local P2 (#2731): the server's text is untrusted EVERYWHERE it can
// reach the operator: the message, extensions.code (printed as serverCode in the
// --json envelope), and every field of a success answer.
func TestSlackConnectNeverPrintsASubmittedTokenFromAnyServerText(t *testing.T) {
	for _, tok := range []string{botTok, appTok} {
		cases := map[string]string{
			"message only":  `{"errors":[{"message":"invalid_auth ` + tok + `","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`,
			"code only":     `{"errors":[{"message":"refused","extensions":{"code":"` + tok + `"}}],"data":null}`,
			"message+code":  `{"errors":[{"message":"bad ` + tok + `","extensions":{"code":"X_` + tok + `"}}],"data":null}`,
			"ambiguous+msg": `{"errors":[{"message":"boom ` + tok + `","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`,
		}
		for name, wire := range cases {
			for _, jsonMode := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/json=%v", name, tok[:8], jsonMode), func(t *testing.T) {
					all, n, e := runSlackRendered(t, wire, jsonMode, connectArgs(t)...)
					if e == nil || n != 1 {
						t.Fatalf("err=%v requests=%d", e, n)
					}
					noSecret(t, "the rendered output", all)
				})
			}
		}
	}
}

// A refusal whose message echoes a token keeps its class, its (clean) server
// code, and, for an ambiguous failure, the unknown-outcome notice: the scrub
// edits the server's text, it does not replace the command's own guidance.
func TestSlackConnectScrubKeepsClassCodeAndRecoveryGuidance(t *testing.T) {
	all, _, e := runSlackRendered(t, `{"errors":[{"message":"bad `+botTok+`","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`, true, connectArgs(t)...)
	if exitCodeFor(e) != exitcode.Usage || !strings.Contains(all, `"serverCode": "BAD_USER_INPUT"`) && !strings.Contains(all, `"serverCode":"BAD_USER_INPUT"`) {
		t.Errorf("class and clean serverCode must survive the scrub: exit %d\n%s", exitCodeFor(e), all)
	}
	noSecret(t, "the rendered output", all)
	all, _, e = runSlackRendered(t, `{"errors":[{"message":"boom `+appTok+`","extensions":{"code":"SOMETHING_UNMAPPED"}}],"data":null}`, false, connectArgs(t)...)
	for _, want := range []string{"outcome unknown", "not retried", "hadron slack status --org", "--offset", withheldText} {
		if !strings.Contains(all, want) {
			t.Errorf("an echoed token must not cost the recovery guidance, missing %q:\n%s", want, all)
		}
	}
	noSecret(t, "the rendered output", all)
	if e == nil {
		t.Fatal("must fail")
	}
}

const withheldText = "[redacted]"

// The success answer is the server's text too: a field repeating a submitted
// token is never rendered (text or JSON), and not even the id is quoted back.
func TestSlackConnectWithholdsASuccessAnswerThatRepeatsAToken(t *testing.T) {
	for _, field := range []string{"id", "organizationId", "defaultAppId", "provider", "status", "teamId", "teamName", "botId", "botUserId", "createdAt", "updatedAt"} {
		for _, tok := range []string{botTok, appTok} {
			var row map[string]any
			_ = json.Unmarshal([]byte(connectedRow), &row)
			row[field] = tok
			b, _ := json.Marshal(row)
			for _, jsonMode := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/json=%v", field, tok[:8], jsonMode), func(t *testing.T) {
					all, n, e := runSlackRendered(t, `{"data":{"createSlackConnection":`+string(b)+`}}`, jsonMode, connectArgs(t)...)
					if e == nil || n != 1 || exitCodeFor(e) != exitcode.Unavailable {
						t.Fatalf("a repeated token is Unavailable, withheld: err=%v requests=%d", e, n)
					}
					noSecret(t, "the rendered output", all)
					if !strings.Contains(all, "hadron slack status --org") {
						t.Errorf("must say where to look: %s", all)
					}
				})
			}
		}
	}
}

func TestSlackConnectAppTokenFromStdinAndBotTokenFromAFile(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	_, ops, n, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":`+connectedRow+`}}`, appTok+"\n",
		"connect", "--org", "o1", "--app", appID, "--bot-token-file", bot, "--app-token-file", "-", "--json")
	if e != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["CreateSlackConnection"], &vars)
	if vars["botToken"] != botTok || vars["appToken"] != appTok {
		t.Fatalf("variables %v", vars)
	}
}

// F3 (§10): "both required" is its own refusal, not an accident of reading an
// empty stdin: with a non-empty pipe and one flag missing, the named message wins
// and nothing is read from the pipe or sent.
func TestSlackConnectMissingTokenFlagIsRefusedByNameEvenWithPipedStdin(t *testing.T) {
	good := tokenFile(t, "tok", appTok)
	for name, args := range map[string][]string{
		"no bot flag":       {"connect", "--org", "o1", "--app", appID, "--app-token-file", good},
		"no app-token flag": {"connect", "--org", "o1", "--app", appID, "--bot-token-file", good},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{}`, botTok+"\n", args...)
			if e == nil || n != 0 || !strings.Contains(e.Error(), "--bot-token-file and --app-token-file are both required") {
				t.Fatalf("err=%v requests=%d", e, n)
			}
		})
	}
}

// An echoing gateway can render a token percent-encoded; the shared redactor
// covers those spellings in the message, the code and a success field.
func TestSlackConnectRedactsAPercentEncodedEcho(t *testing.T) {
	tok := "xoxb-A/b+c=d-secretbot"
	enc := url.QueryEscape(tok)
	bot := tokenFile(t, "bot", tok)
	app := tokenFile(t, "app", appTok)
	args := []string{"connect", "--org", "o1", "--app", appID, "--bot-token-file", bot, "--app-token-file", app}
	for name, wire := range map[string]string{
		"message": `{"errors":[{"message":"bad ` + enc + `","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`,
		"code":    `{"errors":[{"message":"refused","extensions":{"code":"` + enc + `"}}],"data":null}`,
		"success": `{"data":{"createSlackConnection":` + strings.Replace(connectedRow, `"teamName":"Acme"`, `"teamName":"`+enc+`"`, 1) + `}}`,
	} {
		t.Run(name, func(t *testing.T) {
			all, _, e := runSlackRendered(t, wire, true, args...)
			if e == nil || strings.Contains(all, enc) || strings.Contains(all, tok) {
				t.Fatalf("err=%v\n%s", e, all)
			}
		})
	}
}
