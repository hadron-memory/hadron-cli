package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
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
			if err := os.WriteFile(filepath.Join(dir, plainCache1391, team.BindingFileName), []byte(tc.cache), 0o600); err != nil {
				t.Fatal(err)
			}
			wd, _ := os.Getwd() // the directory the command resolves, symlinks and all
			want := filepath.Join(wd, plainCache1391, team.BindingFileName)
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

// cli#1969 F7a (Gloria's BLIND F7): a resume in a plain directory held by one
// worker sends that worker as the expected worker (cli#1839), so another
// worker's waiting claim is refused by the server, and the cache is untouched.
func TestPlainDir1969ResumeSendsTheDirectorysWorker(t *testing.T) {
	dir := plainDir1391(t)
	if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, plainCache1391, team.BindingFileName)
	if err := os.WriteFile(cache, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := mismatchStubs("CLAIM")
	stubs["GetBoundSessionLiveness"] = `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":"2026-08-11T11:00:00Z","isLive":false}}}`
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Fatalf("exit %d, want the Conflict a worker mismatch maps to: %v", code, err)
	}
	if got := resumeInput(t, captured["ResumeTeamSession"])["expectedWorkerRef"]; got != "wkr1" {
		t.Errorf("expectedWorkerRef = %v, want the directory's wkr1", got)
	}
	if raw, _ := os.ReadFile(cache); string(raw) != bindingWithTeamFixture {
		t.Error("a refused resume must leave the directory's binding byte-identical")
	}
}

// Rico's §10 delta F1 (D2 survived) on #1968: under --force, a plain
// directory's cache with no worker id would end its session before the lock
// refused. The start refuses first: nothing is started or ended.
func TestPlainDir1969ForceOverACacheWithNoWorkerIDEndsNothing(t *testing.T) {
	dir := plainDir1391(t)
	if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, plainCache1391, team.BindingFileName)
	legacy := `{"sessionId":"s-old","workerName":"Iris","agentId":"agt1"}`
	if err := os.WriteFile(cache, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// A resolved worker whose id is empty too would "match" a cache with none:
	// the refusal must not rest on the two ids differing.
	noID := strings.Replace(withLiveness(irisWorkerJSON, "false"), `"id":"wkr1"`, `"id":""`, 1)
	stubs := startStubs1391()
	stubs["GetWorker"] = `{"data":{"worker":` + noID + `}}`
	stubs["Workers"] = `{"data":{"workers":{"total":1,"items":[` + noID + `]}}}`
	stubs["WorkersRoster"] = stubs["Workers"]
	stubs["EndTeamSession"] = `{"data":{"endSession":` + endedSessionJSON + `}}`
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--force", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Fatalf("exit %d, want Conflict: %v", code, err)
	}
	for _, op := range []string{"StartTeamSession", "EndTeamSession"} {
		if _, called := captured[op]; called {
			t.Errorf("%s must not be sent over a cache that cannot name its worker", op)
		}
	}
	if raw, _ := os.ReadFile(cache); string(raw) != legacy {
		t.Error("the cache must be left byte-identical")
	}
}
