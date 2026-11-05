package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const readGrant1566 = `{"id":"rg1","appId":"capp100000000000000000000","memoryUrn":"hrn:mem:acme.com:notes","prefix":null,"triggerId":"schedule1","triggerKind":"SCHEDULE","grantedBy":"user1","createdAt":"2026-11-05T00:00:00Z","revokedAt":null,"revokedBy":null}`

const endedReadGrant1566 = `{"id":"rg1","appId":"capp100000000000000000000","memoryUrn":"hrn:mem:acme.com:notes","prefix":"docs","triggerId":"schedule1","triggerKind":"SCHEDULE","grantedBy":"user1","createdAt":"2026-11-05T00:00:00Z","revokedAt":"2026-11-05T01:00:00Z","revokedBy":"user2"}`

func readGrantArgs1566(verb string, extra ...string) []string {
	return append([]string{"automation", "grant", verb}, extra...)
}

func setReadGrant1566(t *testing.T, extra ...string) (*[]attnCall, string, error) {
	t.Helper()
	r := taskResponses()
	r["SetAutomationReadGrant"] = `{"data":{"setAutomationReadGrant":` + readGrant1566 + `}}`
	args := append([]string{autoApp, "--memory", "acme.com:notes", "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1"}, extra...)
	return runTeamTask(t, r, readGrantArgs1566("set", args...)...)
}

