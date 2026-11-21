package cmd

// cli#2756: follow-ups from the #2731 review rounds. Pins for the once-trimmed
// --org/--app, the malformed --app refusal, the short --app form, disconnect's
// text-mode success line, and the Zl/Zp line/paragraph separators the control
// character guard must refuse (pinned beside the ‮ case in the #206 file).
// Fake HTTP only.

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
