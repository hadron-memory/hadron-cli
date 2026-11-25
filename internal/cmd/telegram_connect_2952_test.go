package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2952 connect/disconnect. Fake HTTP only: no Telegram, no network.
// Telegram differs from slack #206 in one shape decision: the connection is
// App-owned, so connect takes NO --org (the organization comes from the App)
// and the recovery pointer is the unfiltered `hadron telegram status`.

const tgBotTok = "7654321:AAH-xqTEST-telegram-secretbot"

const tgAppRef = "hrn:app:acme.com:assistant"

// runTelegramWrite runs telegram <args> against a fake that answers op with
// wire, counts every GraphQL request, and returns stdout, the captured
// variables, the request count and the command's error. stdin, when set, is
// what "-" reads.
func runTelegramWrite(t *testing.T, op, wire, stdin string, args ...string) (string, map[string]json.RawMessage, int, error) {
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
	root.SetArgs(append(append([]string{"telegram"}, args...), "--server", srv.URL))
	e := root.Execute()
	return out.String(), ops, int(atomic.LoadInt32(&n)), e
}

func noTelegramSecret(t *testing.T, where, s string) {
	t.Helper()
	for _, secret := range []string{tgBotTok, "secretbot", "xqTEST", "PRIVATE"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s carries %q:\n%s", where, secret, s)
		}
	}
}

func decodeTelegramVars(t *testing.T, raw json.RawMessage, into *map[string]any) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("no request variables were captured")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("captured variables are not JSON: %v", err)
	}
}

// telegramErrorEnvelope is the failure envelope the root renderer writes in
// --json mode: the document a script actually parses. Assertions read decoded
// fields, never substrings of the rendered bytes — the spacing between keys is
// an encoder detail, not a contract.
type telegramErrorEnvelope struct {
	Error struct {
		Code       int    `json:"code"`
		Message    string `json:"message"`
		ServerCode string `json:"serverCode"`
	} `json:"error"`
}

// decodeTelegramErrorEnvelope decodes the first JSON value of the rendered
// output. The envelope goes to stdout when the command wrote nothing yet,
// else stderr; the telegram render helper returns the streams as the binary
// wrote them, and only the first document is the envelope.
func decodeTelegramErrorEnvelope(t *testing.T, all string) telegramErrorEnvelope {
	t.Helper()
	var env telegramErrorEnvelope
	if err := json.NewDecoder(strings.NewReader(all)).Decode(&env); err != nil {
		t.Fatalf("rendered --json output is not an error envelope: %v\n%s", err, all)
	}
	return env
}

func telegramConnectArgs(t *testing.T) []string {
	t.Helper()
	return []string{"connect", "--app", tgAppRef, "--bot-token-file", tokenFile(t, "bot", tgBotTok)}
}

// runTelegramRendered runs telegram connect through the same error renderer the
// binary uses and returns everything either stream carried, so a leak anywhere
// in the final envelope (message, serverCode, details) is measured.
func runTelegramRendered(t *testing.T, wire string, jsonMode bool, args ...string) (string, int, error) {
	t.Helper()
	return runTelegramRenderedOp(t, "CreateTelegramConnection", wire, jsonMode, args...)
}

func runTelegramRenderedOp(t *testing.T, op, wire string, jsonMode bool, args ...string) (string, int, error) {
	t.Helper()
	var n int32
	srv, _ := captureGraphQLFunc(t, func(o string) string {
		atomic.AddInt32(&n, 1)
		if o == op {
			return wire
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	a := append(append([]string{"telegram"}, args...), "--server", srv.URL)
	if jsonMode {
		a = append(a, "--json")
	}
	root.SetArgs(a)
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	stderr, ok := f.IOStreams.ErrOut.(fmt.Stringer)
	if !ok {
		t.Fatal("stderr cannot be inspected: the helper needs a Stringer stream")
	}
	return out.String() + stderr.String(), int(atomic.LoadInt32(&n)), err
}

func TestTelegramConnectSendsOneRequestWithTheTokenFromAFileAndShowsNoSecret(t *testing.T) {
	bot := tokenFile(t, "bot", tgBotTok+"\n")
	out, ops, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{"data":{"createTelegramConnection":`+telegramRow+`}}`, "",
		"connect", "--app", tgAppRef, "--bot-token-file", bot, "--json")
	if e != nil {
		t.Fatal(e)
	}
	if n != 1 {
		t.Fatalf("one request exactly, got %d", n)
	}
	var vars map[string]any
	decodeTelegramVars(t, ops["CreateTelegramConnection"], &vars)
	if vars["appRef"] != tgAppRef || vars["botToken"] != tgBotTok {
		t.Fatalf("variables %v", vars)
	}
	if len(vars) != 2 {
		t.Fatalf("connect is App-owned: no org variable. %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err, out)
	}
	if len(dto) != 10 || dto["id"] != "t1" || dto["status"] != "ACTIVE" {
		t.Fatalf("the connection shape is telegram get's: %v", dto)
	}
	noTelegramSecret(t, "stdout", out)
	if strings.Contains(out, "lastError") {
		t.Errorf("lastError is never shown: %s", out)
	}
}

func TestTelegramConnectTextOutputAndTokenFromStdin(t *testing.T) {
	out, ops, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{"data":{"createTelegramConnection":`+telegramRow+`}}`, tgBotTok+"\r\n",
		"connect", "--app", appID, "--bot-token-file", "-")
	if e != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, e)
	}
	var vars map[string]any
	decodeTelegramVars(t, ops["CreateTelegramConnection"], &vars)
	if vars["appRef"] != appID || vars["botToken"] != tgBotTok {
		t.Fatalf("stdin must supply exactly the bot token (one trailing CR/LF run dropped): %v", vars)
	}
	if !strings.Contains(out, "✓ connected Telegram bot") || !strings.Contains(out, `"t1"`) {
		t.Fatalf("text output: %s", out)
	}
	noTelegramSecret(t, "stdout", out)
}