func TestReadGrant1566SetWholeMemoryOmitsPrefix(t *testing.T) {
	calls, out, err := setReadGrant1566(t, "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "SetAutomationReadGrant")
	if c.Vars["appRef"] != autoApp || c.Vars["memoryRef"] != "hrn:mem:acme.com:notes" || c.Vars["triggerKind"] != "SCHEDULE" || c.Vars["triggerId"] != "schedule1" {
		t.Fatalf("wrong request: %+v", c.Vars)
	}
	if _, present := c.Vars["prefix"]; present {
		t.Fatalf("a whole-memory grant must omit prefix, not send null: %+v", c.Vars)
	}
	var got map[string]any
	if json.Unmarshal([]byte(out), &got) != nil || got["id"] != "rg1" || got["memoryUrn"] != "hrn:mem:acme.com:notes" {
		t.Fatalf("DTO: %s", out)
	}
	if v, ok := got["prefix"]; !ok || v != nil {
		t.Fatalf("prefix must render as explicit null for a whole-memory grant: %s", out)
	}
	if v, ok := got["revokedAt"]; !ok || v != nil {
		t.Fatalf("revokedAt must render as explicit null: %s", out)
	}
}

func TestReadGrant1566SetSendsPrefix(t *testing.T) {
	calls, _, err := setReadGrant1566(t, "--prefix", "docs", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if c := findCall(t, calls, "SetAutomationReadGrant"); c.Vars["prefix"] != "docs" {
		t.Fatalf("prefix lost: %+v", c.Vars)
	}
}

func TestReadGrant1566SetGuardsSendNothing(t *testing.T) {
	cases := map[string][]string{
		"no yes":        {},
		"blank prefix":  {"--prefix", "  ", "--yes"},
		"empty prefix":  {"--prefix", "", "--yes"},
		"bad kind":      {"--yes"}, // replaced below
		"blank memory":  {"--yes"},
		"blank trigger": {"--yes"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["SetAutomationReadGrant"] = `{"data":{"setAutomationReadGrant":` + readGrant1566 + `}}`
			args := []string{autoApp, "--memory", "acme.com:notes", "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1"}
			switch name {
			case "bad kind":
				args[3] = "MANUAL"
			case "blank memory":
				args[2] = "  "
			case "blank trigger":
				args[5] = " "
			}
			calls, _, err := runTeamTask(t, r, readGrantArgs1566("set", append(args, extra...)...)...)
			if exitCodeFor(err) != exitcode.Usage || len(callsFor(calls, "SetAutomationReadGrant")) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestReadGrant1566SetRefusalIsSentOnceAndMapped(t *testing.T) {
	r := taskResponses()
	r["SetAutomationReadGrant"] = memberGQLError("Forbidden", "FORBIDDEN")
	calls, _, err := runTeamTask(t, r, readGrantArgs1566("set", autoApp, "--memory", "acme.com:notes", "--trigger-kind", "WEBHOOK", "--trigger-id", "hook1", "--yes")...)
	if exitCodeFor(err) != exitcode.Forbidden || len(callsFor(calls, "SetAutomationReadGrant")) != 1 {
		t.Fatalf("err=%v calls=%+v", err, *calls)
	}
}

func TestReadGrant1566SetRefusesAnUnconfirmedGrant(t *testing.T) {
	for name, body := range map[string]string{
		"null":       `{"data":{"setAutomationReadGrant":null}}`,
		"other kind": `{"data":{"setAutomationReadGrant":` + strings.Replace(readGrant1566, `"SCHEDULE"`, `"WEBHOOK"`, 1) + `}}`,
		"revoked":    `{"data":{"setAutomationReadGrant":` + endedReadGrant1566 + `}}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["SetAutomationReadGrant"] = body
			_, _, err := runTeamTask(t, r, readGrantArgs1566("set", autoApp, "--memory", "acme.com:notes", "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes")...)
			if exitCodeFor(err) == 0 {
				t.Fatal("an unconfirmed live grant must not print as success")
			}
		})
	}
}

func TestReadGrant1566ListFiltersAndEmpty(t *testing.T) {
	r := taskResponses()
	r["AutomationReadGrants"] = `{"data":{"automationReadGrants":[]}}`
	calls, out, err := runTeamTask(t, r, readGrantArgs1566("list", "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "AutomationReadGrants")
	for _, k := range []string{"appRef", "memoryRef", "includeRevoked"} {
		if _, present := c.Vars[k]; present {
			t.Fatalf("%s must be omitted when not asked for: %+v", k, c.Vars)
		}
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list must be [], got %s", out)
	}

	r["AutomationReadGrants"] = `{"data":{"automationReadGrants":[` + readGrant1566 + `]}}`
	calls, out, err = runTeamTask(t, r, readGrantArgs1566("list", "--app", autoApp, "--memory", "acme.com:notes", "--include-revoked", "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	c = findCall(t, calls, "AutomationReadGrants")
	if c.Vars["appRef"] != autoApp || c.Vars["memoryRef"] != "hrn:mem:acme.com:notes" || c.Vars["includeRevoked"] != true {
		t.Fatalf("filters lost: %+v", c.Vars)
	}
	var rows []map[string]any
	if json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 || rows[0]["id"] != "rg1" {
		t.Fatalf("rows: %s", out)
	}
}

func TestReadGrant1566ListHumanTable(t *testing.T) {
	r := taskResponses()
	r["AutomationReadGrants"] = `{"data":{"automationReadGrants":[` + readGrant1566 + `,` + endedReadGrant1566 + `]}}`
	_, out, err := runTeamTask(t, r, readGrantArgs1566("list")...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rg1", "hrn:mem:acme.com:notes", "docs", "SCHEDULE", "2026-11-05T01:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestReadGrant1566RevokeConfirmSendOnceAndDTO(t *testing.T) {
	r := taskResponses()
	r["RevokeAutomationReadGrant"] = `{"data":{"revokeAutomationReadGrant":` + endedReadGrant1566 + `}}`
	calls, _, err := runTeamTask(t, r, readGrantArgs1566("revoke", "rg1")...)
	if exitCodeFor(err) != exitcode.Usage || len(callsFor(calls, "RevokeAutomationReadGrant")) != 0 {
		t.Fatalf("revoke without --yes must be a usage refusal that sends nothing: err=%v calls=%+v", err, *calls)
	}
	calls, out, err := runTeamTask(t, r, readGrantArgs1566("revoke", "rg1", "--yes", "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if n := callsFor(calls, "RevokeAutomationReadGrant"); len(n) != 1 || n[0].Vars["id"] != "rg1" {
		t.Fatalf("calls=%+v", *calls)
	}
	var got map[string]any
	if json.Unmarshal([]byte(out), &got) != nil || got["revokedAt"] != "2026-11-05T01:00:00Z" || got["revokedBy"] != "user2" {
		t.Fatalf("DTO: %s", out)
	}
}

func TestReadGrant1566RevokeRefusesAnUnconfirmedRevoke(t *testing.T) {
	for name, body := range map[string]string{
		"null":       `{"data":{"revokeAutomationReadGrant":null}}`,
		"still live": `{"data":{"revokeAutomationReadGrant":` + readGrant1566 + `}}`,
		"other id":   `{"data":{"revokeAutomationReadGrant":` + strings.Replace(endedReadGrant1566, `"rg1"`, `"rg9"`, 1) + `}}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["RevokeAutomationReadGrant"] = body
			_, _, err := runTeamTask(t, r, readGrantArgs1566("revoke", "rg1", "--yes")...)
			if exitCodeFor(err) == 0 {
				t.Fatal("an unconfirmed revoke must not print as success")
			}
		})
	}
}

func TestReadGrant1566RevokeRefusalMapped(t *testing.T) {
	r := taskResponses()
	r["RevokeAutomationReadGrant"] = memberGQLError("Forbidden", "FORBIDDEN")
	calls, _, err := runTeamTask(t, r, readGrantArgs1566("revoke", "rg1", "--yes")...)
	if exitCodeFor(err) != exitcode.Forbidden || len(callsFor(calls, "RevokeAutomationReadGrant")) != 1 {
		t.Fatalf("err=%v calls=%+v", err, *calls)
	}
}
