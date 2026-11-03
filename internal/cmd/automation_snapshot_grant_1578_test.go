package cmd

import (
	"encoding/json"
	"fmt"
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
	if len(got) != 8 || got["id"] != want["id"] || got["revokedAt"] != nil || got["revokedBy"] != nil {
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
			if exitcode.FromError(err) != want || len(callsFor(calls, "SetAutomationTeamSnapshotGrant")) != n {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestSnapshotGrant1578ListAllPages(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationTeamSnapshotGrants": func(v map[string]any) autoReply {
		offset := v["offset"].(float64)
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
		if exitcode.FromError(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("%v err=%v calls=%v", extra, err, *calls)
		}
	}
}