// The short <root>:<slug> form canonicalizes to the URN before the request.
func TestTelegramConnectCanonicalizesTheShortAppForm(t *testing.T) {
	bot := tokenFile(t, "bot", tgBotTok)
	_, ops, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{"data":{"createTelegramConnection":`+telegramRow+`}}`, "",
		"connect", "--app", "acme.com:assistant", "--bot-token-file", bot)
	if e != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, e)
	}
	var vars map[string]any
	decodeTelegramVars(t, ops["CreateTelegramConnection"], &vars)
	if vars["appRef"] != tgAppRef {
		t.Fatalf("variables %v", vars)
	}
}

func TestTelegramConnectRefusesLocallyBeforeAnyRequest(t *testing.T) {
	good := tokenFile(t, "tok", tgBotTok)
	empty := tokenFile(t, "empty", "\n")
	cases := map[string][]string{
		"no app":            {"connect", "--bot-token-file", good},
		"blank app":         {"connect", "--app", "  ", "--bot-token-file", good},
		"bare slug app":     {"connect", "--app", "assistant", "--bot-token-file", good},
		"no bot flag":       {"connect", "--app", appID},
		"empty token file":  {"connect", "--app", appID, "--bot-token-file", empty},
		"missing file":      {"connect", "--app", appID, "--bot-token-file", filepath.Join(t.TempDir(), "nope")},
		"token as a flag":   {"connect", "--app", appID, "--bot-token", tgBotTok},
		"org is not a flag": {"connect", "--org", "o1", "--app", appID, "--bot-token-file", good},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{"data":{"createTelegramConnection":`+telegramRow+`}}`, "", args...)
			if e == nil || n != 0 {
				t.Fatalf("must refuse before any request: err=%v requests=%d", e, n)
			}
			if exitCodeFor(e) != exitcode.Usage {
				t.Errorf("exit %d, want usage: %v", exitCodeFor(e), e)
			}
			noTelegramSecret(t, "error", e.Error())
			noTelegramSecret(t, "stdout", out)
		})
	}
}

