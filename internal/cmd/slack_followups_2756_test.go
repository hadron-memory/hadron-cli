package cmd

// cli#2756: follow-ups from the #2731 review rounds. Pins for the once-trimmed
// --org/--app, the malformed --app refusal, the short --app form, disconnect's
// text-mode success line, and the Zl/Zp line/paragraph separators the control
// character guard must refuse (pinned beside the U+202E case in the #206 file).
// The #2827 rounds added the hostile-status refusal (a status that starts with ACTIVE
// and carries one refused rune of every guarded class, quoted whole, exit code pinned) and
// the happy-path status table whose whole row renders quoted. Fake HTTP only.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// P3-2: both reference flags are trimmed once and the trimmed value is what is
// sent — the wire never carries the padding, and a padded short --app still
// canonicalizes.
func TestSlackConnectTrimsOrgAndAppOnce(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	for name, c := range map[string]struct{ org, app, wantOrg, wantApp string }{
		"padded urn and slug form": {" acme.com ", " acme.com:assistant ", "acme.com", "hrn:app:acme.com:assistant"},
		"padded ids":               {" o1 ", " " + appID + " ", "o1", appID},
	} {
		t.Run(name, func(t *testing.T) {
			_, ops, n, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":`+connectedRow+`}}`, "",
				"connect", "--org", c.org, "--app", c.app, "--bot-token-file", bot, "--app-token-file", app)
			if e != nil || n != 1 {
				t.Fatalf("err=%v requests=%d", e, n)
			}
			var vars map[string]any
			decodeSlackVars(t, ops["CreateSlackConnection"], &vars)
			if vars["orgRef"] != c.wantOrg || vars["appRef"] != c.wantApp {
				t.Fatalf("the trimmed values must be what is sent: %v", vars)
			}
		})
	}
	// A blank --app is the command's own named refusal, not CanonicalAppRef's
	// (the trim happens once, before the empty check).
	_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{}`, "", "connect", "--org", "o1", "--app", "  ",
		"--bot-token-file", bot, "--app-token-file", app)
	if e == nil || n != 0 || exitCodeFor(e) != exitcode.Usage || !strings.Contains(e.Error(), "--app is required") {
		t.Fatalf("a blank --app is the command's own refusal: err=%v requests=%d", e, n)
	}
}

// P3-7c: the <root>:<slug> short --app form end to end (canonicalized on the wire).
func TestSlackConnectAcceptsTheShortAppForm(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	_, ops, n, e := runSlackWrite(t, "CreateSlackConnection", `{"data":{"createSlackConnection":`+connectedRow+`}}`, "",
		"connect", "--org", "acme.com", "--app", "acme.com:assistant", "--bot-token-file", bot, "--app-token-file", app)
	if e != nil || n != 1 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	var vars map[string]any
	decodeSlackVars(t, ops["CreateSlackConnection"], &vars)
	if vars["appRef"] != "hrn:app:acme.com:assistant" {
		t.Fatalf("the short form canonicalizes to the URN: %v", vars)
	}
}

// P3-7c: a malformed --app (a bare slug reads as "I forgot the org") is refused
// by CanonicalAppRef before any request is sent.
func TestSlackConnectRefusesAMalformedAppWithZeroRequests(t *testing.T) {
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{}`, "",
		"connect", "--org", "acme.com", "--app", "assistant", "--bot-token-file", bot, "--app-token-file", app)
	if e == nil || n != 0 || exitCodeFor(e) != exitcode.Usage || !strings.Contains(e.Error(), "not org-qualified") {
		t.Fatalf("a malformed --app refuses with zero requests: err=%v requests=%d", e, n)
	}
	noSecret(t, "error", e.Error())
}

// P3-7c: disconnect's text-mode success line (the #206 file pinned only --json).
func TestSlackDisconnectTextModeSuccessLine(t *testing.T) {
	out, _, n, e := runSlackWrite(t, "DeleteSlackConnection", `{"data":{"deleteSlackConnection":true}}`, "", "disconnect", "c1", "--yes")
	if e != nil || n != 1 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	if !strings.Contains(out, `disconnected Slack connection "c1" (server confirmed)`) {
		t.Fatalf("the text success line: %s", out)
	}
}

