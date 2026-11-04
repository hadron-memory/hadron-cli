package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const snapshotGrant1578 = `{"id":"grant1","appId":"capp100000000000000000000","triggerKind":"SCHEDULE","triggerId":"schedule1","grantedBy":"user1","createdAt":"2026-11-03T00:00:00Z","revokedAt":null,"revokedBy":null}`

func snapshotArgs1578(verb string, extra ...string) []string {
	return append([]string{"automation", "team-snapshot-grant", verb}, extra...)
}

func TestSnapshotGrant1578SetHeaderAndJSON(t *testing.T) {
	r := taskResponses()
	r["SetAutomationTeamSnapshotGrant"] = `{"data":{"setAutomationTeamSnapshotGrant":` + snapshotGrant1578 + `}}`
	calls, out, err := runTeamTask(t, r, snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes", "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "SetAutomationTeamSnapshotGrant")
	if c.Session != "" || c.Vars["appRef"] != autoApp || c.Vars["triggerKind"] != "SCHEDULE" || c.Vars["triggerId"] != "schedule1" {
		t.Fatalf("wrong request: %+v", c)
	}
	var got, want map[string]any
	if json.Unmarshal([]byte(out), &got) != nil || json.Unmarshal([]byte(snapshotGrant1578), &want) != nil {
		t.Fatal(out)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DTO: %s", out)
	}
	if len(callsFor(calls, "SetAutomationTeamSnapshotGrant")) != 1 {
		t.Fatal(*calls)
	}
}

func TestSnapshotGrant1578RefusalAndConfirm(t *testing.T) {
	for _, yes := range []bool{false, true} {
		t.Run(fmt.Sprint(yes), func(t *testing.T) {
			r := taskResponses()
			r["SetAutomationTeamSnapshotGrant"] = memberGQLError("Forbidden", "FORBIDDEN")
			args := snapshotArgs1578("set", autoApp, "--trigger-kind", "WEBHOOK", "--trigger-id", "hook1")
			if yes {
				args = append(args, "--yes")
			}
			calls, _, err := runTeamTask(t, r, args...)
			want := exitcode.Usage
			n := 0
			if yes {
				want = exitcode.Forbidden
				n = 1
			}
			if exitCodeFor(err) != want || len(callsFor(calls, "SetAutomationTeamSnapshotGrant")) != n {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestSnapshotGrant1578ListAllPages(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationTeamSnapshotGrants": func(v map[string]any) autoReply {
		offset := v["offset"].(float64)
		if _, present := v["includeRevoked"]; present {
			t.Error("unset include-revoked must be omitted")
		}
		row := snapshotGrant1578
		more := true
		if offset == 1 {
			row = strings.ReplaceAll(row, "grant1", "grant2")
			more = false
		} else if offset != 0 {
			t.Errorf("offset=%v", offset)
		}
		return autoReply{body: fmt.Sprintf(`{"data":{"automationTeamSnapshotGrants":{"items":[%s],"total":2,"hasMore":%t}}}`, row, more)}
	}})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", s.URL}, snapshotArgs1578("list", autoApp, "--json")...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total   int  `json:"total"`
		HasMore bool `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].ID != "grant1" || got.Items[1].ID != "grant2" || got.Total != 2 || got.HasMore || s.count("AutomationTeamSnapshotGrants") != 2 {
		t.Fatalf("%s calls%d", out, s.count("AutomationTeamSnapshotGrants"))
	}
}

func TestSnapshotGrant1578EmptyAndExplicitPage(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			body := `{"data":{"automationTeamSnapshotGrants":{"items":[],"total":0,"hasMore":false}}}`
			if explicit {
				body = `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":2,"hasMore":true}}}`
			}
			s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationTeamSnapshotGrants": fixed(body)})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := snapshotArgs1578("list", autoApp, "--json", "--include-revoked=false")
			if explicit {
				args = append(args, "--limit", "1")
			}
			root.SetArgs(append([]string{"--server", s.URL}, args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if s.count("AutomationTeamSnapshotGrants") != 1 || s.lastVars("AutomationTeamSnapshotGrants")["includeRevoked"] != false {
				t.Fatal(s.calls, s.vars)
			}
			if !explicit && !strings.Contains(out.String(), `"items": []`) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestSnapshotGrant1578BadArgsBeforeRequest(t *testing.T) {
	for _, extra := range [][]string{{"set", autoApp, "--trigger-kind", "MANUAL", "--trigger-id", "x", "--yes"}, {"set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id=", "--yes"}, {"list", autoApp, "--limit", "0"}, {"list", autoApp, "--limit", "201"}, {"list", autoApp, "--offset", "-1"}} {
		calls, _, err := runTeamTask(t, taskResponses(), snapshotArgs1578(extra[0], extra[1:]...)...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("%v err=%v calls=%v", extra, err, *calls)
		}
	}
}

func TestSnapshotGrant1578Revoke(t *testing.T) {
	for _, state := range []string{"live", "ended", "missing"} {
		t.Run(state, func(t *testing.T) {
			row := snapshotGrant1578
			ended := strings.ReplaceAll(row, `"revokedAt":null,"revokedBy":null`, `"revokedAt":"2026-11-03T01:00:00Z","revokedBy":"user1"`)
			if state == "ended" {
				row = ended
			}
			items := row
			if state == "missing" {
				items = ""
			}
			r := taskResponses()
			r["AutomationTeamSnapshotGrants"] = `{"data":{"automationTeamSnapshotGrants":{"items":[` + items + `],"total":1,"hasMore":false}}}`
			r["RevokeAutomationTeamSnapshotGrant"] = `{"data":{"revokeAutomationTeamSnapshotGrant":` + ended + `}}`
			calls, out, err := runTeamTask(t, r, append(snapshotArgs1578("revoke", "grant1", "--yes", "--json"), "--app", autoApp)...)
			n := 1
			if state != "live" {
				n = 0
			}
			if state == "missing" {
				if exitCodeFor(err) != exitcode.NotFound {
					t.Fatalf("err=%v", err)
				}
			} else if err != nil || !strings.Contains(out, `"revokedAt": "2026-11-03T01:00:00Z"`) {
				t.Fatalf("out=%s err=%v", out, err)
			}
			if len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != n {
				t.Fatal(*calls)
			}
			q := findCall(t, calls, "AutomationTeamSnapshotGrants")
			if q.Vars["appRef"] != autoApp || q.Vars["includeRevoked"] != true {
				t.Fatal(q)
			}
		})
	}
}

func TestSnapshotGrant1578ConfirmShowsAppAndTrigger(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			r := taskResponses()
			r["AutomationTeamSnapshotGrants"] = `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":1,"hasMore":false}}}`
			srv, calls := attnServer(t, r)
			f, out, prompt := testFactoryTTY(t, "n\n")
			root := NewRootCmd(f)
			args := snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1")
			wantApp := autoApp
			if revoke {
				args = append(snapshotArgs1578("revoke", "grant1"), "--app", autoApp)
				wantApp = "capp100000000000000000000"
			}
			root.SetArgs(append([]string{"--server", srv.URL}, args...))
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Cancelled {
				t.Fatalf("err=%v", err)
			}
			text := prompt.String() + out.String()
			for _, want := range []string{wantApp, "SCHEDULE", "schedule1"} {
				if !strings.Contains(text, want) {
					t.Fatalf("confirmation lacks%q: %s", want, text)
				}
			}
			if revoke && !strings.Contains(text, "from --app") {
				t.Fatal(text)
			}
			if len(callsFor(calls, "SetAutomationTeamSnapshotGrant"))+len(callsFor(calls, "RevokeAutomationTeamSnapshotGrant")) != 0 {
				t.Fatal(*calls)
			}
		})
	}
}

func TestSnapshotGrant1578RootRefusalCode(t *testing.T) {
	for _, verb := range []string{"set", "list", "revoke"} {
		t.Run(verb, func(t *testing.T) {
			op := "AutomationTeamSnapshotGrants"
			args := snapshotArgs1578("list", autoApp)
			replies := map[string]func(map[string]any) autoReply{}
			if verb == "set" {
				op = "SetAutomationTeamSnapshotGrant"
				args = snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes")
			}
			if verb == "revoke" {
				op = "RevokeAutomationTeamSnapshotGrant"
				args = append(snapshotArgs1578("revoke", "grant1", "--yes"), "--app", autoApp)
				replies["AutomationTeamSnapshotGrants"] = fixed(`{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":1,"hasMore":false}}}`)
			}
			replies[op] = fixed(memberGQLError("Forbidden", "FORBIDDEN"))
			srv := newAutoServer(t, replies)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "--json"}, args...))
			err := root.Execute()
			if err == nil || renderError(f, err) != exitcode.Forbidden {
				t.Fatalf("err=%v", err)
			}
			var envelope struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
				t.Fatal(err, out.String())
			}
			if envelope.Error.Code != 8 || envelope.Error.ServerCode != "FORBIDDEN" || srv.count(op) != 1 {
				t.Fatalf("%s calls%d", out, srv.count(op))
			}
		})
	}
}