// A missing --bot-token-file is its own named refusal, not an accident of
// reading an empty stdin (slack #206's F3): with a non-empty pipe the named
// message wins and nothing is read or sent.
func TestTelegramConnectMissingTokenFlagIsRefusedByNameEvenWithPipedStdin(t *testing.T) {
	_, _, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{}`, tgBotTok+"\n", "connect", "--app", appID)
	if e == nil || n != 0 || !strings.Contains(e.Error(), "--bot-token-file is required") {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	noTelegramSecret(t, "error", e.Error())
}

// "-" on a terminal would have the token typed in the clear: refused, nothing sent.
func TestTelegramConnectRefusesStdinFromATerminal(t *testing.T) {
	var n int32
	srv, _ := captureGraphQLFunc(t, func(string) string { atomic.AddInt32(&n, 1); return "" })
	f, _, _ := testFactoryTTY(t, tgBotTok+"\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"telegram", "connect", "--app", appID, "--bot-token-file", "-", "--server", srv.URL})
	e := root.Execute()
	if e == nil || atomic.LoadInt32(&n) != 0 || !strings.Contains(e.Error(), "which is a terminal") {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	noTelegramSecret(t, "error", e.Error())
}

// The pass-through is pinned end to end (Joran's P2): every documented refusal
// renders the server's own message as received, in BOTH renderers, and a
// TELEGRAM_TOOL_* answer additionally carries its telegramErrorCode and its
// serverCode in the --json envelope.
// telegramToolNotConfigured is the wire form of the server's definitive
// "the Telegram tool is not set up" refusal: a TELEGRAM_TOOL_* code with the
// Telegram-side error code on its own extension.
const telegramToolNotConfigured = `{"errors":[{"message":"no token","extensions":{"code":"TELEGRAM_TOOL_NOT_CONFIGURED","telegramErrorCode":"not_configured"}}],"data":null}`

func TestTelegramConnectPassesServerRefusalsThrough(t *testing.T) {
	bot := tokenFile(t, "bot", tgBotTok)
	args := []string{"connect", "--app", appID, "--bot-token-file", bot}
	for name, c := range map[string]struct {
		wire    string
		want    int
		message string
		extra   []string // asserted in the rendered output (both modes)
	}{
		// The ONE Forbidden: not an admin, unknown or ambiguous App, a user-owned
		// App — rendered as received, never parsed into a guess.
		"not an org admin":      {`{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}],"data":null}`, exitcode.Forbidden, "not allowed", nil},
		"already has one":       {`{"errors":[{"message":"This App already has a Telegram connection.","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`, exitcode.Usage, "This App already has a Telegram connection.", nil},
		"bot already connected": {`{"errors":[{"message":"This bot is already connected.","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`, exitcode.Usage, "This bot is already connected.", nil},
		"tool not configured":   {telegramToolNotConfigured, exitcode.Error, "no token", []string{"not_configured", "connect was refused"}},
	} {
		for _, jsonMode := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%v", name, jsonMode), func(t *testing.T) {
				all, n, e := runTelegramRendered(t, c.wire, jsonMode, args...)
				if e == nil || n != 1 {
					t.Fatalf("one request, then the refusal: err=%v requests=%d", e, n)
				}
				if exitCodeFor(e) != c.want {
					t.Errorf("exit %d, want %d: %v", exitCodeFor(e), c.want, e)
				}
				// The server's message reaches the operator unchanged.
				if !strings.Contains(all, c.message) {
					t.Errorf("the server's message must render as received, missing %q:\n%s", c.message, all)
				}
				for _, want := range c.extra {
					if !strings.Contains(all, want) {
						t.Errorf("missing %q:\n%s", want, all)
					}
				}
				noTelegramSecret(t, "the rendered output", all)
			})
		}
	}
	// A TELEGRAM_TOOL_* refusal is definitive: it is never reworded as an
	// unknown outcome and points at nothing (there is no registration to look
	// for), and the --json envelope names the server code a script matches on.
	t.Run("definitive refusal rides the --json envelope's serverCode", func(t *testing.T) {
		all, _, e := runTelegramRendered(t, telegramToolNotConfigured, true, args...)
		if e == nil {
			t.Fatal("the refusal must fail")
		}
		if env := decodeTelegramErrorEnvelope(t, all); env.Error.ServerCode != "TELEGRAM_TOOL_NOT_CONFIGURED" {
			t.Fatalf("the serverCode must ride the envelope, got %q:\n%s", env.Error.ServerCode, all)
		}
	})
	t.Run("a definitive refusal is not an unknown outcome in text mode", func(t *testing.T) {
		all, _, e := runTelegramRendered(t, telegramToolNotConfigured, false, args...)
		if e == nil {
			t.Fatal("the refusal must fail")
		}
		if strings.Contains(all, "outcome unknown") || strings.Contains(all, "hadron telegram status") {
			t.Fatalf("a definitive refusal is not an unknown outcome and needs no pointer:\n%s", all)
		}
	})
	// The invalid-token spelling a caller actually meets (the agentic line's
	// example): the Telegram error code is surfaced, not swallowed.
	t.Run("invalid_token surfaces next to the server's message", func(t *testing.T) {
		all, _, e := runTelegramRendered(t, `{"errors":[{"message":"Telegram refused the token","extensions":{"code":"TELEGRAM_TOOL_ERROR","telegramErrorCode":"invalid_token"}}],"data":null}`, false, args...)
		if e == nil {
			t.Fatal("the refusal must fail")
		}
		for _, want := range []string{"invalid_token", "Telegram refused the token", "connect was refused"} {
			if !strings.Contains(all, want) {
				t.Errorf("missing %q:\n%s", want, all)
			}
		}
	})
	// The Telegram error code is read as a STRING extension or not at all: a
	// number is ignored (ErrorCodeExtension's type assertion fails closed), so
	// the refusal stays definitive with no code surfaced next to it.
	t.Run("a non-string telegramErrorCode is ignored", func(t *testing.T) {
		all, _, e := runTelegramRendered(t, `{"errors":[{"message":"no token","extensions":{"code":"TELEGRAM_TOOL_NOT_CONFIGURED","telegramErrorCode":401}}],"data":null}`, false, args...)
		if e == nil {
			t.Fatal("the refusal must fail")
		}
		if !strings.Contains(all, "connect was refused") || strings.Contains(all, "Telegram error code") {
			t.Fatalf("a non-string code is ignored, the refusal itself stays definitive:\n%s", all)
		}
	})
	// An error BELOW createTelegramConnection happened after the write may
	// have committed (cli#398): however well coded, it is not proof the
	// server refused, so the unknown-outcome notice and the status pointer
	// stay. Together with the 5xx case
	// (TestTelegramConnectToolRefusalOnA5xxIsNotDefinitive) this pins both
	// conditions telegramToolRefusedAt adds to the TELEGRAM_TOOL_ prefix.
	t.Run("a below-field error is not definitive even with a TELEGRAM_TOOL code", func(t *testing.T) {
		wire := `{"errors":[{"message":"late failure","extensions":{"code":"TELEGRAM_TOOL_ERROR"},"path":["createTelegramConnection","bot"]}],"data":null}`
		all, _, e := runTelegramRendered(t, wire, false, args...)
		if e == nil {
			t.Fatal("must fail")
		}
		if !strings.Contains(all, "outcome unknown") || !strings.Contains(all, "hadron telegram status") {
			t.Fatalf("a below-field error keeps the unknown outcome and the pointer:\n%s", all)
		}
		if strings.Contains(all, "connect was refused") {
			t.Fatalf("a post-commit error is not a refusal:\n%s", all)
		}
	})
}

