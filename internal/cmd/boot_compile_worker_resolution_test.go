package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

func c1ResolveRun(t *testing.T, mode, ref string, flags ...string) (*[]attnCall, string, error) {
	t.Helper()
	writeTeamBinding(t)
	responses := c1Responses()
	responses["Workers"] = `{"data":{"workers":{"total":1,"items":[` + irisWorkerJSON + `]}}}`
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	switch mode {
	case "unbound":
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	case "corrupt":
		if err := os.WriteFile(path, []byte("invalid binding JSON"), 0600); err != nil {
			t.Fatal(err)
		}
	case "foreign":
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = []byte(strings.ReplaceAll(string(raw), srv.URL, "https://foreign.example"))
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"--server", srv.URL, "team", "worker", "compile", ref}
	root.SetArgs(append(args, flags...))
	err := root.Execute()
	return calls, out.String(), err
}

func TestC1WorkerNameRequiresUsableScopeBeforeRequests(t *testing.T) {
	for _, mode := range []string{"unbound", "corrupt", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			calls, out, err := c1ResolveRun(t, mode, "Iris")
			if err == nil || len(*calls) != 0 || out != "" {
				t.Fatalf("unusable scope lost: %v calls=%v out=%s", err, *calls, out)
			}
			if mode == "corrupt" && !strings.Contains(err.Error(), "corrupt") {
				t.Fatalf("binding cause lost: %v", err)
			}
			if mode == "unbound" && !strings.Contains(err.Error(), "--app") {
				t.Fatalf("no context remedy lost: %v", err)
			}
		})
	}
}

func TestC1WorkerNameScopeSourceAndResolvedIdentity(t *testing.T) {
	for _, tc := range []struct {
		mode, source, app string
		flags             []string
	}{
		{"bound", "from the worktree binding", "capp100000000000000000000", nil},
		{"corrupt", "from --app", "hrn:app:acme.com:eng-team", []string{"--app", "hrn:app:acme.com:eng-team"}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			calls, out, err := c1ResolveRun(t, tc.mode, "Iris", tc.flags...)
			if err != nil {
				t.Fatal(err)
			}
			list := c2Mutation(t, calls, "Workers")
			// Workers (slim name scan), GetWorker (the match, cli#1459), compile.
			if list.Vars["appRef"] != tc.app || len(*calls) != 3 {
				t.Fatalf("name scan was unscoped: %s", list.RawVars)
			}
			mutation := c2Mutation(t, calls, "CompileWorkerBootPrompt")
			if mutation.Vars["workerRef"] != "wkr1" || mutation.Session != "" {
				t.Fatalf("wrong compiled actor: %s", mutation.RawVars)
			}
			if !strings.Contains(out, tc.source) || strings.Contains(out, "explicit worker reference") {
				t.Fatalf("misleading source: %s", out)
			}
		})
	}
}

func TestC1ExplicitWorkerRefIgnoresUnrelatedBindingAndApp(t *testing.T) {
	for _, ref := range []string{"hrn:worker:acme.com:eng-team:iris", "0123456789abcdef0123456789abcdef"} {
		for _, mode := range []string{"unbound", "corrupt", "foreign"} {
			t.Run(ref+mode, func(t *testing.T) {
				calls, out, err := c1ResolveRun(t, mode, ref, "--app", "hrn:app:acme.com:unrelated")
				if err != nil {
					t.Fatal(err)
				}
				c2Mutation(t, calls, "GetWorker")
				c2Mutation(t, calls, "CompileWorkerBootPrompt")
				if len(*calls) != 2 || !strings.Contains(out, "explicit worker reference") {
					t.Fatalf("explicit ref used ambient App: calls=%v out=%s", *calls, out)
				}
			})
		}
	}
}
