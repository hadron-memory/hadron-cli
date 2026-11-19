package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestSnapshotRevoke1612MalformedSecondPageNoPartialOrWrite(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"null-page", `{"data":{"automationTeamSnapshotGrants":null}}`, "the server returned no team snapshot grant page"},
		{"null-row", grantPage1578("null", 2, false), "the server returned a null team snapshot grant"},
		{"incomplete-row", grantPage1578(strings.Replace(strings.ReplaceAll(snapshotGrant1578, "grant1", "other"), `"createdAt":"2026-11-03T00:00:00Z"`, `"createdAt":""`, 1), 2, false), "the server returned an incomplete team snapshot grant"},
	}
	for _, verb := range []string{"list"} { // a person revoke no longer pages (cli#1619)
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

// The held-worker --instruction revoke still reads the whole App history before
// any mutation (the by-id read has no worker arm, cli#1619). A person's revoke
// no longer pages at all; see automation_snapshot_revoke_by_id_1619_test.go.
func TestSnapshotRevoke1612ExhaustsMoreThanTwentyRows(t *testing.T) {
	teamGitDir(t)
	rows := []string{}
	for n := 0; n < 27; n++ {
		id := fmt.Sprintf("history-%02d", n)
		if n == 1 {
			id = "grant1"
		}
		rows = append(rows, strings.ReplaceAll(snapshotGrant1578, "grant1", id))
	}
	offsets := []int{}
	writes := 0
	srv, _ := snapshotServer1614(t, func(c snapshotCall1614) string {
		switch c.OperationName {
		case "AutomationTeamSnapshotGrantsInstructed":
			if c.Variables["limit"] != float64(20) || c.Variables["includeRevoked"] != true {
				t.Errorf("history limit/include changed: %v", c.Variables)
			}
			offset := int(c.Variables["offset"].(float64))
			offsets = append(offsets, offset)
			end := offset + 11 // server may return fewer than requested
			if offset < 0 || offset >= len(rows) {
				t.Errorf("invalid offset %d", offset)
				return grantPage1578("", 27, false)
			}
			if end > len(rows) {
				end = len(rows)
			}
			return grantPage1578(strings.Join(rows[offset:end], ","), len(rows), end < len(rows))
		case "RevokeAutomationTeamSnapshotGrantInstructed":
			writes++
			if len(offsets) != 3 || offsets[2] != 22 {
				t.Error("mutation before full history exhaustion")
			}
			if c.Variables["id"] != "grant1" {
				t.Error(c.Variables)
			}
			return `{"data":{"revokeAutomationTeamSnapshotGrant":` + endedSnapshot1578() + `}}`
		default:
			t.Errorf("unexpected op %s (the instruction path never uses the by-id read)", c.OperationName)
			return `{}`
		}
	})
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(snapshotArgs1578("revoke", "grant1", "--app", autoApp, "--yes", "--instruction", "live-chat:client:7", "--json", "--server", srv.URL))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offsets, []int{0, 11, 22}) || writes != 1 {
		t.Fatalf("offsets%v writes%d", offsets, writes)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || len(dto) != 8 || dto["id"] != "grant1" || dto["revokedAt"] == nil {
		t.Fatalf("bad receipt %s: %v", out, err)
	}
}