// The bot-token-file flag is trimmed before it names a file (the fold's own
// message says so): a value padded with blanks still reads the token and
// sends exactly one request.
func TestTelegramConnectTrimsTheTokenFileFlag(t *testing.T) {
	bot := tokenFile(t, "bot", tgBotTok+"\n")
	out, ops, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{"data":{"createTelegramConnection":`+telegramRow+`}}`, "",
		"connect", "--app", tgAppRef, "--bot-token-file", " "+bot+" ", "--json")
	if e != nil {
		t.Fatal(e)
	}
	if n != 1 {
		t.Fatalf("one request, got %d", n)
	}
	var vars map[string]any
	decodeTelegramVars(t, ops["CreateTelegramConnection"], &vars)
	if vars["botToken"] != tgBotTok {
		t.Fatalf("the token the server received: %v", vars)
	}
	if !strings.Contains(out, `"id": "t1"`) {
		t.Fatalf("connected: %s", out)
	}
	noTelegramSecret(t, "stdout", out)
}

// A 5xx envelope is not proof the server refused (cli#1495): a gateway can
// answer 502 with a GraphQL body whose code says TELEGRAM_TOOL_* after the
// write committed, so telegramToolRefusedAt's FromNon200Envelope guard keeps
// the unknown-outcome wording and the status pointer. Pinned end to end in
// both renderers: the definitive-refusal branch ("connect was refused") is
// only ever earned by a 200 answer.
func TestTelegramConnectToolRefusalOnA5xxIsNotDefinitive(t *testing.T) {
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		if r.Method == http.MethodPost {
			atomic.AddInt32(&posts, 1)
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(telegramToolNotConfigured))
	}))
	defer srv.Close()
	bot := tokenFile(t, "bot", tgBotTok)
	for _, jsonMode := range []bool{true, false} {
		t.Run(fmt.Sprintf("json=%v", jsonMode), func(t *testing.T) {
			before := atomic.LoadInt32(&posts)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			a := []string{"telegram", "connect", "--app", appID, "--bot-token-file", bot, "--server", srv.URL}
			if jsonMode {
				a = append(a, "--json")
			}
			root.SetArgs(a)
			e := root.Execute()
			if e == nil {
				t.Fatal("must fail")
			}
			renderError(f, e)
			stderr, ok := f.IOStreams.ErrOut.(fmt.Stringer)
			if !ok {
				t.Fatal("stderr cannot be inspected: the helper needs a Stringer stream")
			}
			all := out.String() + stderr.String()
			if got := atomic.LoadInt32(&posts) - before; got != 1 {
				t.Fatalf("sent once, never retried: requests=%d", got)
			}
			if !strings.Contains(all, "outcome unknown") || !strings.Contains(all, "hadron telegram status") {
				t.Fatalf("a 5xx envelope is an unknown outcome and keeps the pointer:\n%s", all)
			}
			if strings.Contains(all, "connect was refused") {
				t.Fatalf("a gateway answer is not proof the server refused:\n%s", all)
			}
			noTelegramSecret(t, "rendered output", all)
		})
	}
}

func TestTelegramConnectAmbiguousFailureIsSentOnceAndPointsAtStatus(t *testing.T) {
	bot := tokenFile(t, "bot", tgBotTok)
	_, _, n, e := runTelegramWrite(t, "CreateTelegramConnection", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, "",
		"connect", "--app", appID, "--bot-token-file", bot)
	if e == nil || n != 1 {
		t.Fatalf("sent once, never retried: err=%v requests=%d", e, n)
	}
	if !strings.Contains(e.Error(), "outcome unknown") || !strings.Contains(e.Error(), "hadron telegram status") {
		t.Errorf("must say the outcome is unknown and where to look: %v", e)
	}
	// connect takes no --org, so the pointer can never carry one.
	if strings.Contains(e.Error(), "--org") {
		t.Errorf("the pointer is the unfiltered listing: %v", e)
	}
	noTelegramSecret(t, "error", e.Error())
}

func TestTelegramConnectEmptyAnswerIsNotSuccess(t *testing.T) {
	_, _, _, e := runTelegramWrite(t, "CreateTelegramConnection", `{"data":{"createTelegramConnection":null}}`, "", telegramConnectArgs(t)...)
	if e == nil || exitCodeFor(e) != exitcode.Unavailable {
		t.Fatalf("a null answer is Unavailable, never connected: %v", e)
	}
}

// The server's text is untrusted everywhere it can reach the operator: the
// message, extensions.code (printed as serverCode in the --json envelope), and
// every field of a success answer.
func TestTelegramConnectNeverPrintsTheTokenFromAnyServerText(t *testing.T) {
	cases := map[string]string{
		"message only":  `{"errors":[{"message":"invalid_token ` + tgBotTok + `","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`,
		"code only":     `{"errors":[{"message":"refused","extensions":{"code":"` + tgBotTok + `"}}],"data":null}`,
		"message+code":  `{"errors":[{"message":"bad ` + tgBotTok + `","extensions":{"code":"X_` + tgBotTok + `"}}],"data":null}`,
		"ambiguous+msg": `{"errors":[{"message":"boom ` + tgBotTok + `","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`,
	}
	for name, wire := range cases {
		for _, jsonMode := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%v", name, jsonMode), func(t *testing.T) {
				all, n, e := runTelegramRendered(t, wire, jsonMode, telegramConnectArgs(t)...)
				if e == nil || n != 1 {
					t.Fatalf("err=%v requests=%d", e, n)
				}
				noTelegramSecret(t, "the rendered output", all)
			})
		}
	}
}

