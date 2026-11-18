package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestGrantInstructionConfirmedTarget2448(t *testing.T) {
	for _, change := range []string{"unchanged", "binding-app", "binding-removed", "binding-corrupt", "configured-app"} {
		t.Run(change, func(t *testing.T) {
			teamGitDir(t)
			c := managerCases2448()[5]
			srv, calls := snapshotServer1614(t, func(call snapshotCall1614) string {
				switch call.OperationName {
				case "GetBoundSessionEnd":
					return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
				case "TeamAppIdentity":
					return `{"data":{"app":{"id":"` + otherApp1594 + `","urn":"hrn:app:acme.com:other","name":"Other"}}}`
				case c.op + "Instructed":
					return `{"data":{"revokeAppGrant":` + c.answer + `}}`
				default:
					t.Errorf("unexpected operation %s", call.OperationName)
					return `{}`
				}
			})
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, _ := testFactory(t)
			cfg, err := f.Config()
			if err != nil {
				t.Fatal(err)
			}
			if change == "configured-app" {
				if err := cfg.Set("app", app1594); err != nil {
					t.Fatal(err)
				}
			}
			streams, _, stderr := output.TestTTY("y\n")
			streams.In = &managerPromptReader2448{answer: streams.In, before: func() {
				if len(*calls) != 0 {
					t.Fatalf("request before consent: %+v", *calls)
				}
				switch change {
				case "binding-app":
					rewriteBinding1592(t, func(b map[string]any) { b["appId"] = otherApp1594 })
				case "binding-removed":
					if err := os.Remove(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")); err != nil {
						t.Fatal(err)
					}
				case "binding-corrupt":
					if err := os.WriteFile(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"), []byte(`{broken`), 0600); err != nil {
						t.Fatal(err)
					}
				case "configured-app":
					if err := cfg.Set("app", otherApp1594); err != nil {
						t.Fatal(err)
					}
				}
			}}
			f.IOStreams = streams
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "grant", "rm", "wkr1", "WORKERS_STAFF", "--instruction", "live-chat:one", "--server", srv.URL, "--json"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			prompt := "Revoke grant WORKERS_STAFF from worker wkr1 in App " + app1594 + "?"
			if !strings.HasPrefix(stderr.String(), prompt) {
				t.Fatalf("wrong confirmed target: %q", stderr.String())
			}
			call := managerWrite2448(t, c, calls, c.op+"Instructed")
			if call.Variables["appRef"] != app1594 {
				t.Fatalf("prompt target changed: prompt=%q mutationApp=%v", stderr.String(), call.Variables["appRef"])
			}
			wantSession := ""
			if change == "unchanged" || change == "configured-app" {
				wantSession = "s-new"
			}
			if call.Session != wantSession {
				t.Fatalf("attribution changed: session=%q want=%q stderr=%q", call.Session, wantSession, stderr.String())
			}
			if call.Variables["instruction"] == nil {
				t.Fatal("person fallback dropped claim")
			}
			for _, read := range *calls {
				if read.OperationName != c.op+"Instructed" && (read.Session != "" || read.Variables["instruction"] != nil) {
					t.Fatalf("lookup claimed authority: %+v", read)
				}
			}
		})
	}
}

// A memory-only legacy binding cannot resolve an App before consent without a
// request. Ruling #116489 requires an explicit/configured App for this one path.
func TestGrantInstructionLegacyPromptRefusal2448(t *testing.T) {
	teamGitDir(t)
	c := managerCases2448()[5]
	srv, calls := snapshotServer1614(t, func(call snapshotCall1614) string {
		switch call.OperationName {
		case "GetBoundSessionEnd":
			return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
		case "TeamMemoryApp":
			return `{"data":{"memory":{"id":"m1","appId":"` + app1594 + `"}}}`
		default:
			return `{"data":{"revokeAppGrant":` + c.answer + `}}`
		}
	})
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	rewriteBinding1592(t, func(b map[string]any) { delete(b, "appId"); b["teamMemory"] = "hrn:mem:acme.com:team" })
	f, _ := testFactory(t)
	streams, out, stderr := output.TestTTY("y\n")
	prompted := false
	streams.In = &managerPromptReader2448{answer: streams.In, before: func() { prompted = true }}
	f.IOStreams = streams
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "grant", "rm", "wkr1", "WORKERS_STAFF", "--instruction", "task:one", "--server", srv.URL, "--json"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--app") || !strings.Contains(err.Error(), "configured App") {
		t.Fatalf("legacy prompt must refuse with App remedy: %v", err)
	}
	if prompted || len(*calls) != 0 || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("legacy refusal after effects: prompt=%t calls=%+v out=%q stderr=%q", prompted, *calls, out.String(), stderr.String())
	}
}

func TestGrantInstructionLegacyPreservedPaths2448(t *testing.T) {
	for _, mode := range []string{"plain-interactive", "plain-yes", "instructed-yes", "explicit-app", "configured-app"} {
		t.Run(mode, func(t *testing.T) {
			teamGitDir(t)
			c := managerCases2448()[5]
			srv, calls := snapshotServer1614(t, func(call snapshotCall1614) string {
				switch call.OperationName {
				case "GetBoundSessionEnd":
					return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
				case "TeamMemoryApp":
					return `{"data":{"memory":{"id":"m1","appId":"` + app1594 + `"}}}`
				case "TeamAppIdentity":
					return teamAppIdentityJSON
				case c.op, c.op + "Instructed":
					return `{"data":{"revokeAppGrant":` + c.answer + `}}`
				default:
					t.Errorf("unexpected operation %s", call.OperationName)
					return `{}`
				}
			})
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			rewriteBinding1592(t, func(b map[string]any) { delete(b, "appId"); b["teamMemory"] = "hrn:mem:acme.com:team" })
			f, _ := testFactory(t)
			streams, _, stderr := output.TestTTY("y\n")
			f.IOStreams = streams
			if mode == "configured-app" {
				cfg, err := f.Config()
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Set("app", app1594); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"team", "grant", "rm", "wkr1", "WORKERS_STAFF", "--server", srv.URL, "--json"}
			instructed := !strings.HasPrefix(mode, "plain-")
			if instructed {
				args = append(args, "--instruction", "task:one")
			}
			if strings.HasSuffix(mode, "-yes") {
				args = append(args, "--yes")
			}
			if mode == "explicit-app" {
				args = append(args, "--app", app1594)
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			op := c.op
			if instructed {
				op += "Instructed"
			}
			call := managerWrite2448(t, c, calls, op)
			if call.Variables["appRef"] != app1594 || call.Session != "" {
				t.Fatalf("legacy target/attribution changed: %+v", call)
			}
			if instructed != (call.Variables["instruction"] != nil) {
				t.Fatalf("claim changed: %+v", call)
			}
			if mode == "explicit-app" || mode == "configured-app" {
				if !strings.HasPrefix(stderr.String(), "Revoke grant WORKERS_STAFF from worker wkr1 in App "+app1594+"?") {
					t.Fatalf("prompt target=%q", stderr.String())
				}
			}
		})
	}
}
