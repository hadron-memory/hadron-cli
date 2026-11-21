package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2760 item 1: the degraded note names the one cause that applied. A failed
// connections read and a read that did not list the row are told apart on
// stderr; exit 0 and the JSON markers are the same for both.
func TestConnectDegradedNoteNamesTheLookupOutcome2760(t *testing.T) {
	for name, tc := range map[string]struct {
		fill      string
		want, not string
	}{
		"fill read fails":     {fillBoom, "the connections read failed", "did not list"},
		"row is not listed":   {connRows(connRow("someone-else")), "the connections read did not list it", "read failed"},
		"listing comes empty": {connRows(), "the connections read did not list it", "read failed"},
	} {
		for _, mode := range []string{"--json", "--no-browser"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, tc.fill, "connection", "connect", "gmail", "--org", "org_1", mode)
				if r.err != nil || r.code != 0 {
					t.Fatalf("a degraded fill must still succeed: exit %d err %v", r.code, r.err)
				}
				if strings.Count(r.errOut, "could not be filled") != 1 || !strings.Contains(r.errOut, tc.want) || strings.Contains(r.errOut, tc.not) {
					t.Fatalf("note must name only %q: %q", tc.want, r.errOut)
				}
				if mode == "--json" {
					var dto degradedDTO
					if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
						t.Fatal(err)
					}
					if !dto.Degraded || strings.Join(dto.DegradedFields, ",") != "mailboxEmail,displayName" {
						t.Fatalf("JSON marker changed: %+v", dto)
					}
				}
			})
		}
	}
}

// cli#2760 item 2: a malformed start names what is missing, still Unavailable
// (exit 7), and nothing is shown, opened, polled or filled. cli#2845 F2 adds
// the blank-link case (a whitespace-only authorize URL is no link — the pin is
// red until the start check trims it) and F5 loops the output modes, so the
// refusal is pinned in each of --json, --no-wait and --no-browser, not just
// the human one.
func TestConnectMalformedStartNamesTheMissingField2760(t *testing.T) {
	for name, tc := range map[string]struct{ start, want string }{
		"no start":      {`{"data":{"startConnection":null}}`, "server returned no connection start"},
		"no link":       {strings.Replace(connectStart, `"authorizeUrl":"`+connectURL+`"`, `"authorizeUrl":""`, 1), "server returned a connection start with no authorize link"},
		"blank link":    {strings.Replace(connectStart, `"authorizeUrl":"`+connectURL+`"`, `"authorizeUrl":"   "`, 1), "server returned a connection start with no authorize link"},
		"empty flow id": {strings.Replace(connectStart, `"flowId":"flow_1"`, `"flowId":""`, 1), "server returned a connection start with no flow id"},
		"blank flow id": {strings.Replace(connectStart, `"flowId":"flow_1"`, `"flowId":"  "`, 1), "server returned a connection start with no flow id"},
	} {
		for _, mode := range []string{"--json", "--no-wait", "--no-browser"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				r := runConnectionScript(t, tc.start, []string{done}, connRows(connRow("c9")), nil, "connection", "connect", "gmail", "--org", "org_1", mode)
				if r.code != exitcode.Unavailable || r.err == nil || r.err.Error() != tc.want {
					t.Fatalf("exit %d err %v, want Unavailable %d %q", r.code, r.err, exitcode.Unavailable, tc.want)
				}
				if len(r.opened) != 0 || strings.Contains(r.out, connectURL) || strings.Contains(r.errOut, connectURL) {
					t.Fatalf("the link was shown or opened: opened %v out %q err %q", r.opened, r.out, r.errOut)
				}
				if count(r.ops, "ConnectionFlow") != 0 || count(r.ops, "EmailConnections") != 0 {
					t.Fatalf("polled after a broken start: %v", r.ops)
				}
			})
		}
	}
}

// cli#2760 item 3: the marker means the lookup failed or did not list the row.
// A row that IS listed with an empty mailbox email and no display name is a
// successful fill: no marker, no note, the fields as the row has them.
func TestConnectListedRowWithEmptyFieldsIsNotDegraded2760(t *testing.T) {
	row := strings.Replace(connRow("c9"), `"mailboxEmail":"c9@acme.com"`, `"mailboxEmail":""`, 1)
	if !strings.Contains(row, `"mailboxEmail":""`) {
		t.Fatalf("fixture did not apply: %s", row)
	}
	// cli#2845 F4: the null display name is part of this fixture, not an
	// accident of connRow — guard it explicitly instead of relying on it.
	if !strings.Contains(row, `"displayName":null`) {
		t.Fatalf("fixture lost its null displayName: %s", row)
	}
	r := runFlowConnect(t, []string{flowResp("CONNECTED", "c9", "")}, connRows(row), "connection", "connect", "gmail", "--org", "org_1", "--json")
	if r.err != nil || r.code != 0 {
		t.Fatalf("exit %d err %v", r.code, r.err)
	}
	if strings.Contains(r.errOut, "could not be filled") {
		t.Fatalf("a listed row was noted degraded: err %q", r.errOut)
	}
	// cli#2845 F3: assert by decoding, not by matching "degraded" as text, so
	// the negative cannot pass on the encoder's spelling of the keys.
	var raw struct {
		Degraded       json.RawMessage `json:"degraded"`
		DegradedFields json.RawMessage `json:"degradedFields"`
	}
	if err := json.Unmarshal([]byte(r.out), &raw); err != nil {
		t.Fatalf("%v: %q", err, r.out)
	}
	if raw.Degraded != nil || raw.DegradedFields != nil {
		t.Fatalf("a listed row carried the degraded marker: %q", r.out)
	}
	var dto degradedDTO
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.Connection.ID != "c9" || dto.Connection.MailboxEmail != "" || dto.Connection.DisplayName != nil {
		t.Fatalf("connection %+v err %v", dto.Connection, err)
	}
}