// A refusal whose message echoes the token keeps its class, its (clean) server
// code, and, for an ambiguous failure, the unknown-outcome notice.
func TestTelegramConnectScrubKeepsClassCodeAndRecoveryGuidance(t *testing.T) {
	all, _, e := runTelegramRendered(t, `{"errors":[{"message":"bad `+tgBotTok+`","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`, true, telegramConnectArgs(t)...)
	if exitCodeFor(e) != exitcode.Usage || !strings.Contains(all, `"serverCode": "BAD_USER_INPUT"`) && !strings.Contains(all, `"serverCode":"BAD_USER_INPUT"`) {
		t.Errorf("class and clean serverCode must survive the scrub: exit %d\n%s", exitCodeFor(e), all)
	}
	noTelegramSecret(t, "the rendered output", all)
	all, _, e = runTelegramRendered(t, `{"errors":[{"message":"boom `+tgBotTok+`","extensions":{"code":"SOMETHING_UNMAPPED"}}],"data":null}`, false, telegramConnectArgs(t)...)
	if e == nil {
		t.Fatal("must fail")
	}
	for _, want := range []string{"outcome unknown", "not retried", "hadron telegram status", "--offset", redactedPlaceholder} {
		if !strings.Contains(all, want) {
			t.Errorf("an echoed token must not cost the recovery guidance, missing %q:\n%s", want, all)
		}
	}
	noTelegramSecret(t, "the rendered output", all)
}

// The success answer is the server's text too: a field repeating the token is
// never rendered (text or JSON), and not even the id is quoted back.
func TestTelegramConnectWithholdsASuccessAnswerThatRepeatsTheToken(t *testing.T) {
	for _, field := range []string{"id", "organizationId", "appId", "provider", "status", "displayName", "botId", "botUsername", "createdAt", "updatedAt"} {
		var row map[string]any
		_ = json.Unmarshal([]byte(telegramRow), &row)
		row[field] = tgBotTok
		b, _ := json.Marshal(row)
		for _, jsonMode := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%v", field, jsonMode), func(t *testing.T) {
				all, n, e := runTelegramRendered(t, `{"data":{"createTelegramConnection":`+string(b)+`}}`, jsonMode, telegramConnectArgs(t)...)
				if e == nil || n != 1 || exitCodeFor(e) != exitcode.Unavailable {
					t.Fatalf("a repeated token is Unavailable, withheld: err=%v requests=%d", e, n)
				}
				noTelegramSecret(t, "the rendered output", all)
				if !strings.Contains(all, "hadron telegram status") {
					t.Errorf("must say where to look: %s", all)
				}
			})
		}
	}
}

// Success needs a usable id; an ERROR row is a recorded failure, not
// "connected"; an unknown status names the one command that cannot loop.
func TestTelegramConnectValidatesTheSuccessAnswer(t *testing.T) {
	_, _, e := runTelegramRendered(t, `{"data":{"createTelegramConnection":`+strings.Replace(telegramRow, `"id":"t1"`, `"id":""`, 1)+`}}`, false, telegramConnectArgs(t)...)
	if e == nil || exitCodeFor(e) != exitcode.Unavailable || !strings.Contains(e.Error(), "carried no connection id") {
		t.Fatalf("an empty id is unconfirmed, never success: %v", e)
	}
	_, _, e = runTelegramRendered(t, `{"data":{"createTelegramConnection":{}}}`, false, telegramConnectArgs(t)...)
	if e == nil || exitCodeFor(e) != exitcode.Unavailable {
		t.Fatalf("an empty object is unconfirmed: %v", e)
	}
	_, _, e = runTelegramRendered(t, `{"data":{"createTelegramConnection":`+strings.Replace(telegramRow, `"ACTIVE"`, `"WAT"`, 1)+`}}`, false, telegramConnectArgs(t)...)
	if e == nil || exitCodeFor(e) != exitcode.Unavailable || !strings.Contains(e.Error(), "hadron telegram disconnect -- t1") || strings.Contains(e.Error(), "hadron telegram get") {
		t.Fatalf("an unknown status is unconfirmed and ends in the one command that cannot loop (disconnect): %v", e)
	}
	all, _, e := runTelegramRendered(t, `{"data":{"createTelegramConnection":`+strings.Replace(telegramRow, `"ACTIVE"`, `"ERROR"`, 1)+`}}`, false, telegramConnectArgs(t)...)
	if e != nil || strings.Contains(all, "✓ connected") || !strings.Contains(all, "recorded a failure") || !strings.Contains(all, "status ERROR") {
		t.Fatalf("ERROR is not '✓ connected': err=%v\n%s", e, all)
	}
	all, _, e = runTelegramRendered(t, `{"data":{"createTelegramConnection":`+telegramRow+`}}`, false, telegramConnectArgs(t)...)
	if e != nil || !strings.Contains(all, "✓ connected Telegram bot") {
		t.Fatalf("ACTIVE keeps the success banner: err=%v\n%s", e, all)
	}
}

