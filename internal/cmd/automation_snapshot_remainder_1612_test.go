package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestSnapshotRevoke1612MalformedSecondPageNoPartialOrWrite(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"null-page", `{"data":{"automationTeamSnapshotGrants":null}}`, "the server returned no team snapshot grant page"},
		{"null-row", grantPage1578("null", 2, false), "the server returned a null team snapshot grant"},
		{"incomplete-row", grantPage1578(strings.Replace(strings.ReplaceAll(snapshotGrant1578, "grant1", "other"), `"createdAt":"2026-11-03T00:00:00Z"`, `"createdAt":""`, 1), 2, false), "the server returned an incomplete team snapshot grant"},
	}
	for _, verb := range []string{"list", "revoke"} {
		for _, tc := range cases {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				s := newAutoServer(t, map[string]func(map[string]any) autoReply{
					"AutomationTeamSnapshotGrants": func(v map[string]any) autoReply {
						if v["limit"] != float64(20) {
							t.Errorf("internal page size changed: %v", v)
						}
						if v["offset"] == float64(0) {
							return autoReply{body: grantPage1578(snapshotGrant1578, 2, true)}
						}
						if v["offset"] != float64(1) {
							t.Errorf("bad actual-row offset: %v", v)
						}
						return autoReply{body: tc.body}
					},
					"RevokeAutomationTeamSnapshotGrant": fixed(`{"data":{"revokeAutomationTeamSnapshotGrant":` + endedSnapshot1578() + `}}`),
				})
				f, out := testFactory(t) // isolate XDG before constructing root or any config helper
				args := snapshotArgs1578("list", autoApp, "--json")
				if verb == "revoke" {
					args = snapshotArgs1578("revoke", "grant1", "--yes", "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(append([]string{"--server", s.URL, "--app", autoApp}, args...))
				err := root.Execute()
				if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), tc.want) || out.Len() != 0 || s.count("AutomationTeamSnapshotGrants") != 2 || s.count("RevokeAutomationTeamSnapshotGrant") != 0 {
					t.Fatalf("err=%v out=%q queries%d writes%d", err, out.String(), s.count("AutomationTeamSnapshotGrants"), s.count("RevokeAutomationTeamSnapshotGrant"))
				}
			})
		}
	}
}

func TestSnapshotRevoke1612ExhaustsMoreThanTwentyRows(t *testing.T) {
	rows := []string{}
	for n := 0; n < 27; n++ {
		id := fmt.Sprintf("history-%02d", n)
		if n == 1 {
			id = "grant1"
		}
		rows = append(rows, strings.ReplaceAll(snapshotGrant1578, "grant1", id))
	}
	var s *autoServer
	s = newAutoServer(t, map[string]func(map[string]any) autoReply{
		"AutomationTeamSnapshotGrants": func(v map[string]any) autoReply {
			if v["limit"] != float64(20) || v["includeRevoked"] != true {
				t.Errorf("history limit/include changed: %v", v)
			}
			offset := int(v["offset"].(float64))
			end := offset + 11 // server may return fewer than requested
			if offset < 0 || offset >= len(rows) {
				t.Errorf("invalid offset %d", offset)
				return autoReply{body: grantPage1578("", 27, false)}
			}
			if end > len(rows) {
				end = len(rows)
			}
			return autoReply{body: grantPage1578(strings.Join(rows[offset:end], ","), len(rows), end < len(rows))}
		},
		"RevokeAutomationTeamSnapshotGrant": func(v map[string]any) autoReply {
			if s.count("AutomationTeamSnapshotGrants") != 3 || s.lastVars("AutomationTeamSnapshotGrants")["offset"] != float64(22) {
				t.Error("mutation before full history exhaustion")
			}
			if v["id"] != "grant1" {
				t.Error(v)
			}
			return autoReply{body: `{"data":{"revokeAutomationTeamSnapshotGrant":` + endedSnapshot1578() + `}}`}
		},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", s.URL, "--app", autoApp}, snapshotArgs1578("revoke", "grant1", "--yes", "--json")...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	offsets := []int{}
	for _, v := range s.vars["AutomationTeamSnapshotGrants"] {
		offsets = append(offsets, int(v["offset"].(float64)))
	}
	s.mu.Unlock()
	if !reflect.DeepEqual(offsets, []int{0, 11, 22}) || s.count("RevokeAutomationTeamSnapshotGrant") != 1 {
		t.Fatalf("offsets%v writes%d", offsets, s.count("RevokeAutomationTeamSnapshotGrant"))
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || len(dto) != 8 || dto["id"] != "grant1" || dto["revokedAt"] == nil {
		t.Fatalf("bad receipt %s: %v", out, err)
	}
}
