package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1717 item 1: a connect whose follow-up fill read could not supply the
// mailbox email and display name still succeeds (the established connection is
// never lost), but it is no longer silent: one stderr note names what could not
// be filled, and --json carries "degraded": true plus the field names. A healthy
// fill adds nothing to either stream.

const fillBoom = `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`

type degradedDTO struct {
	Status         string   `json:"status"`
	Degraded       bool     `json:"degraded"`
	DegradedFields []string `json:"degradedFields"`
	Connection     struct {
		ID           string  `json:"id"`
		Provider     string  `json:"provider"`
		MailboxEmail string  `json:"mailboxEmail"`
		DisplayName  *string `json:"displayName"`
	} `json:"connection"`
}

func TestConnectDegradedFillIsMarkedInJSONAndOnStderr1717(t *testing.T) {
	for name, fill := range map[string]string{
		"fill read fails":     fillBoom,
		"row is not listed":   connRows(connRow("someone-else")),
		"listing comes empty": connRows(),
	} {
		t.Run(name, func(t *testing.T) {
			r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, fill, "connection", "connect", "gmail", "--org", "org_1", "--json")
			if r.err != nil || r.code != 0 {
				t.Fatalf("a degraded fill must still succeed: exit %d err %v", r.code, r.err)
			}
			var dto degradedDTO
			if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
				t.Fatalf("%v: %q", err, r.out)
			}
			if dto.Status != "connected" || dto.Connection.ID != "c9" {
				t.Fatalf("success lost: %+v", dto)
			}
			if !dto.Degraded || strings.Join(dto.DegradedFields, ",") != "mailboxEmail,displayName" {
				t.Fatalf("degraded marker %+v, out %q", dto, r.out)
			}
			// The marker names fields that really are empty: both keys are present,
			// the email is "" and the display name null, and the provider is the row key.
			// Decoded, not matched as text, so the pin does not depend on the
			// encoder's whitespace (cli#2760 item 4).
			var raw struct {
				Connection map[string]json.RawMessage `json:"connection"`
			}
			if err := json.Unmarshal([]byte(r.out), &raw); err != nil {
				t.Fatal(err)
			}
			email, hasEmail := raw.Connection["mailboxEmail"]
			name, hasName := raw.Connection["displayName"]
			if !hasEmail || string(email) != `""` || !hasName || string(name) != "null" || dto.Connection.Provider != "gmail" {
				t.Fatalf("degraded connection %+v, out %q", dto.Connection, r.out)
			}
			if !strings.Contains(r.errOut, "mailboxEmail") || !strings.Contains(r.errOut, "displayName") || !strings.Contains(r.errOut, "c9") {
				t.Fatalf("stderr note must name the fields and the connection: %q", r.errOut)
			}
			if n := strings.Count(r.errOut, "could not be filled"); n != 1 {
				t.Fatalf("want exactly one note, got %d: %q", n, r.errOut)
			}
		})
	}
}

// Human mode: the same one note, the success line unchanged.
func TestConnectDegradedFillHumanNoteAndSuccessLine1717(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, fillBoom, "connection", "connect", "gmail", "--org", "org_1", "--no-browser")
	if r.err != nil || r.code != 0 {
		t.Fatalf("exit %d err %v", r.code, r.err)
	}
	if !strings.Contains(r.out, "✓ connected gmail () as c9") {
		t.Fatalf("success line changed: %q", r.out)
	}
	if strings.Count(r.errOut, "could not be filled") != 1 || !strings.Contains(r.errOut, "mailboxEmail, displayName") {
		t.Fatalf("stderr note: %q", r.errOut)
	}
}