// A suggested command is pasted into a shell, so a server-supplied value in it
// is shell-quoted, and cobra's leading-dash rule needs "--" before the id.
func TestTelegramConnectSuggestedCommandsAreShellSafe(t *testing.T) {
	row := strings.Replace(strings.Replace(telegramRow, `"id":"t1"`, `"id":"t 1$(x)"`, 1), `"ACTIVE"`, `"WAT"`, 1)
	_, _, e := runTelegramRendered(t, `{"data":{"createTelegramConnection":`+row+`}}`, false, telegramConnectArgs(t)...)
	if e == nil || !strings.Contains(e.Error(), "hadron telegram disconnect -- 't 1$(x)'") || strings.Contains(e.Error(), "telegram get") || strings.Contains(e.Error(), "telegram status") {
		t.Fatalf("unknown status ends in one quoted disconnect, no looping get/status: %v", e)
	}
	row = strings.Replace(strings.Replace(telegramRow, `"id":"t1"`, `"id":"-x"`, 1), `"ACTIVE"`, `"WAT"`, 1)
	_, _, e = runTelegramRendered(t, `{"data":{"createTelegramConnection":`+row+`}}`, false, telegramConnectArgs(t)...)
	if e == nil || !strings.Contains(e.Error(), "hadron telegram disconnect -- -x") {
		t.Fatalf("the suggested command must carry the -- terminator: %v", e)
	}
}

// A control-character id is never named in a suggested command (the terminal
// would act on it); the fallback names no command at all.
func TestTelegramConnectNeverPrintsAControlCharacterID(t *testing.T) {
	id := "a\x1b[31mb\nc"
	jid, _ := json.Marshal(id)
	row := strings.Replace(strings.Replace(telegramRow, `"id":"t1"`, `"id":`+string(jid), 1), `"ACTIVE"`, `"WAT"`, 1)
	all, _, e := runTelegramRendered(t, `{"data":{"createTelegramConnection":`+row+`}}`, false, telegramConnectArgs(t)...)
	// The RAW error, not only the rendered stream: the renderer sanitizes its
	// own output, so this is what pins the guard itself.
	if e == nil || strings.ContainsAny(e.Error(), "\x1b\n") || strings.Contains(e.Error(), "hadron telegram") || !strings.Contains(e.Error(), "ask the server's administrator") {
		t.Fatalf("a control-character id is not printed and no command is suggested: %v\n%s", e, all)
	}
}

// An echoing gateway can render a token percent-encoded; the shared redactor
// covers those spellings in the message, the code and a success field.
func TestTelegramConnectRedactsAPercentEncodedEcho(t *testing.T) {
	tok := "7654321:AAH-a/b+c=d-secretbot"
	enc := url.QueryEscape(tok)
	bot := tokenFile(t, "bot", tok)
	args := []string{"connect", "--app", appID, "--bot-token-file", bot}
	for name, wire := range map[string]string{
		"message": `{"errors":[{"message":"bad ` + enc + `","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`,
		"code":    `{"errors":[{"message":"refused","extensions":{"code":"` + enc + `"}}],"data":null}`,
		"success": `{"data":{"createTelegramConnection":` + strings.Replace(telegramRow, `"displayName":"Acme\u001b[31m"`, `"displayName":"`+enc+`"`, 1) + `}}`,
	} {
		t.Run(name, func(t *testing.T) {
			all, _, e := runTelegramRendered(t, wire, true, args...)
			if e == nil || strings.Contains(all, enc) || strings.Contains(all, tok) {
				t.Fatalf("err=%v\n%s", e, all)
			}
		})
	}
}