func TestSnapshotGrant1578LostWriteAnswerNeverRetries(t *testing.T) {
	srv := newAutoServer(t, map[string]func(map[string]any) autoReply{"SetAutomationTeamSnapshotGrant": func(map[string]any) autoReply { return autoReply{abort: true} }})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, snapshotArgs1578("set", autoApp, "--trigger-kind", "SCHEDULE", "--trigger-id", "schedule1", "--yes")...))
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), "list grants") || out.Len() != 0 || srv.count("SetAutomationTeamSnapshotGrant") != 1 {
		t.Fatalf("err=%v out=%s count%d", err, out, srv.count("SetAutomationTeamSnapshotGrant"))
	}
}

func TestSnapshotGrant1578PagingFailuresHaveNoPartialOutput(t *testing.T) {
	for _, mode := range []string{"refusal", "no-progress", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			srv := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationTeamSnapshotGrants": func(v map[string]any) autoReply {
				if v["offset"].(float64) == 0 {
					return autoReply{body: `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":2,"hasMore":true}}}`}
				}
				if mode == "refusal" {
					return autoReply{body: memberGQLError("Forbidden", "FORBIDDEN")}
				}
				if mode == "no-progress" {
					return autoReply{body: `{"data":{"automationTeamSnapshotGrants":{"items":[],"total":2,"hasMore":true}}}`}
				}
				return autoReply{body: `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":2,"hasMore":false}}}`}
			}})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "--json"}, snapshotArgs1578("list", autoApp)...))
			err := root.Execute()
			if err == nil || out.Len() != 0 || srv.count("AutomationTeamSnapshotGrants") != 2 {
				t.Fatalf("err=%v out=%s count%d", err, out, srv.count("AutomationTeamSnapshotGrants"))
			}
		})
	}
}
