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
				} else if _, ok := c.Variables["instruction"]; ok || strings.Contains(c.Query, "instruction") || c.Session != "" || len(c.Variables) != 3 {
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
			if c.Variables["includeRevoked"] != true || len(c.Variables) != 5 {
				t.Errorf("lookup vars: %+v", c.Variables)
			}
			return `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":1,"hasMore":false}}}`
		case "RevokeAutomationTeamSnapshotGrantInstructed":
			if c.Variables["id"] != "grant1" || len(c.Variables) != 2 {
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

func TestSnapshotInstruction1614UnconfirmedZeroRequests(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
				return `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":1,"hasMore":false}}}`
			})
			f, _ := testFactory(t)
			want := exitcode.Usage
			if tty {
				f, _, _ = testFactoryTTY(t, "n\n")
				want = exitcode.Cancelled
			}
			root := NewRootCmd(f)
			root.SetArgs(snapshotArgs1578("revoke", "grant1", "--app", autoApp, "--instruction", "task:one", "--server", srv.URL))
			err := root.Execute()
			if exitCodeFor(err) != want || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}
func TestSnapshotInstruction1614SinglePreconfirm(t *testing.T) {
	ended := strings.ReplaceAll(snapshotGrant1578, `"revokedAt":null,"revokedBy":null`, `"revokedAt":"2026-11-04T00:00:00Z","revokedBy":"user1"`)
	f, out, stderr := testFactoryTTY(t, "y\n")
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		if !strings.Contains(stderr.String(), "one event per page") || !strings.Contains(stderr.String(), "grant1") || !strings.Contains(stderr.String(), autoApp) {
			t.Errorf("first request before honest preconfirm: %s", stderr.String())
		}
		if strings.HasPrefix(c.OperationName, "AutomationTeamSnapshotGrants") {
			return `{"data":{"automationTeamSnapshotGrants":{"items":[` + snapshotGrant1578 + `],"total":1,"hasMore":false}}}`
		}
		return `{"data":{"revokeAutomationTeamSnapshotGrant":` + ended + `}}`
	})
	root := NewRootCmd(f)
	root.SetArgs(snapshotArgs1578("revoke", "grant1", "--app", autoApp, "--instruction", "live-chat:one", "--server", srv.URL, "--json"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.Contains(out.String(), `"revokedBy": "user1"`) {
		t.Fatalf("calls=%+v out=%s", *calls, out.String())
	}
}

func TestSnapshotInstruction1614RefusalOnce(t *testing.T) {
	for _, stage := range []string{"list-first", "list-later", "revoke-lookup", "revoke-write"} {
		t.Run(stage, func(t *testing.T) {
			teamGitDir(t)
			srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
				if c.Session != "s-new" {
					t.Errorf("worker downgraded: %+v", c)
				}
				if c.OperationName == "RevokeAutomationTeamSnapshotGrantInstructed" || stage == "list-first" || stage == "revoke-lookup" || c.Variables["offset"] == float64(1) {
					return memberGQLError("refused", "FORBIDDEN")
				}
				more := stage == "list-later"
				return fmt.Sprintf(`{"data":{"automationTeamSnapshotGrants":{"items":[%s],"total":2,"hasMore":%t}}}`, snapshotGrant1578, more)
			})
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := snapshotArgs1578("list", autoApp)
			if strings.HasPrefix(stage, "revoke") {
				args = snapshotArgs1578("revoke", "grant1", "--app", autoApp, "--yes")
			}
			args = append(args, "--server", srv.URL, "--instruction", "task:one", "--json")
			root.SetArgs(args)
			err := root.Execute()
			n := 1
			if stage == "list-later" || stage == "revoke-write" {
				n = 2
			}
			if exitCodeFor(err) != exitcode.Forbidden || len(*calls) != n || out.Len() != 0 {
				t.Fatalf("err=%v calls=%+v out=%s", err, *calls, out.String())
			}
		})
	}
}
func TestSnapshotInstruction1614MissingOrEnded(t *testing.T) {
	for _, ended := range []bool{false, true} {
		t.Run(fmt.Sprint(ended), func(t *testing.T) {
			row := ""
			if ended {
				row = strings.ReplaceAll(snapshotGrant1578, `"revokedAt":null,"revokedBy":null`, `"revokedAt":"2026-11-04T00:00:00Z","revokedBy":"user1"`)
			}
			srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
				if c.OperationName != "AutomationTeamSnapshotGrantsInstructed" {
					t.Errorf("unexpected revoke %s", c.OperationName)
				}
				return `{"data":{"automationTeamSnapshotGrants":{"items":[` + row + `],"total":1,"hasMore":false}}}`
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(snapshotArgs1578("revoke", "grant1", "--app", autoApp, "--yes", "--instruction", "live-chat:one", "--server", srv.URL))
			err := root.Execute()
			want := 0
			if !ended {
				want = exitcode.NotFound
			}
			if exitCodeFor(err) != want || len(*calls) != 1 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestSnapshotInstruction1614RebindBetweenPages(t *testing.T) {
	teamGitDir(t)
	n := 0
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		n++
		want := "s-new"
		if n > 1 {
			want = ""
		}
		if c.Session != want {
			t.Errorf("session %q want %q", c.Session, want)
		}
		if n == 1 {
			rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "successor" })
		}
		row := snapshotGrant1578
		if n > 1 {
			row = strings.ReplaceAll(row, "grant1", "grant2")
		}
		return fmt.Sprintf(`{"data":{"automationTeamSnapshotGrants":{"items":[%s],"total":2,"hasMore":%t}}}`, row, n == 1)
	})
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(snapshotArgs1578("list", autoApp, "--instruction", "task:one", "--server", srv.URL, "--json"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.Contains(out.String(), `"id": "grant2"`) {
		t.Fatalf("calls=%+v out=%s", *calls, out.String())
	}
}
func TestSnapshotInstruction1614SetRemainsPersonOnly(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"automation", "team-snapshot-grant", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Flags().Lookup("instruction") != nil {
		t.Fatal("set gained instruction flag")
	}
}