// A suggested command is the LAST thing in its message, alone on the final
// rendered line, with the server's text before it.
func TestTelegramConnectRecoveryCommandIsLastEvenWithAServerError(t *testing.T) {
	for name, wire := range map[string]string{
		"null answer": `{"data":{"createTelegramConnection":null}}`,
		"ambiguous":   `{"errors":[{"message":"upstream boom","extensions":{"code":"SOMETHING_UNMAPPED"}}],"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			all, _, e := runTelegramRendered(t, wire, false, telegramConnectArgs(t)...)
			if e == nil || !strings.HasSuffix(e.Error(), "Check with:\n  hadron telegram status") {
				t.Fatalf("the command must end the message:\n%v", e)
			}
			if name == "ambiguous" {
				boom, check := strings.Index(e.Error(), "upstream boom"), strings.Index(e.Error(), "Check with:")
				if boom < 0 || check < 0 || boom > check {
					t.Errorf("the server's text must be present and come before the command: %v", e)
				}
			}
			lines := strings.Split(strings.TrimRight(all, "\n"), "\n")
			if last := strings.TrimSpace(lines[len(lines)-1]); last != "hadron telegram status" {
				t.Errorf("the final rendered line must be exactly the command, got %q\n%s", last, all)
			}
		})
	}
}

func TestTelegramConnectHelpNamesNoTokenValue(t *testing.T) {
	h := cmdHelp(t, "telegram", "connect")
	for _, want := range []string{"--bot-token-file", "never retried", "hadron telegram status", "outcome is unknown"} {
		if !strings.Contains(h, want) {
			t.Errorf("connect help missing %q: %s", want, h)
		}
	}
	if strings.Contains(h, "AAH-") || strings.Contains(h, "--bot-token ") {
		t.Errorf("connect help must not offer a token as a flag value: %s", h)
	}
}

func TestTelegramDisconnectNeedsYesWithoutATerminalAndSendsNothing(t *testing.T) {
	_, _, n, e := runTelegramWrite(t, "DeleteTelegramConnection", `{"data":{"deleteTelegramConnection":true}}`, "", "disconnect", "t1")
	if e == nil || n != 0 || exitCodeFor(e) != exitcode.Usage {
		t.Fatalf("zero requests without --yes: err=%v requests=%d", e, n)
	}
}

func TestTelegramDisconnectSendsOneRequestWithTheExactRef(t *testing.T) {
	out, ops, n, e := runTelegramWrite(t, "DeleteTelegramConnection", `{"data":{"deleteTelegramConnection":true}}`, "", "disconnect", " t1 ", "--yes", "--json")
	if e != nil || n != 1 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	var vars map[string]any
	decodeTelegramVars(t, ops["DeleteTelegramConnection"], &vars)
	if vars["ref"] != "t1" {
		t.Fatalf("variables %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["connectionId"] != "t1" || dto["disconnected"] != true || len(dto) != 2 {
		t.Fatalf("json %s (%v)", out, err)
	}
	// Text mode says the server confirmed it.
	out, _, _, e = runTelegramWrite(t, "DeleteTelegramConnection", `{"data":{"deleteTelegramConnection":true}}`, "", "disconnect", "t1", "--yes")
	if e != nil || !strings.Contains(out, "disconnected Telegram connection") || !strings.Contains(out, "server confirmed") {
		t.Fatalf("text success line: err=%v\n%s", e, out)
	}
}

func TestTelegramDisconnectPassesNotFoundThroughAndNeverPreReads(t *testing.T) {
	_, _, n, e := runTelegramWrite(t, "DeleteTelegramConnection", `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}],"data":null}`, "", "disconnect", "t1", "--yes")
	if e == nil || n != 1 || exitCodeFor(e) != exitcode.NotFound {
		t.Fatalf("one mutation, no existence read, NOT_FOUND passed through: err=%v requests=%d", e, n)
	}
	// Juniper's surviving mutant: the refusal must keep its server code
	// (MapErrorKeepingCode), so a script can tell NOT_FOUND from any other
	// exit-4 behind the same number — pinned as a decoded envelope field.
	all, _, e := runTelegramRenderedOp(t, "DeleteTelegramConnection", `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}],"data":null}`, true, "disconnect", "t1", "--yes")
	if e == nil {
		t.Fatal("the refusal must fail")
	}
	if env := decodeTelegramErrorEnvelope(t, all); env.Error.ServerCode != "NOT_FOUND" {
		t.Fatalf("disconnect refusals carry their serverCode in --json, got %q:\n%s", env.Error.ServerCode, all)
	}
}

// The server's error text is rendered, never executed and never swallowed:
// control characters in a refusal message reach the terminal only after the
// renderer neutralises them, and the words themselves still arrive — a refusal
// dropped on the floor would pass a control-character check vacuously. The
// text-mode scrub lives at the ROOT renderer (output.SafeError): a terminal
// escape sequence is removed whole (its parameters are not left as junk) and a
// lone control character becomes a space. In --json the encoder writes the C0
// controls as \u escapes, so the raw document cannot act on the terminal while
// a decoding program still receives the server's bytes exactly (#1131).
func TestTelegramDisconnectScrubsControlCharactersFromServerText(t *testing.T) {
	const serverText = "boom\x1b[31m red\rinjected-line"
	wire := `{"errors":[{"message":"boom\u001b[31m red\u000dinjected-line","extensions":{"code":"SOMETHING_UNMAPPED"}}],"data":null}`
	for _, jsonMode := range []bool{true, false} {
		t.Run(fmt.Sprintf("json=%v", jsonMode), func(t *testing.T) {
			all, _, e := runTelegramRenderedOp(t, "DeleteTelegramConnection", wire, jsonMode, "disconnect", "t1", "--yes")
			if e == nil {
				t.Fatal("must fail")
			}
			// The message must survive its neutralisation, not be dropped with
			// the controls.
			if !strings.Contains(all, "boom") || !strings.Contains(all, "injected-line") {
				t.Fatalf("the server's message text must still be rendered:\n%q", all)
			}
			if strings.ContainsAny(all, "\x1b\r") {
				t.Fatalf("no raw control character may reach the terminal:\n%q", all)
			}
			if jsonMode {
				env := decodeTelegramErrorEnvelope(t, all)
				if !strings.Contains(env.Error.Message, serverText) {
					t.Fatalf("--json carries the message as the server sent it (the encoder's escapes make it inert), got %q", env.Error.Message)
				}
				if env.Error.ServerCode != "SOMETHING_UNMAPPED" {
					t.Fatalf("an unmapped code still rides the envelope as serverCode, got %q", env.Error.ServerCode)
				}
			} else if !strings.Contains(all, "boom red injected-line") {
				t.Fatalf("text mode keeps every word and drops the controls:\n%q", all)
			}
		})
	}
}

func TestTelegramDisconnectFalseAnswerIsNotSuccessAndAmbiguousIsUnknown(t *testing.T) {
	_, _, _, e := runTelegramWrite(t, "DeleteTelegramConnection", `{"data":{"deleteTelegramConnection":false}}`, "", "disconnect", "t1", "--yes")
	if e == nil || exitCodeFor(e) != exitcode.Unavailable || !strings.Contains(e.Error(), "did not confirm") {
		t.Fatalf("false is Unavailable: %v", e)
	}
	_, _, n, e := runTelegramWrite(t, "DeleteTelegramConnection", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, "", "disconnect", "t1", "--yes")
	if e == nil || n != 1 || !strings.Contains(e.Error(), "outcome unknown") {
		t.Fatalf("sent once, outcome unknown: err=%v requests=%d", e, n)
	}
}

func TestTelegramDisconnectAsksOnATerminal(t *testing.T) {
	for answer, wantRequests := range map[string]int{"n\n": 0, "y\n": 1} {
		var n int32
		srv, _ := captureGraphQLFunc(t, func(op string) string {
			atomic.AddInt32(&n, 1)
			return `{"data":{"deleteTelegramConnection":true}}`
		})
		f, _, errOut := testFactoryTTY(t, answer)
		root := NewRootCmd(f)
		root.SetArgs([]string{"telegram", "disconnect", "t1", "--server", srv.URL})
		err := root.Execute()
		if answer == "y\n" && err != nil {
			t.Errorf("accepting must succeed: %v", err)
		}
		if answer == "n\n" && (err == nil || exitCodeFor(err) != exitcode.Cancelled) {
			t.Errorf("declining is a cancellation, not %v (exit %d)", err, exitCodeFor(err))
		}
		if int(atomic.LoadInt32(&n)) != wantRequests {
			t.Errorf("answer %q: %d requests, want %d", answer, n, wantRequests)
		}
		if p := errOut.String(); !strings.Contains(p, "Disconnect Telegram connection") || strings.Contains(p, "cannot be undone") || strings.Contains(p, "restore") ||
			!strings.Contains(p, "best effort") || !strings.Contains(p, "registers it anew") {
			t.Errorf("the prompt names a soft delete with best-effort teardown, no restore promise: %q", p)
		}
	}
}

// The disconnect outcome-unknown message never repeats the id (cli#3114): it ends in
// the paged listing, alone on the last line, never an angle-bracket placeholder.
func TestTelegramDisconnectSuggestionsEndWithTheCommandAloneOnTheLastLine(t *testing.T) {
	_, _, n, e := runTelegramWrite(t, "DeleteTelegramConnection", `{"errors":[{"message":"boom","extensions":{"code":"SOMETHING_UNMAPPED"}}],"data":null}`, "", "disconnect", "--yes", "a b$(x)")
	if e == nil || n != 1 || !strings.HasSuffix(e.Error(), "by listing connections with:\n  hadron telegram status") || strings.Contains(e.Error(), "a b$(x)") {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	for name, id := range map[string]string{
		"escape":   "a\x1bb",
		"newline":  "x\nrm -rf injected-line",
		"carriage": "x\rrm -rf injected-line",
		"format":   "x\u202ejected-line",
		"line sep": "x\u2028injected-line",
		"para sep": "x\u2029injected-line",
	} {
		t.Run(name, func(t *testing.T) {
			all, _, e := runTelegramRenderedOp(t, "DeleteTelegramConnection", `{"data":{"deleteTelegramConnection":false}}`, false, "disconnect", "--yes", id)
			if e == nil {
				t.Fatal("must fail")
			}
			lines := strings.Split(strings.TrimRight(all, "\n"), "\n")
			last := strings.TrimSpace(lines[len(lines)-1])
			if last != "hadron telegram status" {
				t.Errorf("rendered last line %q, want the unfiltered listing\n%s", last, all)
			}
			if !strings.Contains(all, "follow its --offset hint") {
				t.Errorf("the listing fallback must say it is paged:\n%s", all)
			}
			if strings.Contains(all, "injected-line") || strings.ContainsAny(all, "\x1b\r\u202e\u2028\u2029") {
				t.Errorf("nothing from the id may reach the output:\n%q", all)
			}
		})
	}
}

// The command as printed really works with a dash-prefixed id (--server goes
// before the terminator: everything after -- is positional).
func TestTelegramSuggestedIDCommandsSurviveADashPrefixedID(t *testing.T) {
	for _, deleted := range []string{"true", "false"} {
		var n int32
		srv, ops := captureGraphQLFunc(t, func(string) string {
			atomic.AddInt32(&n, 1)
			return `{"data":{"deleteTelegramConnection":` + deleted + `}}`
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"telegram", "disconnect", "--yes", "--server", srv.URL, "--", "-x"})
		e := root.Execute()
		if atomic.LoadInt32(&n) != 1 {
			t.Fatalf("a dash-prefixed id after -- must be sent once: requests=%d err=%v", n, e)
		}
		var vars map[string]any
		decodeTelegramVars(t, ops["DeleteTelegramConnection"], &vars)
		if vars["ref"] != "-x" {
			t.Fatalf("variables %v", vars)
		}
		if deleted == "true" && e != nil {
			t.Fatalf("must succeed: %v", e)
		}
		if deleted == "false" && (e == nil || !strings.HasSuffix(e.Error(), "hadron telegram status")) {
			t.Fatalf("disconnect's pointer is the runnable listing: %v", e)
		}
	}
}

// The group page names what ACTIVE does and does not mean, and the recovery
// order (disconnect first, then connect again).
func TestTelegramGroupHelpTellsTheTruthAboutStatus(t *testing.T) {
	h := cmdHelp(t, "telegram")
	for _, want := range []string{"status", "get", "connect", "disconnect", "does not prove", "registers the bot again"} {
		if !strings.Contains(h, want) {
			t.Errorf("group help missing %q: %s", want, h)
		}
	}
	if strings.Contains(h, "health check") || strings.Contains(h, "healthy") {
		t.Errorf("the group page must not promise health: %s", h)
	}
}
