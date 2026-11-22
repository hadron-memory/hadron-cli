package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2952 telegram status/get (reads). Fake HTTP only: no Telegram, no network.
// The fake rows carry lastError and a bot token the query never selects, so a
// selection drift that ever asked for them fails the PRIVATE checks below.

const telegramRow = `{"id":"t1","organizationId":"o1","appId":"a1","provider":"telegram","status":"ACTIVE","displayName":"Acme\u001b[31m","botId":"B1","botUsername":"acme_bot","createdAt":"c","updatedAt":"u","lastError":"PRIVATE_ERR","botToken":"PRIVATE_TOK"}`

func runTelegram(t *testing.T, wire string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	srv, ops := captureGraphQL(t, map[string]string{"TelegramConnections": wire, "TelegramConnection": wire})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"telegram"}, args...), "--server", srv.URL))
	e := root.Execute()
	return out.String(), ops, e
}

func TestTelegramStatusRequestAndShape(t *testing.T) {
	out, ops, e := runTelegram(t, `{"data":{"telegramConnections":{"total":3,"items":[`+telegramRow+`]}}}`, "status", "--org", "o1", "--limit", "1", "--offset", "1", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["TelegramConnections"], &vars)
	if vars["orgId"] != "o1" || vars["limit"] != float64(1) || vars["offset"] != float64(1) {
		t.Fatal(vars)
	}
	var got struct {
		Items   []map[string]any
		Total   int
		Offset  int
		HasMore bool
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatal(e, out)
	}
	if got.Total != 3 || !got.HasMore || got.Offset != 1 || len(got.Items) != 1 || len(got.Items[0]) != 10 {
		t.Fatal(out)
	}
	if got.Items[0]["appId"] != "a1" || got.Items[0]["botUsername"] != "acme_bot" {
		t.Fatal(out)
	}
	if strings.Contains(out, "PRIVATE") || strings.Contains(out, "lastError") {
		t.Fatal(out)
	}
}

func TestTelegramStatusDefaultsOmitScopeAndPaging(t *testing.T) {
	_, ops, e := runTelegram(t, `{"data":{"telegramConnections":{"total":0,"items":[]}}}`, "status", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["TelegramConnections"], &vars)
	if len(vars) != 0 {
		t.Fatal("expected omitted variables, got", vars)
	}
}

func TestTelegramStatusCountOnlyAndEmpty(t *testing.T) {
	out, ops, e := runTelegram(t, `{"data":{"telegramConnections":{"total":4,"items":[]}}}`, "status", "--limit", "0", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["TelegramConnections"], &vars)
	if v, ok := vars["limit"]; !ok || v != float64(0) {
		t.Fatal("limit 0 must be sent", vars)
	}
	if !strings.Contains(out, `"items": []`) && !strings.Contains(out, `"items":[]`) {
		t.Fatal(out)
	}
}

func TestTelegramStatusHumanHintTableAndEscapes(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"TelegramConnections": `{"data":{"telegramConnections":{"total":3,"items":[` + telegramRow + `]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"telegram", "status", "--server", srv.URL})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	if strings.ContainsRune(out.String(), '\x1b') || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal(out.String())
	}
	// The table's columns: the App (not a default app), the bot's two names.
	for _, col := range []string{"ORG ID", "APP ID", "STATUS", "BOT", "BOT USERNAME", "BOT ID"} {
		if !strings.Contains(out.String(), col) {
			t.Fatalf("table missing column %s:\n%s", col, out.String())
		}
	}
	if got := f.IOStreams.ErrOut.(*strings.Builder).String(); !strings.Contains(got, "showing 1 of 3; next page: --offset 1") {
		t.Fatal("missing continuation hint:", got)
	}
}

func TestTelegramStatusFinalPageHasNoHint(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"TelegramConnections": `{"data":{"telegramConnections":{"total":1,"items":[` + telegramRow + `]}}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"telegram", "status", "--server", srv.URL})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	if got := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(got, "next page") {
		t.Fatal("unexpected hint:", got)
	}
}

