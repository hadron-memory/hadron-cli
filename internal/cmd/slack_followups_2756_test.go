package cmd

// cli#2756: follow-ups from the #2731 review rounds. Pins for the once-trimmed
// --org/--app, the malformed --app refusal, the short --app form, disconnect's
// text-mode success line, and the Zl/Zp line/paragraph separators the control
// character guard must refuse (pinned beside the U+202E case in the #206 file).
// The #2827 rounds added the hostile-status refusal (one refused rune of every
// guarded class, exit code pinned) and the happy-path status table whose every
// cell renders quoted. Fake HTTP only.

import (
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

// #2827 review (SEC10, Bianca F1): a server-supplied status is untrusted. The
// status command refuses anything but ACTIVE/ERROR before its table (toDTO),
// and the refusal quotes the value (strconv.Quote), so hostile Zl/Zp/Cc/Cf
// runes can neither forge the refusal message nor the table row; the table's
// status cell is additionally wrapped like its siblings (SafeCell) should the
// allowlist ever widen. This pin holds the reachable guard: nothing raw
// reaches the terminal, and the refusal quotes what it names.
func TestSlackStatusRefusesAHostileStatusSafely(t *testing.T) {
	// One refused rune of every guarded class inside the server-supplied
	// status \u2014 a Zl and a Zp separator, a CR and an ESC (Cc) and an RLO
	// override (Cf) \u2014 carried as JSON escapes so this file stays pure ASCII.
	// Every rune below also appears in the raw-leak check, so no arm of that
	// check is vacuous (#2827 review, Jonas N3).
	hostile := "\"A\\u2028b\\u2029c\\u000dd\\u001be\\u202ef\""
	row := `{"id":"c1","organizationId":"o1","defaultAppId":"a1","provider":"slack","status":` + hostile + `,"teamId":"T1","teamName":"Acme","botId":"B1","botUserId":"U1","createdAt":"c","updatedAt":"u"}`
	out, ops, n, e := runSlackWrite(t, "SlackConnections", `{"data":{"slackConnections":{"total":1,"items":[`+row+`]}}}`, "", "status")
	if e == nil || n != 1 {
		t.Fatalf("the hostile status is refused after the one read: err=%v requests=%d", e, n)
	}
	if len(ops) != 1 || ops["SlackConnections"] == nil {
		t.Fatalf("the one request is the connections read: %v", ops)
	}
	// The refusal is Unavailable (a row this CLI will not render), pinned
	// (#2827 review, Jonas N5).
	if exitCodeFor(e) != exitcode.Unavailable {
		t.Fatalf("the hostile status refuses Unavailable, got exit %d: %v", exitCodeFor(e), e)
	}
	// Nothing hostile reaches the terminal raw, in the rendered output or the
	// error: every carried rune \u2014 separators, override, CR, ESC \u2014 appears only
	// as its quoted escape, never itself.
	if strings.ContainsAny(out+"\n"+e.Error(), "\x1b\r\u202e\u2028\u2029") {
		t.Fatalf("a hostile status must not reach the terminal raw:\n%q", out)
	}
	// The refusal quotes the value: the separator renders as its escape.
	if !strings.Contains(e.Error(), "\\u2028") || !strings.Contains(e.Error(), "\"A") {
		t.Fatalf("the refusal quotes the hostile status:\n%q", e.Error())
	}
}

// #2827 fold (Jonas N1): the happy path reaches the table. Every cell of the
// status table renders quoted \u2014 the table's own idiom \u2014 and the status cell
// is wrapped like its siblings, so this pin makes the wrap reachable: reverting
// the status cell to a raw render goes red here.
func TestSlackStatusTableQuotesEveryCell(t *testing.T) {
	row := `{"id":"c1","organizationId":"o1","defaultAppId":"a1","provider":"slack","status":"ACTIVE","teamId":"T1","teamName":"Acme","botId":"B1","botUserId":"U1","createdAt":"c","updatedAt":"u"}`
	out, _, n, e := runSlackWrite(t, "SlackConnections", `{"data":{"slackConnections":{"total":1,"items":[`+row+`]}}}`, "", "status")
	if e != nil || n != 1 {
		t.Fatalf("err=%v requests=%d", e, n)
	}
	for _, want := range []string{`"c1"`, `"ACTIVE"`, `"T1"`, `"Acme"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("every cell of the status table renders quoted, missing %s:\n%s", want, out)
		}
	}
}
