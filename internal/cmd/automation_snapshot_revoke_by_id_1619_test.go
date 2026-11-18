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

// --app is still accepted (older scripts pass it) but no longer scopes or
// supplies anything for a person: the by-id read carries only the id.
func TestSnapshotRevokeByIDAcceptsAppAndIgnoresIt1619(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrantById"] = byIDReply1619(snapshotGrantEnded1619)
	calls, _, err := runTeamTask(t, r, append(snapshotArgs1578("revoke", "grant1", "--yes", "--json"), "--app", autoApp)...)
	if err != nil {
		t.Fatal(err)
	}
	reads := callsFor(calls, "AutomationTeamSnapshotGrantById")
	if len(reads) != 1 || !reflect.DeepEqual(reads[0].Vars, map[string]any{"id": "grant1"}) || len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 0 {
		t.Fatalf("--app must not change the by-id read: %+v", *calls)
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
