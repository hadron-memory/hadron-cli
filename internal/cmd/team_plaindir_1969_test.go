//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1969 F4 (Gloria's BLIND F4/NP1 on #1968): every "remove <path>" a plain
// directory's refusal prints names the SAME absolute path the bind's own
// refusal (writeBinding) does, not the relative .hadron-team/... spelling.
func TestPlainDir1969HeldRefusalsNameTheAbsoluteCache(t *testing.T) {
	legacy := `{"sessionId":"s-old","workerName":"Iris","agentId":"agt1"}`
	bob := func() map[string]string {
		w := strings.NewReplacer(`"id":"wkr1"`, `"id":"wkr2"`, `:iris"`, `:bob"`, `"slug":"iris"`, `"slug":"bob"`, `"name":"Iris"`, `"name":"Bob"`, `You are Iris.`, `You are Bob.`).Replace(withLiveness(irisWorkerJSON, "false"))
		s := startStubs1391()
		s["GetWorker"] = `{"data":{"worker":` + w + `}}`
		s["Workers"] = `{"data":{"workers":{"total":1,"items":[` + w + `]}}}`
		s["WorkersRoster"] = s["Workers"]
		return s
	}
	for _, tc := range []struct {
		name  string
		cache string
		args  []string
		stubs func() map[string]string
	}{
		{"start names another worker", bindingWithTeamFixture, []string{"--as", "Bob"}, bob},
		{"start resolves a cache with no worker id", legacy, []string{"--as", "Iris"}, startStubs1391},
		{"resume over a cache with no worker id", legacy, []string{"--tool", "claude-code"}, func() map[string]string {
			s := resumeStubs("RESUMED")
			s["GetTeamSession"] = `{"data":{"session":` + endedSessionJSON + `}}`
			s["GetBoundSessionLiveness"] = `{"data":{"session":{"id":"s-old","isLive":false}}}`
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := plainDir1391(t)
			if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, plainCache1391, "hadron-team-session.json"), []byte(tc.cache), 0o600); err != nil {
				t.Fatal(err)
			}
			wd, _ := os.Getwd() // the directory the command resolves, symlinks and all
			want := filepath.Join(wd, plainCache1391, "hadron-team-session.json")
			gql, _ := captureGraphQL(t, tc.stubs())
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "session", "start", "--server", gql.URL}, tc.args...))
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Conflict {
				t.Fatalf("want the held-directory Conflict, got %v", err)
			}
			if !strings.Contains(err.Error(), "remove "+want+")") {
				t.Errorf("the refusal must name the absolute cache %s: %v", want, err)
			}
		})
	}
}
