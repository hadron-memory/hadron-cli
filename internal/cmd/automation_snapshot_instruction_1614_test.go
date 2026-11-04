package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type snapshotCall1614 struct {
	OperationName, Query string
	Variables            map[string]any
	Session              string
}

func snapshotServer1614(t *testing.T, reply func(snapshotCall1614) string) (*httptest.Server, *[]snapshotCall1614) {
	t.Helper()
	calls := []snapshotCall1614{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c snapshotCall1614
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Error(err)
		}
		c.Session = r.Header.Get("X-Hadron-Session")
		calls = append(calls, c)
		fmt.Fprint(w, reply(c))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}
func TestSnapshotInstruction1614ListPaging(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		t.Run(fmt.Sprint(instructed), func(t *testing.T) {
			teamGitDir(t)
			op := "AutomationTeamSnapshotGrants"
			if instructed {
				op += "Instructed"
			}
			srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
				if c.OperationName != op {
					t.Errorf("op=%s", c.OperationName)
				}
				if c.Variables["appRef"] != autoApp || c.Variables["limit"] != float64(20) {
					t.Errorf("vars=%+v", c.Variables)
				}
				if instructed {
					v, ok := c.Variables["instruction"].(map[string]any)
					if !ok || v["kind"] != "LIVE_CHAT" || v["ref"] != "client: 7 " || !strings.Contains(c.Query, "instruction: $instruction") || c.Session != "s-new" {
						t.Errorf("instruction/header lost: %+v", c)
					}
				} else if _, ok := c.Variables["instruction"]; ok || strings.Contains(c.Query, "instruction") || c.Session != "" {
					t.Errorf("legacy changed: %+v", c)
				}
				row, more := snapshotGrant1578, true
				if c.Variables["offset"] == float64(1) {
					row = strings.ReplaceAll(row, "grant1", "grant2")
					more = false
				}
				return fmt.Sprintf(`{"data":{"automationTeamSnapshotGrants":{"items":[%s],"total":2,"hasMore":%t}}}`, row, more)
			})
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := snapshotArgs1578("list", autoApp, "--json", "--server", srv.URL)
			if instructed {
				args = append(args, "--instruction", "live-chat:client: 7 ")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 2 || !strings.Contains(out.String(), `"id": "grant2"`) {
				t.Fatalf("calls=%d out=%s", len(*calls), out.String())
			}
		})
	}
}
func TestSnapshotInstruction1614RevokeWire(t *testing.T) {
	teamGitDir(t)
	ended := strings.ReplaceAll(snapshotGrant1578, `"revokedAt":null,"revokedBy":null`, `"revokedAt":"2026-11-04T00:00:00Z","revokedBy":"user1"`)
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		v, ok := c.Variables["instruction"].(map[string]any)
		if !ok || v["ref"] != "client:7" || c.Session != "s-new" || !strings.Contains(c.Query, "instruction: $instruction") {
			t.Errorf("lost instruction: %+v", c)
		}
		switch c.OperationName {
		case "AutomationTeamSnapshotGrantsInstructed":
			return `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":1,"hasMore":false}}}`
		case "RevokeAutomationTeamSnapshotGrantInstructed":
			if c.Variables["id"] != "grant1" {
				t.Errorf("wrong grant %+v", c)
			}
			return `{"data":{"revokeAutomationTeamSnapshotGrant":` + ended + `}}`
		default:
			t.Errorf("unexpected op %s", c.OperationName)
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
	if len(*calls) != 2 || !strings.Contains(out.String(), `"revokedBy": "user1"`) {
		t.Fatalf("calls=%+v out=%s", *calls, out.String())
	}
}
func TestSnapshotInstruction1614EarlyUsage(t *testing.T) {
	for _, verb := range []string{"list", "revoke"} {
		for _, raw := range []string{"", "task:", "bad:ref"} {
			t.Run(verb+raw, func(t *testing.T) {
				srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string { t.Error("call before usage refusal"); return `{}` })
				target := autoApp
				if verb == "revoke" {
					target = "grant1"
				}
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(snapshotArgs1578(verb, target, "--app", autoApp, "--instruction="+raw, "--server", srv.URL))
				err := root.Execute()
				if exitCodeFor(err) != exitcode.Usage || err == nil || strings.Contains(err.Error(), "unknown flag") || len(*calls) != 0 {
					t.Fatalf("err=%v calls=%+v", err, *calls)
				}
			})
		}
	}
}
