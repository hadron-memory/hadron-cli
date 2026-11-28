package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2446: --instruction on `automation grant list|revoke` (the
// automation READ grants), after the snapshot commands (#1614).

func readGrantArgs2446(verb string, extra ...string) []string {
	return append([]string{"automation", "grant", verb}, extra...)
}

func endedReadGrant2446() string {
	return strings.ReplaceAll(readGrant1566, `"revokedAt":null,"revokedBy":null`, `"revokedAt":"2026-11-05T01:00:00Z","revokedBy":"user2"`)
}

func TestReadGrantInstruction2446Wire(t *testing.T) {
	for _, verb := range []string{"list", "revoke"} {
		for _, instructed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/instructed=%t", verb, instructed), func(t *testing.T) {
				teamGitDir(t)
				op := map[string]string{"list": "AutomationReadGrants", "revoke": "RevokeAutomationReadGrant"}[verb]
				if instructed {
					op += "Instructed"
				}
				srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
					if c.OperationName != op {
						t.Errorf("op=%s want %s", c.OperationName, op)
					}
					if instructed {
						v, ok := c.Variables["instruction"].(map[string]any)
						if !ok || v["kind"] != "LIVE_CHAT" || v["ref"] != "client:7" || !strings.Contains(c.Query, "instruction: $instruction") || c.Session != "s-new" {
							t.Errorf("instruction/session lost: %+v", c)
						}
					} else if _, ok := c.Variables["instruction"]; ok || strings.Contains(c.Query, "instruction") || c.Session != "" {
						t.Errorf("uninstructed request changed: %+v", c)
					}
					if verb == "list" {
						if c.Variables["appRef"] != autoApp || c.Variables["memoryRef"] != "hrn:mem:acme.com:notes" {
							t.Errorf("vars=%+v", c.Variables)
						}
						return `{"data":{"automationReadGrants":[` + readGrant1566 + `]}}`
					}
					if c.Variables["id"] != "rg1" {
						t.Errorf("vars=%+v", c.Variables)
					}
					return `{"data":{"revokeAutomationReadGrant":` + endedReadGrant2446() + `}}`
				})
				writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := readGrantArgs2446("list", "--app", autoApp, "--memory", "acme.com:notes")
				if verb == "revoke" {
					args = readGrantArgs2446("revoke", "rg1", "--yes")
				}
				args = append(args, "--json", "--server", srv.URL)
				if instructed {
					args = append(args, "--instruction", "live-chat:client:7")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if len(*calls) != 1 || !strings.Contains(out.String(), `"id": "rg1"`) {
					t.Fatalf("calls=%d out=%s", len(*calls), out.String())
				}
			})
		}
	}
}

func TestReadGrantInstruction2446EarlyUsageSendsNothing(t *testing.T) {
	cases := map[string][]string{
		"list, empty instruction":      readGrantArgs2446("list", "--app", autoApp, "--instruction="),
		"list, kind without ref":       readGrantArgs2446("list", "--app", autoApp, "--instruction", "task:"),
		"list, unknown kind":           readGrantArgs2446("list", "--app", autoApp, "--instruction", "bad:ref"),
		"list, no --app (ruling)":      readGrantArgs2446("list", "--memory", "acme.com:notes", "--instruction", "task:one"),
		"revoke, empty instruction":    readGrantArgs2446("revoke", "rg1", "--yes", "--instruction="),
		"revoke, kind without ref":     readGrantArgs2446("revoke", "rg1", "--yes", "--instruction", "task:"),
		"revoke, unknown kind":         readGrantArgs2446("revoke", "rg1", "--yes", "--instruction", "bad:ref"),
		"revoke, unconfirmed (no tty)": readGrantArgs2446("revoke", "rg1", "--instruction", "task:one"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			teamGitDir(t)
			srv, calls := snapshotServer1614(t, func(snapshotCall1614) string { t.Error("a request went out"); return `{}` })
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", srv.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage || strings.Contains(err.Error(), "unknown flag") || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

// Precedent (the snapshot commands, #1614): a worktree that cannot carry the
// call (no binding, or one for another server) does not block it. The call
// goes out ONCE as the signed-in person, with no session header and the
// instruction still on the wire, and a stderr note says the worker session was
// not used; the server judges it. Never a second attempt.
func TestReadGrantInstruction2446UnboundOrChangedBindingGoesOutOncePlain(t *testing.T) {
	for _, bind := range []string{"none", "another-server"} {
		for _, verb := range []string{"list", "revoke"} {
			t.Run(bind+"/"+verb, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
					if verb == "list" {
						return `{"data":{"automationReadGrants":[]}}`
					}
					return `{"data":{"revokeAutomationReadGrant":` + endedReadGrant2446() + `}}`
				})
				if bind == "another-server" {
					writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), "http://other.invalid")
				}
				f, _, stderr := testFactoryTTY(t, "")
				root := NewRootCmd(f)
				args := readGrantArgs2446("list", "--app", autoApp)
				if verb == "revoke" {
					args = readGrantArgs2446("revoke", "rg1", "--yes")
				}
				root.SetArgs(append(args, "--instruction", "task:one", "--json", "--server", srv.URL))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if len(*calls) != 1 || (*calls)[0].Session != "" || (*calls)[0].Variables["instruction"] == nil {
					t.Fatalf("calls=%+v", *calls)
				}
				// With no binding at all the call is simply the person's, without a
				// note; a binding that does not fit is named.
				if noted := strings.Contains(stderr.String(), "worker session is not used"); noted != (bind == "another-server") {
					t.Fatalf("note=%t for binding %q: %q", noted, bind, stderr.String())
				}
			})
		}
	}
}

