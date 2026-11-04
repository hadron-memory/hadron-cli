package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func endedSnapshot1578() string {
	return strings.Replace(snapshotGrant1578, `"revokedAt":null,"revokedBy":null`, `"revokedAt":"2026-11-03T01:00:00Z","revokedBy":"user1"`, 1)
}

func grantPage1578(items string, total int, more bool) string {
	return fmt.Sprintf(`{"data":{"automationTeamSnapshotGrants":{"items":[%s],"total":%d,"hasMore":%t}}}`, items, total, more)
}

// runTeamTask writes a live worker binding and pins it to its fake server
// before constructing the root (team_task_commands_test.go). This is the same
// fixture whose authored task writes assert a nonempty session header; it is
// not an unbound-context assertion that can pass trivially.
func TestSnapshotGrant1578BoundHeaderlessAllVerbs(t *testing.T) {
	for _, verb := range []string{"set", "list", "revoke"} {
		t.Run(verb, func(t *testing.T) {
			r := taskResponses()
			r["AutomationTeamSnapshotGrants"] = grantPage1578(snapshotGrant1578, 1, false)
			r["SetAutomationTeamSnapshotGrant"] = `{"data":{"setAutomationTeamSnapshotGrant":` + snapshotGrant1578 + `}}`
			r["RevokeAutomationTeamSnapshotGrant"] = `{"data":{"revokeAutomationTeamSnapshotGrant":` + endedSnapshot1578() + `}}`
			args := snapshotArgs1578("list", autoApp, "--json")
			want := []string{"AutomationTeamSnapshotGrants"}
			if verb == "set" {
				args = snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes", "--json")
				want = []string{"SetAutomationTeamSnapshotGrant"}
			}
			if verb == "revoke" {
				args = append(snapshotArgs1578("revoke", "grant1", "--yes", "--json"), "--app", autoApp)
				want = append(want, "RevokeAutomationTeamSnapshotGrant")
			}
			calls, _, err := runTeamTask(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range want {
				matches := callsFor(calls, op)
				if len(matches) != 1 {
					t.Fatalf("%s count%d", op, len(matches))
				}
				if matches[0].Session != "" {
					t.Fatalf("%s sent session %q", op, matches[0].Session)
				}
			}
		})
	}
}

func TestSnapshotGrant1578RejectWriteReceipts(t *testing.T) {
	for _, verb := range []string{"set", "revoke"} {
		good := snapshotGrant1578
		op, field := "SetAutomationTeamSnapshotGrant", "setAutomationTeamSnapshotGrant"
		if verb == "revoke" {
			good = endedSnapshot1578()
			op, field = "RevokeAutomationTeamSnapshotGrant", "revokeAutomationTeamSnapshotGrant"
		}
		cases := map[string]string{
			"null-payload":  "null",
			"empty-id":      strings.Replace(good, `"id":"grant1"`, `"id":""`, 1),
			"empty-app":     strings.Replace(good, `"appId":"capp100000000000000000000"`, `"appId":""`, 1),
			"empty-kind":    strings.Replace(good, `"triggerKind":"SCHEDULE"`, `"triggerKind":""`, 1),
			"empty-trigger": strings.Replace(good, `"triggerId":"schedule1"`, `"triggerId":""`, 1),
			"empty-grantor": strings.Replace(good, `"grantedBy":"user1"`, `"grantedBy":""`, 1),
			"empty-date":    strings.Replace(good, `"createdAt":"2026-11-03T00:00:00Z"`, `"createdAt":""`, 1),
			"other-kind":    strings.Replace(good, `"triggerKind":"SCHEDULE"`, `"triggerKind":"WEBHOOK"`, 1),
			"other-trigger": strings.Replace(good, `"triggerId":"schedule1"`, `"triggerId":"other"`, 1),
		}
		if verb == "set" {
			cases["already-revoked"] = endedSnapshot1578()
			cases["revoker-only"] = strings.Replace(good, `"revokedBy":null`, `"revokedBy":"user1"`, 1)
		} else {
			cases["other-id"] = strings.Replace(good, `"id":"grant1"`, `"id":"grant2"`, 1)
			cases["other-app"] = strings.Replace(good, `"appId":"capp100000000000000000000"`, `"appId":"anotherApp"`, 1)
			cases["not-revoked"] = snapshotGrant1578
			cases["no-time"] = strings.Replace(good, `"revokedAt":"2026-11-03T01:00:00Z"`, `"revokedAt":null`, 1)
			cases["no-revoker"] = strings.Replace(good, `"revokedBy":"user1"`, `"revokedBy":null`, 1)
		}
		for name, row := range cases {
			t.Run(verb+"/"+name, func(t *testing.T) {
				r := taskResponses()
				r["AutomationTeamSnapshotGrants"] = grantPage1578(snapshotGrant1578, 1, false)
				r[op] = `{"data":{"` + field + `":` + row + `}}`
				args := snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes", "--json")
				if verb == "revoke" {
					args = append(snapshotArgs1578("revoke", "grant1", "--yes", "--json"), "--app", autoApp)
				}
				calls, out, err := runTeamTask(t, r, args...)
				if err == nil || exitCodeFor(err) != exitcode.Error || out != "" {
					t.Fatalf("err=%v stdout=%q", err, out)
				}
				if len(callsFor(calls, op)) != 1 {
					t.Fatalf("write must be sent once: %+v", *calls)
				}
			})
		}
	}
}

func TestSnapshotGrant1578RejectMalformedPages(t *testing.T) {
	cases := map[string]string{
		"null-page":       `{"data":{"automationTeamSnapshotGrants":null}}`,
		"null-item":       grantPage1578("null", 1, false),
		"negative-total":  grantPage1578("", -1, false),
		"oversized":       grantPage1578(snapshotGrant1578+","+strings.ReplaceAll(snapshotGrant1578, "grant1", "grant2"), 2, false),
		"incomplete-item": grantPage1578(strings.Replace(snapshotGrant1578, `"grantedBy":"user1"`, `"grantedBy":""`, 1), 1, false),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AutomationTeamSnapshotGrants"] = body
			calls, out, err := runTeamTask(t, r, snapshotArgs1578("list", autoApp, "--limit", "1", "--json")...)
			if err == nil || exitCodeFor(err) != exitcode.Error || out != "" {
				t.Fatalf("err=%v stdout=%q", err, out)
			}
			if len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 1 || len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant"))+len(callsFor(calls, "SetAutomationTeamSnapshotGrant")) != 0 {
				t.Fatal(*calls)
			}
		})
	}
}

