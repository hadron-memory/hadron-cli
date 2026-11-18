package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1619: a PERSON's `team-snapshot-grant revoke` finds the grant with the
// by-id read automationTeamSnapshotGrant(id:) (server#3381), not by paging the
// App's whole grant history. The held-worker --instruction path stays on the
// paged list (the by-id read has no worker arm).

const snapshotGrantEnded1619 = `{"id":"grant1","appId":"capp100000000000000000000","triggerKind":"SCHEDULE","triggerId":"schedule1","grantedBy":"user1","createdAt":"2026-11-03T00:00:00Z","revokedAt":"2026-11-03T01:00:00Z","revokedBy":"user1"}`

func byIDReply1619(row string) string {
	return `{"data":{"automationTeamSnapshotGrant":` + row + `}}`
}

func TestSnapshotRevokeByIDOneReadNoPages1619(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrantById"] = byIDReply1619(snapshotGrant1578)
	r["RevokeAutomationTeamSnapshotGrant"] = `{"data":{"revokeAutomationTeamSnapshotGrant":` + snapshotGrantEnded1619 + `}}`
	// No --app and no active App: the by-id read does not need one.
	calls, out, err := runTeamTask(t, r, snapshotArgs1578("revoke", "grant1", "--yes", "--json")...)
	if err != nil {
		t.Fatalf("a person revoke by id needs no App: %v", err)
	}
	if n := len(callsFor(calls, "AutomationTeamSnapshotGrants")); n != 0 {
		t.Fatalf("no list pages for a person revoke, got %d", n)
	}
	reads := callsFor(calls, "AutomationTeamSnapshotGrantById")
	if len(reads) != 1 || !reflect.DeepEqual(reads[0].Vars, map[string]any{"id": "grant1"}) {
		t.Fatalf("want exactly one by-id read carrying only the id, got %+v", reads)
	}
	if len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 1 {
		t.Fatalf("one revoke mutation: %+v", *calls)
	}
	var got, want map[string]any
	if json.Unmarshal([]byte(out), &got) != nil || json.Unmarshal([]byte(snapshotGrantEnded1619), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("--json is the grant DTO, unchanged: %s", out)
	}
}

// Unknown and foreign ids are the same server NOT_FOUND, and the same CLI error
// as the list lookup's "not found": exit 4, no mutation, no list fallback.
func TestSnapshotRevokeByIDNotFoundIsToday1619(t *testing.T) {
	for _, name := range []string{"unknown id", "foreign id"} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AutomationTeamSnapshotGrantById"] = memberGQLError("Team snapshot grant not found", "NOT_FOUND")
			calls, _, err := runTeamTask(t, r, snapshotArgs1578("revoke", "grant1", "--yes", "--json")...)
			if exitCodeFor(err) != exitcode.NotFound || err == nil || !strings.Contains(err.Error(), "team snapshot grant not found") {
				t.Fatalf("want exit %d \"team snapshot grant not found\", got %v", exitcode.NotFound, err)
			}
			if len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 0 || len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 0 {
				t.Fatalf("a not-found sends no mutation and does not fall back to the list: %+v", *calls)
			}
		})
	}
}

// An already revoked row is shown as revoked and revocation is not re-sent.
func TestSnapshotRevokeByIDRevokedRowNotResent1619(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrantById"] = byIDReply1619(snapshotGrantEnded1619)
	calls, out, err := runTeamTask(t, r, snapshotArgs1578("revoke", "grant1", "--yes", "--json")...)
	if err != nil || !strings.Contains(out, `"revokedAt": "2026-11-03T01:00:00Z"`) {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 0 {
		t.Fatalf("a revoked row must not be re-sent: %+v", *calls)
	}
}

// The confirmation shows the grant's OWN App and trigger (from the by-id read),
// not an App the user selected.
func TestSnapshotRevokeByIDConfirmationShowsTheGrantsApp1619(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrantById"] = byIDReply1619(snapshotGrant1578)
	srv, calls := attnServer(t, r)
	f, out, prompt := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, snapshotArgs1578("revoke", "grant1")...))
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Cancelled {
		t.Fatalf("err=%v", err)
	}
	text := prompt.String() + out.String()
	for _, want := range []string{"capp100000000000000000000", "SCHEDULE", "schedule1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("confirmation lacks %q: %s", want, text)
		}
	}
	if len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant"))+len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 0 {
		t.Fatalf("declining sends no mutation and reads no list: %+v", *calls)
	}
}

// byIDOrMissing1619 is the by-id reply for a row, or the server's NOT_FOUND when
// the row is empty (an unknown or foreign id answers the same).
func byIDOrMissing1619(row string) string {
	if row == "" {
		return memberGQLError("Team snapshot grant not found", "NOT_FOUND")
	}
	return byIDReply1619(row)
}