func TestReadGrantInstruction2446RefusalShownOnceNotRetried(t *testing.T) {
	for _, verb := range []string{"list", "revoke"} {
		t.Run(verb, func(t *testing.T) {
			teamGitDir(t)
			srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
				if c.Session != "s-new" {
					t.Errorf("downgraded to the person: %+v", c)
				}
				return memberGQLError("refused: not your App", "FORBIDDEN")
			})
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := readGrantArgs2446("list", "--app", autoApp)
			if verb == "revoke" {
				args = readGrantArgs2446("revoke", "rg1", "--yes")
			}
			root.SetArgs(append(args, "--instruction", "task:one", "--json", "--server", srv.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Forbidden || len(*calls) != 1 || out.Len() != 0 {
				t.Fatalf("err=%v calls=%+v out=%s", err, *calls, out.String())
			}
			// Render the real --json error envelope: the server's code and its
			// specific refusal text must both reach the caller.
			renderError(f, err)
			var env struct {
				Error struct {
					Message    string `json:"message"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			// cli#3319: the command's deprecation notice rides stderr first; the envelope is what follows it.
			stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
			if !strings.HasPrefix(stderr, "warning: ") {
				t.Fatalf("the retirement notice goes to stderr first: %q", stderr)
			}
			if _, rest, ok := strings.Cut(stderr, "\n"); ok {
				stderr = rest
			}
			rendered := out.String() + stderr
			if jerr := json.Unmarshal([]byte(rendered), &env); jerr != nil || env.Error.ServerCode != "FORBIDDEN" || !strings.Contains(env.Error.Message, "refused: not your App") {
				t.Fatalf("envelope lost serverCode or message (%v): %s", jerr, rendered)
			}
		})
	}
}

func TestReadGrantInstruction2446SetStaysPersonOnly(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(readGrantArgs2446("set", autoApp, "--memory", "acme.com:notes", "--trigger-kind", "SCHEDULE", "--trigger-id", "s1", "--yes", "--instruction", "task:one"))
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("set must not take --instruction: %v", err)
	}
}

// A null element in the grant list is a malformed answer, never a panic: the
// generated rows are pointers (cli#2446 BLIND delta).
func TestReadGrantInstruction2446NullRowIsMalformedNotPanic(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		t.Run(fmt.Sprintf("instructed=%t", instructed), func(t *testing.T) {
			teamGitDir(t)
			srv, _ := snapshotServer1614(t, func(snapshotCall1614) string {
				return `{"data":{"automationReadGrants":[` + readGrant1566 + `,null]}}`
			})
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := readGrantArgs2446("list", "--app", autoApp, "--json", "--server", srv.URL)
			if instructed {
				args = append(args, "--instruction", "task:one")
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "null read grant") || out.Len() != 0 {
				t.Fatalf("err=%v out=%s", err, out.String())
			}
		})
	}
}