func TestSnapshotGrant1578RevokeLaterPage(t *testing.T) {
	srv := newAutoServer(t, map[string]func(map[string]any) autoReply{
		"AutomationTeamSnapshotGrants": func(v map[string]any) autoReply {
			if v["includeRevoked"] != true {
				t.Error("lookup must include ended rows")
			}
			switch v["offset"].(float64) {
			case 0:
				return autoReply{body: grantPage1578(strings.ReplaceAll(snapshotGrant1578, "grant1", "other"), 2, true)}
			case 1:
				return autoReply{body: grantPage1578(snapshotGrant1578, 2, false)}
			default:
				t.Error("unexpected offset", v)
				return autoReply{body: grantPage1578("", 2, false)}
			}
		},
		"RevokeAutomationTeamSnapshotGrant": func(v map[string]any) autoReply {
			if v["id"] != "grant1" {
				t.Error(v)
			}
			return autoReply{body: `{"data":{"revokeAutomationTeamSnapshotGrant":` + endedSnapshot1578() + `}}`}
		},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "--app", autoApp}, snapshotArgs1578("revoke", "grant1", "--yes", "--json")...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "grant1" || got["revokedAt"] == nil || srv.count("AutomationTeamSnapshotGrants") != 2 || srv.count("RevokeAutomationTeamSnapshotGrant") != 1 {
		t.Fatalf("%s queries%d writes%d", out, srv.count("AutomationTeamSnapshotGrants"), srv.count("RevokeAutomationTeamSnapshotGrant"))
	}
}

func TestSnapshotGrant1578ActiveAppPromptAndHumanOutput(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrants"] = grantPage1578(snapshotGrant1578, 1, false)
	srv, calls := attnServer(t, r)
	f, out, prompt := testFactoryTTY(t, "n\n")
	setActiveApp(t, autoApp)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, snapshotArgs1578("revoke", "grant1")...))
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Cancelled || out.Len() != 0 || len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 0 {
		t.Fatalf("err%v out%s calls%+v", err, out, *calls)
	}
	q := findCall(t, calls, "AutomationTeamSnapshotGrants")
	if q.Vars["appRef"] != autoApp {
		t.Fatal(q)
	}
	for _, part := range []string{"from the active App", autoApp, "capp100000000000000000000", "SCHEDULE", "schedule1"} {
		if !strings.Contains(prompt.String(), part) {
			t.Fatalf("prompt lacks %q: %s", part, prompt)
		}
	}
	for _, verb := range []string{"set", "list"} {
		t.Run(verb, func(t *testing.T) {
			rs := taskResponses()
			rs["AutomationTeamSnapshotGrants"] = grantPage1578(snapshotGrant1578, 1, false)
			rs["SetAutomationTeamSnapshotGrant"] = `{"data":{"setAutomationTeamSnapshotGrant":` + snapshotGrant1578 + `}}`
			args := snapshotArgs1578("list", autoApp)
			if verb == "set" {
				args = snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes")
			}
			_, text, err := runTeamTask(t, rs, args...)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"grant1", "capp100000000000000000000", "SCHEDULE", "schedule1"} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s lacks%q", text, want)
				}
			}
			if verb == "set" && (!strings.Contains(text, "revoked: -") || !strings.Contains(text, "granted by: user1")) {
				t.Fatal(text)
			}
		})
	}
}

func TestSnapshotGrant1578ContinuationNote(t *testing.T) {
	r := taskResponses()
	r["AutomationTeamSnapshotGrants"] = grantPage1578(snapshotGrant1578, 3, true)
	calls, out, stderr, err := runTeamTaskAll1524(t, r, snapshotArgs1578("list", autoApp, "--limit", "1", "--offset", "1", "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if len(callsFor(calls, "AutomationTeamSnapshotGrants")) != 1 || !strings.Contains(stderr, "--limit 1 --offset 2") {
		t.Fatalf("stderr=%s calls=%+v", stderr, *calls)
	}
	var page struct {
		Items   []map[string]any `json:"items"`
		Total   int              `json:"total"`
		HasMore bool             `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Total != 3 || !page.HasMore {
		t.Fatal(out)
	}
}
