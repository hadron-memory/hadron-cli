package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Every current role subcommand must ask for its App, without making a
// worktree or worker binding sound like a prerequisite. Supply otherwise
// valid arguments so argument validation cannot hide the scope diagnostic.
func TestTeamRoleNoApp1722(t *testing.T) {
	commands := [][]string{
		{"list"}, {"get", "probe-role"},
		{"create", "probe-role", "--description", "probe"},
		{"update", "probe-role", "--description", "probe"},
		{"rm", "probe-role", "--yes"},
		{"ls"}, {"delete", "probe-role", "--yes"},
	}
	for _, location := range []string{"outside", "unbound"} {
		for _, args := range commands {
			for _, asJSON := range []bool{false, true} {
				mode := "text"
				if asJSON {
					mode = "json"
				}
				t.Run(location+"/"+args[0]+"/"+mode, func(t *testing.T) {
					f, out := testFactory(t) // empty isolated App config
					t.Chdir(t.TempDir())
					gitDir := ""
					if location == "unbound" {
						gitDir = t.TempDir()
					}
					t.Setenv(team.GitDirEnv, gitDir)
					var requests atomic.Int32
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.WriteHeader(http.StatusInternalServerError)
					}))
					defer srv.Close()
					errOut := captureErrOut(f)
					root := NewRootCmd(f)
					argv := append([]string{"team", "role"}, args...)
					argv = append(argv, "--server", srv.URL)
					if asJSON {
						argv = append(argv, "--json")
					}
					root.SetArgs(argv)
					err := root.Execute()
					if err == nil {
						t.Fatal("missing App must refuse")
					}
					if code := renderError(f, err); code != exitcode.Usage {
						t.Fatalf("exit %d: %v", code, err)
					}
					if requests.Load() != 0 {
						t.Fatalf("missing App made %d requests", requests.Load())
					}
					message := strings.TrimSpace(strings.TrimPrefix(errOut(), "hadron: "))
					if asJSON {
						var envelope struct {
							Error struct {
								Code    int    `json:"code"`
								Message string `json:"message"`
							} `json:"error"`
						}
						if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
							t.Fatal(err)
						}
						if envelope.Error.Code != exitcode.Usage || errOut() != "" {
							t.Fatalf("JSON error shape: %s stderr %q", out.String(), errOut())
						}
						message = envelope.Error.Message
					} else if out.String() != "" {
						t.Fatalf("unexpected stdout: %q", out.String())
					}
					const want = "no team App — pass --app <ref> (or set a default App context)"
					if message != want {
						t.Fatalf("message = %q; want %q", message, want)
					}
				})
			}
		}
	}
}

// Existing scope sources and their precedence must keep working, including
// explicit/config scopes outside git. JSON listing avoids decorating reads;
// assert the actual operation's appRef, not only a rendered label.
func TestTeamRoleScopeSources1722(t *testing.T) {
	const flagApp = "hrn:app:acme.com:flag-team"
	const configApp = "hrn:app:acme.com:config-team"
	const boundApp = "capp100000000000000000000"
	for _, tc := range []struct {
		name, flag, config, binding, want string
	}{
		{"flag outside", flagApp, "", "", flagApp},
		{"config outside", "", configApp, "", configApp},
		{"bound", "", "", boundApp, boundApp},
		{"flag wins", flagApp, configApp, boundApp, flagApp},
		{"config wins", "", configApp, boundApp, configApp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, out := testFactory(t)
			t.Chdir(t.TempDir())
			t.Setenv(team.GitDirEnv, "")
			if tc.config != "" {
				cfg, err := f.Config()
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Set("app", tc.config); err != nil {
					t.Fatal(err)
				}
			}
			if tc.binding != "" {
				dir := t.TempDir()
				t.Setenv(team.GitDirEnv, dir)
				if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(`{"appId":"`+tc.binding+`"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			srv, captured := captureGraphQL(t, map[string]string{
				"TeamRoles": `{"data":{"teamRoles":{"total":0,"items":[]}}}`,
			})
			root := NewRootCmd(f)
			args := []string{"team", "role", "list", "--json", "--server", srv.URL}
			if tc.flag != "" {
				args = append(args, "--app", tc.flag)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars struct {
				AppRef string `json:"appRef"`
			}
			if err := json.Unmarshal(captured["TeamRoles"], &vars); err != nil {
				t.Fatal(err)
			}
			if vars.AppRef != tc.want || strings.TrimSpace(out.String()) != "[]" {
				t.Fatalf("scope %q want %q; output %q", vars.AppRef, tc.want, out.String())
			}
		})
	}
}

func TestTeamRoleBindingErrorPreserved1722(t *testing.T) {
	f, _ := testFactory(t)
	dir := t.TempDir()
	t.Setenv(team.GitDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "list"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "is corrupt") || strings.Contains(err.Error(), "no team App") {
		t.Fatalf("corrupt binding was hidden: %v", err)
	}
}