// Neither the note nor the marker may carry the single-use authorize link
// (scoped §10 of the gate): it is on stdout once, in the default human case.
func TestConnectDegradedNoteNeverCarriesTheAuthorizeLink1717(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, fillBoom, "connection", "connect", "gmail", "--org", "org_1", "--no-browser")
	if !strings.Contains(r.errOut, "could not be filled") {
		t.Fatalf("no note to check: %q", r.errOut)
	}
	if strings.Contains(r.errOut, connectURL) || strings.Contains(r.errOut, "nonce") {
		t.Fatalf("stderr carries the link: %q", r.errOut)
	}
	rj := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, fillBoom, "connection", "connect", "gmail", "--org", "org_1", "--json")
	if rj.code != 0 {
		t.Fatalf("exit %d", rj.code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rj.out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"degraded", "degradedFields"} {
		if strings.Contains(string(raw[k]), "http") {
			t.Fatalf("%s carries a URL: %s", k, raw[k])
		}
	}
}

// A healthy fill adds nothing: no note, no new keys at the top level, and the
// connection object carries the filled values.
func TestConnectHealthyFillAddsNoMarkerAndNoNote1717(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, connRows(connRow("c9")), "connection", "connect", "gmail", "--org", "org_1", "--json")
	if r.err != nil || r.code != 0 {
		t.Fatalf("exit %d err %v", r.code, r.err)
	}
	if strings.Contains(r.out, "degraded") || strings.Contains(r.errOut, "could not be filled") {
		t.Fatalf("healthy fill marked degraded: out %q err %q", r.out, r.errOut)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(r.out), &raw)
	var dto degradedDTO
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.Connection.ID != "c9" || dto.Connection.MailboxEmail != "c9@acme.com" || dto.Connection.Provider != "gmail" {
		t.Fatalf("healthy connection %+v err %v", dto.Connection, err)
	}
	want := []string{"authorizeUrl", "browserOpened", "connection", "expiresAt", "failureCode", "flowId", "orgId", "provider", "status"}
	if len(raw) != len(want) {
		t.Fatalf("top-level keys changed: %v", raw)
	}
	for _, k := range want {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing key %s", k)
		}
	}
}

// Outcomes that never fill (failed, still pending at the deadline, and
// --no-wait's "started") never carry the marker or the note.
func TestConnectNonConnectedOutcomesCarryNoDegradedMarker1717(t *testing.T) {
	cases := map[string]struct {
		flows []string
		args  []string
		code  int
	}{
		"failed":  {[]string{flowResp("FAILED", "", "access_denied")}, []string{"--json"}, exitcode.Error},
		"pending": {[]string{pend}, []string{"--json", "--wait", "10s"}, exitcode.WaitTimeout},
		"started": {[]string{pend}, []string{"--json", "--no-wait"}, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"connection", "connect", "gmail", "--org", "org_1"}, c.args...)
			r := runFlowConnect(t, c.flows, fillBoom, args...)
			if r.code != c.code || strings.Contains(r.out, "degraded") || strings.Contains(r.errOut, "could not be filled") {
				t.Fatalf("exit %d (want %d) out %q err %q", r.code, c.code, r.out, r.errOut)
			}
			if count(r.ops, "EmailConnections") != 0 {
				t.Fatalf("an outcome that never fills read the connection list: %v", r.ops)
			}
		})
	}
}

// cli#1717 item 7 (a): `connection flow` refuses an empty or blank id as a
// usage error (exit 2) before any request. Pinned here; the refusal itself
// predates #1717.
func TestConnectionFlowRefusesAnEmptyIDBeforeAnyRequest1717(t *testing.T) {
	for _, id := range []string{"", "   "} {
		r := runFlowConnect(t, []string{pend}, connRows(), "connection", "flow", id)
		if r.code != exitcode.Usage || r.err == nil || !strings.Contains(r.err.Error(), "<flow-id>") {
			t.Fatalf("id %q: exit %d err %v, want usage exit %d naming <flow-id>", id, r.code, r.err, exitcode.Usage)
		}
		if len(r.ops) != 0 {
			t.Fatalf("id %q: a request was sent: %v", id, r.ops)
		}
	}
}