// The continuation hint is cosmetic and follows only a page that RENDERED: a
// stdout that rejects every write (a closed pipe) fails the command, and the
// hint must not print beside that failure.
func TestTelegramStatusFailedWritePrintsNoHint(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"TelegramConnections": `{"data":{"telegramConnections":{"total":3,"items":[` + telegramRow + `]}}}`})
	f, _ := testFactory(t)
	f.IOStreams.Out = failingWriter{}
	root := NewRootCmd(f)
	root.SetArgs([]string{"telegram", "status", "--server", srv.URL})
	if e := root.Execute(); e == nil {
		t.Fatal("a failed write fails the command")
	}
	if got := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(got, "next page") {
		t.Fatal("no hint after a page that failed to render:", got)
	}
}

func TestTelegramStatusRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		args       []string
		want       int
	}{
		{"null-page", `{"data":{"telegramConnections":null}}`, []string{"status"}, 7},
		{"null-row", `{"data":{"telegramConnections":{"total":1,"items":[null]}}}`, []string{"status"}, 7},
		{"unknown-status", `{"data":{"telegramConnections":{"total":1,"items":[` + strings.Replace(telegramRow, `"ACTIVE"`, `"PAUSED"`, 1) + `]}}}`, []string{"status"}, 7},
		{"denied", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`, []string{"status"}, 8},
		{"server-error", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, []string{"status"}, 1},
		{"limit-over-cap", ``, []string{"status", "--limit", "201"}, 2},
		{"negative-offset", ``, []string{"status", "--offset", "-1"}, 2},
		{"blank-org", ``, []string{"status", "--org", "  "}, 2},
		{"extra-arg", ``, []string{"status", "x"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, e := runTelegram(t, tc.wire, tc.args...)
			if exitCodeFor(e) != tc.want {
				t.Fatal(e, out)
			}
			if strings.Contains(out, "PRIVATE") {
				t.Fatal(out)
			}
		})
	}
}

func TestTelegramGet(t *testing.T) {
	// " t1 ": the id is trimmed before it is sent (the trim is pinned by the
	// variable capture, not just accepted).
	out, ops, e := runTelegram(t, `{"data":{"telegramConnection":`+telegramRow+`}}`, "get", " t1 ", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["TelegramConnection"], &vars)
	if vars["ref"] != "t1" {
		t.Fatal("get must trim the id it sends", vars)
	}
	var m map[string]any
	if vars["ref"] != "t1" || json.Unmarshal([]byte(out), &m) != nil || len(m) != 10 || m["status"] != "ACTIVE" || m["appId"] != "a1" || strings.Contains(out, "PRIVATE") {
		t.Fatal(vars, out)
	}
	// Text mode: one line per field, control characters never raw.
	out, _, e = runTelegram(t, `{"data":{"telegramConnection":`+telegramRow+`}}`, "get", "t1")
	if e != nil || strings.ContainsRune(out, '\x1b') || !strings.Contains(out, `"t1"`) || !strings.Contains(out, "bot username:") {
		t.Fatal(e, out)
	}
	// An invisible id (missing, other org, revoked member) answers null → 4.
	if _, _, e := runTelegram(t, `{"data":{"telegramConnection":null}}`, "get", "nope"); exitCodeFor(e) != 4 {
		t.Fatal("null must be not-found", e)
	}
	// get refuses what status refuses: an unrecognized status is exit 7 here too.
	if _, _, e := runTelegram(t, `{"data":{"telegramConnection":`+strings.Replace(telegramRow, `"ACTIVE"`, `"PAUSED"`, 1)+`}}`, "get", "t1"); exitCodeFor(e) != 7 {
		t.Fatal("an unknown status must be refused on get too", e)
	}
	// A non-FORBIDDEN API error maps like any read (exit 1, not 8).
	if _, _, e := runTelegram(t, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, "get", "t1"); exitCodeFor(e) != 1 {
		t.Fatal("an internal error on get is exit 1", e)
	}
	if _, _, e := runTelegram(t, ``, "get"); exitCodeFor(e) != 2 {
		t.Fatal(e)
	}
	if _, _, e := runTelegram(t, `{}`, "get", "  "); exitCodeFor(e) != 2 {
		t.Fatal("a blank id is a usage refusal", e)
	}
}