// #2827: a server-supplied status is untrusted. The status command refuses anything but
// ACTIVE/ERROR before its table (toDTO), and the refusal quotes the value (strconv.Quote),
// so hostile Zl/Zp/Cc/Cf runes can neither forge the refusal message nor the table row; the
// table's status cell is additionally wrapped like its siblings (SafeCell) should the
// allowlist ever widen. This pin holds the reachable guard: nothing raw reaches the
// terminal (the error text, and the line the real renderer prints), and the refusal quotes
// the WHOLE value it names, so a refusal that merely listed the allowed values would fail.
//
// The status starts with ACTIVE on purpose: a check that matches by prefix, or trims at the
// first separator, would accept it; only the exact allowlist refuses it.
func TestSlackStatusRefusesAHostileStatusSafely(t *testing.T) {
	// One refused rune of every guarded class inside the server-supplied status: a Zl and a
	// Zp separator, a CR and an ESC (Cc) and an RLO override (Cf). The Go escapes keep this
	// file pure ASCII; the same runes appear in the raw-leak check, so no arm of that check
	// is vacuous.
	const value = "ACTIVE\u2028b\u2029c\rd\x1be\u202ef"
	quoted, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	row := `{"id":"c1","organizationId":"o1","defaultAppId":"a1","provider":"slack","status":` + string(quoted) + `,"teamId":"T1","teamName":"Acme","botId":"B1","botUserId":"U1","createdAt":"c","updatedAt":"u"}`
	out, ops, n, e := runSlackWrite(t, "SlackConnections", `{"data":{"slackConnections":{"total":1,"items":[`+row+`]}}}`, "", "status")
	if e == nil || n != 1 {
		t.Fatalf("the hostile status is refused after the one read: err=%v requests=%d", e, n)
	}
	if len(ops) != 1 || ops["SlackConnections"] == nil {
		t.Fatalf("the one request is the connections read: %v", ops)
	}
	// The refusal is Unavailable (a row this CLI will not render).
	if exitCodeFor(e) != exitcode.Unavailable {
		t.Fatalf("the hostile status refuses Unavailable, got exit %d: %v", exitCodeFor(e), e)
	}
	// Nothing hostile reaches the terminal raw, in the rendered output or the error: every
	// carried rune (separators, override, CR, ESC) appears only as its quoted escape.
	if strings.ContainsAny(out+"\n"+e.Error(), "\x1b\r\u202e\u2028\u2029") {
		t.Fatalf("a hostile status must not reach the terminal raw: out=%q err=%q", out, e.Error())
	}
	// The refusal quotes the whole value, not just a prefix of it.
	if want := "status " + strconv.Quote(value); !strings.Contains(e.Error(), want) {
		t.Fatalf("the refusal must quote the whole hostile status %s:\n%q", want, e.Error())
	}
	// The rendered form (the line the real renderer prints) is inert and keeps the quoted value.
	rendered := renderPlain2739(t, e)
	if r, bad := errTextHasControl2739(rendered); bad {
		t.Fatalf("the rendered refusal carries %U: %q", r, rendered)
	}
	if !strings.Contains(rendered, strconv.Quote(value)) {
		t.Fatalf("the rendered refusal keeps the quoted value %s: %q", strconv.Quote(value), rendered)
	}
}

// The happy path reaches the table. Every cell of the status table renders quoted (the
// table's own idiom), and the status cell is wrapped like its siblings, so this pin makes the
// wrap reachable: reverting the status cell to a raw render goes red here. The whole row is
// asserted, in column order, so a value cannot pass by appearing in the wrong cell or only in
// the header.
func TestSlackStatusTableQuotesEveryCell(t *testing.T) {
	row := `{"id":"c1","organizationId":"o1","defaultAppId":"a1","provider":"slack","status":"ACTIVE","teamId":"T1","teamName":"Acme","botId":"B1","botUserId":"U1","createdAt":"c","updatedAt":"u"}`
	out, _, n, e := runSlackWrite(t, "SlackConnections", `{"data":{"slackConnections":{"total":1,"items":[`+row+`]}}}`, "", "status")
	if e != nil || n != 1 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("one header and one row expected:\n%s", out)
	}
	if got, want := strings.Fields(lines[1]), []string{`"c1"`, `"o1"`, `"a1"`, `"ACTIVE"`, `"T1"`, `"Acme"`, `"B1"`, `"U1"`, `"u"`}; !slices.Equal(got, want) {
		t.Fatalf("every cell of the row renders quoted, in column order:\n got  %q\n want %q\n%s", got, want, out)
	}
}

// cli#2876: a token pasted into --app (beside --bot-token-file, an easy slip) is
// refused before any request, and neither the text error nor the --json envelope
// carries it.
func TestSlackConnectNeverEchoesATokenGivenAsApp2876(t *testing.T) {
	// Assembled at runtime: a token-shaped literal in the source trips push protection.
	pasted := "xo" + "xb-1234567890-0987654321-AbCdEfGhIjKlMnOpQrStUvWx"
	bot := tokenFile(t, "bot", botTok)
	app := tokenFile(t, "app", appTok)
	_, _, n, e := runSlackWrite(t, "CreateSlackConnection", `{}`, "",
		"connect", "--org", "acme.com", "--app", pasted, "--bot-token-file", bot, "--app-token-file", app)
	if e == nil || n != 0 || exitCodeFor(e) != exitcode.Usage {
		t.Fatalf("a token as --app refuses with zero requests: err=%v requests=%d", e, n)
	}
	if strings.Contains(e.Error(), pasted) || strings.Contains(e.Error(), pasted[:10]) || strings.Contains(e.Error(), pasted[len(pasted)-10:]) {
		t.Errorf("the refusal echoes the pasted token: %v", e)
	}
	if !strings.Contains(e.Error(), "not org-qualified") {
		t.Errorf("the refusal comes from --app validation: %v", e)
	}
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	f.JSON = true
	renderError(f, e)
	if strings.Contains(out.String()+stderr.String(), pasted[:10]) || strings.Contains(out.String()+stderr.String(), pasted[len(pasted)-10:]) {
		t.Errorf("the --json envelope echoes the pasted token: %q %q", out.String(), stderr.String())
	}
}