// A broken by-id reply never reaches the confirmation or the mutation.
func TestSnapshotRevokeByIDMalformedReplyNoWrite1619(t *testing.T) {
	for _, tc := range []struct{ name, reply, want string }{
		{"null grant", `{"data":{"automationTeamSnapshotGrant":null}}`, "returned no team snapshot grant"},
		{"incomplete row", byIDReply1619(strings.Replace(snapshotGrant1578, `"createdAt":"2026-11-03T00:00:00Z"`, `"createdAt":""`, 1)), "incomplete team snapshot grant"},
		{"a different grant", byIDReply1619(strings.ReplaceAll(snapshotGrant1578, "grant1", "other")), "different team snapshot grant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := taskResponses()
			r["AutomationTeamSnapshotGrantById"] = tc.reply
			calls, out, err := runTeamTask(t, r, snapshotArgs1578("revoke", "grant1", "--yes", "--json")...)
			if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), tc.want) || out != "" {
				t.Fatalf("err=%v out=%q", err, out)
			}
			if len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 0 || len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 0 {
				t.Fatalf("a broken reply sends no mutation and does not fall back to the list: %+v", *calls)
			}
		})
	}
}

const grantApp1619 = "capp100000000000000000000"

func appByRefReply1619(id string) string {
	if id == "" {
		return `{"data":{"app":null}}`
	}
	return `{"data":{"app":{"id":"` + id + `","urn":"hrn:app:acme.com:ops","name":"Ops","appType":"CLOUD","description":null}}}`
}

// Wolfgang's ruling on #2423: an explicit --app is never a silent no-op. For a
// person, the grant is read by id; if --app is given and the grant belongs to a
// DIFFERENT App, the revoke refuses as today's "not found" (exit 4) before the
// confirmation and sends no mutation. An App given as an id is compared
// directly; a URN or root:slug costs one app(ref:) read to learn its id. With no
// --app, the grant's own App is simply named in the confirmation.
func TestSnapshotRevokeByIDExplicitAppScopesTheGrant1619(t *testing.T) {
	const other = "capp900000000000000000000"
	for _, tc := range []struct {
		name     string
		app      string
		appReply string // GetAppMetadata reply; "-" means it must not be called
		proceeds bool
	}{
		{"app id of the grant's own App", grantApp1619, "-", true},
		{"app id of another App", other, "-", false},
		{"URN resolving to the grant's own App", autoApp, appByRefReply1619(grantApp1619), true},
		{"URN resolving to another App", autoApp, appByRefReply1619(other), false},
		{"URN the caller cannot read", autoApp, appByRefReply1619(""), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := taskResponses()
			r["AutomationTeamSnapshotGrantById"] = byIDReply1619(snapshotGrant1578)
			r["RevokeAutomationTeamSnapshotGrant"] = `{"data":{"revokeAutomationTeamSnapshotGrant":` + snapshotGrantEnded1619 + `}}`
			if tc.appReply != "-" {
				r["GetAppMetadata"] = tc.appReply
			}
			calls, _, err := runTeamTask(t, r, append(snapshotArgs1578("revoke", "grant1", "--yes", "--json"), "--app", tc.app)...)
			revokes := len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant"))
			if len(callsFor(calls, "AutomationTeamSnapshotGrantById")) != 1 || len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 0 {
				t.Fatalf("one by-id read, no list pages: %+v", *calls)
			}
			if tc.appReply == "-" && len(callsFor(calls, "GetAppMetadata")) != 0 {
				t.Fatalf("an App id is compared directly, no lookup: %+v", *calls)
			}
			if tc.proceeds {
				if err != nil || revokes != 1 {
					t.Fatalf("the grant is in the named App: err=%v revokes=%d", err, revokes)
				}
				return
			}
			if exitCodeFor(err) != exitcode.NotFound || err == nil || !strings.Contains(err.Error(), "team snapshot grant not found") || revokes != 0 {
				t.Fatalf("a grant outside the named App is not found, no mutation: err=%v revokes=%d", err, revokes)
			}
		})
	}
}

// The refusal comes BEFORE the confirmation: a person on a terminal is never
// asked to revoke a grant the named App does not hold.
func TestSnapshotRevokeByIDWrongAppRefusesBeforeThePrompt1619(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrantById"] = byIDReply1619(snapshotGrant1578)
	srv, calls := attnServer(t, r)
	f, _, prompt := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, append(snapshotArgs1578("revoke", "grant1"), "--app", "capp900000000000000000000")...))
	err := root.Execute()
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(prompt.String(), "Revoke team snapshot grant") || len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 0 {
		t.Fatalf("no prompt and no mutation for a grant in another App: %s %+v", prompt, *calls)
	}
}