// cli#1717 item 7 (b): the flow id comes from the server's startConnection
// reply. An empty one is a broken reply (not a usage error: there is no flag
// to blame and the request has already been made), refused like an empty
// authorize URL: Unavailable (exit 7), before the link is shown or opened and
// before any poll or fill.
func TestConnectRefusesAnEmptyFlowIDFromTheServer1717(t *testing.T) {
	for name, flowID := range map[string]string{"empty": "", "blank": "  "} {
		t.Run(name, func(t *testing.T) {
			start := strings.Replace(connectStart, `"flowId":"flow_1"`, `"flowId":"`+flowID+`"`, 1)
			for _, args := range [][]string{
				{"connection", "connect", "gmail", "--org", "org_1"},
				{"connection", "connect", "gmail", "--org", "org_1", "--no-browser"},
				{"connection", "connect", "gmail", "--org", "org_1", "--no-wait", "--json"},
			} {
				r := runConnectionScript(t, start, []string{done}, connRows(connRow("c9")), nil, args...)
				if r.code != exitcode.Unavailable || r.err == nil || r.err.Error() != "server returned a connection start with no flow id" {
					t.Fatalf("%v: exit %d err %v, want Unavailable %d", args, r.code, r.err, exitcode.Unavailable)
				}
				if len(r.opened) != 0 || strings.Contains(r.out, connectURL) || strings.Contains(r.errOut, connectURL) {
					t.Fatalf("%v: the link was shown or opened: opened %v out %q err %q", args, r.opened, r.out, r.errOut)
				}
				if count(r.ops, "ConnectionFlow") != 0 || count(r.ops, "EmailConnections") != 0 {
					t.Fatalf("%v: polled after a broken start: %v", args, r.ops)
				}
			}
		})
	}
}

// Scoped §10 of the gate, adversarial case (Joost, F1 at 6f0d448f): the
// connection id in the note comes from the server. SafeCell neutralizes
// terminal controls but is not redaction, so a connectionId that is, or
// contains the nonce of, the single-use authorize link must not reach stderr
// through the degraded note, in any mode. The same redactor already scrubs
// every error raised after the link was received.
func TestConnectDegradedNoteRedactsAnIDThatEchoesTheLink1717(t *testing.T) {
	ids := map[string]string{
		"id is the full authorize URL": connectURL,
		"id contains the nonce":        "conn-SECRETNONCE123456",
	}
	modes := map[string][]string{
		"human, link printed": {"connection", "connect", "gmail", "--org", "org_1", "--no-browser"},
		"human, browser open": {"connection", "connect", "gmail", "--org", "org_1"},
		"json":                {"connection", "connect", "gmail", "--org", "org_1", "--json"},
	}
	for idName, id := range ids {
		for modeName, args := range modes {
			t.Run(idName+"/"+modeName, func(t *testing.T) {
				r := runFlowConnect(t, []string{flowResp("CONNECTED", id, "")}, fillBoom, args...)
				if r.err != nil || r.code != 0 {
					t.Fatalf("exit %d err %v", r.code, r.err)
				}
				if !strings.Contains(r.errOut, "could not be filled") {
					t.Fatalf("the note is gone, not redacted: %q", r.errOut)
				}
				if strings.Contains(r.errOut, connectURL) || strings.Contains(r.errOut, "SECRETNONCE123456") || strings.Contains(r.errOut, "nonce=") {
					t.Fatalf("stderr carries the link or its nonce: %q", r.errOut)
				}
			})
		}
	}
}

// The marker's field names are fixed strings: nothing server-supplied can
// reach "degradedFields", whatever the connection id says.
func TestConnectDegradedFieldsAreFixedWhateverTheIDSays1717(t *testing.T) {
	r := runFlowConnect(t, []string{flowResp("CONNECTED", connectURL, "")}, fillBoom, "connection", "connect", "gmail", "--org", "org_1", "--json")
	var dto degradedDTO
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.Degraded || strings.Join(dto.DegradedFields, ",") != "mailboxEmail,displayName" {
		t.Fatalf("degraded marker %+v", dto)
	}
}
