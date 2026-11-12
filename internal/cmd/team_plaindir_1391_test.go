//go:build !windows

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1391 (S2 of server#1800, ruling 0230 = B): worker sessions start,
// resume and shed from a directory that is not a git checkout. The binding
// cache then lives in the directory's own .hadron-team/, under the same lock,
// and the directory binds one worker.

const plainCache1391 = ".hadron-team"

// plainDir1391 is a directory with no .git and no HADRON_TEAM_GIT_DIR, and a
// `git` that answers as it does there — so the test does not depend on where
// the temp dir happens to sit.
func plainDir1391(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: not a git repository (or any of the parent directories): .git' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { // #nosec G306 — a test shim must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv(team.GitDirEnv, "")
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func startStubs1391() map[string]string {
	free := `{"data":{"workers":{"total":2,"items":[` + withLiveness(irisWorkerJSON, "false") + `,` + retiredWorkerJSON + `]}}}`
	return map[string]string{
		"GetUser":          `{"data":{"user":null}}`,
		"Workers":          free,
		"WorkersRoster":    free,
		"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
		"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + endedSessionJSON + `]}}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
		"WorkerReporting":  `{"data":{"worker":{"id":"wkr1","hasLiveSession":true,"reportsTo":null,"reportsToRetired":false}}}`,
	}
}

func readPlainBinding1391(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, plainCache1391, "hadron-team-session.json"))
	if err != nil {
		t.Fatalf("the plain directory must hold the binding cache: %v", err)
	}
	var b map[string]any
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// Acceptance 1: start --as from a plain directory binds, caches, and whoami
// then reports the worker from that cache.
func TestPlainDir1391StartBindsAndWhoamiReportsTheWorker(t *testing.T) {
	dir := plainDir1391(t)
	gql, captured := captureGraphQL(t, startStubs1391())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--tool", "claude-code", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("start from a plain directory must bind: %v", err)
	}
	if _, called := captured["StartTeamSession"]; !called {
		t.Fatal("the server bind is the source of truth and must happen")
	}
	if b := readPlainBinding1391(t, dir); b["sessionId"] != "s-new" || b["workerName"] != "Iris" {
		t.Fatalf("cached binding: %v", b)
	}
	if !strings.Contains(out.String(), filepath.Join(plainCache1391, "hadron-team-session.json")) {
		t.Errorf("start must report where it cached the binding: %s", out.String())
	}
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "whoami", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	var who map[string]any
	if err := json.Unmarshal([]byte(out2.String()), &who); err != nil {
		t.Fatalf("whoami json: %v (%s)", err, out2.String())
	}
	if who["workerName"] != "Iris" || who["sessionId"] != "s-new" {
		t.Fatalf("whoami must report the worker from the plain directory's cache: %s", out2.String())
	}
}

// Acceptance 2a: a resume redeems one claim from a plain directory and caches.
func TestPlainDir1391ResumeRedeemsAndCaches(t *testing.T) {
	dir := plainDir1391(t)
	gql, captured := captureGraphQL(t, resumeStubs("RESUMED"))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--tool", "claude-code", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a resume from a plain directory must bind: %v", err)
	}
	if _, called := captured["ResumeTeamSession"]; !called {
		t.Fatal("the claim must be redeemed")
	}
	if b := readPlainBinding1391(t, dir); b["sessionId"] != "s-new" || b["workerId"] != "wkr1" {
		t.Fatalf("cached binding: %v", b)
	}
}

// Acceptance 2b: shed writes its checkpoint and claim from a plain directory,
// and its cooldown stamp sits beside the cached binding.
func TestPlainDir1391ShedWritesCheckpointAndClaim(t *testing.T) {
	dir := plainDir1391(t)
	if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plainCache1391, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	url, rig := shedServer(t, nil)
	out, _, err := runShed(t, url, "--json")
	if err != nil {
		t.Fatalf("shed from a plain directory: %v", err)
	}
	var d struct {
		Status string `json:"status"`
		Claim  string `json:"claim"`
	}
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.Status != "written" || d.Claim != "written" {
		t.Fatalf("want written/written, got %s (%v)", out, e)
	}
	if rig.count("CreateNode") != 1 || rig.count("CreateResumeClaim") != 1 {
		t.Fatalf("checkpoint and claim must both be written: %v", rig.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, plainCache1391, "hadron-team-shed.json")); err != nil {
		t.Fatalf("the cooldown stamp belongs beside the cached binding: %v", err)
	}
}

// Acceptance 3: unbound, unclaimed, in a plain directory, a post refuses with
// the typed unbound error and sends nothing — never posts as the human.
func TestPlainDir1391UnboundPostRefusesWithNoWrite(t *testing.T) {
	dir := plainDir1391(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatHumanMsgJSON + `}}`,
		"TeamAppIdentity":       teamAppIdentityJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "hi", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.NotFound {
		t.Fatalf("exit %d, want NotFound: %v", code, err)
	}
	if _, posted := captured["CreateTeamChatMessage"]; posted {
		t.Fatal("an unbound post must never reach the server: it would be attributed to the human")
	}
	if _, err := os.Stat(filepath.Join(dir, plainCache1391)); !os.IsNotExist(err) {
		t.Fatal("a refused post must create no cache")
	}
}

// Acceptance 5 by refusal (ruling 0230): a second worker's start in a plain
// directory that caches another worker's binding refuses before the server
// is asked — with --force too, which must not end the first worker's session.
func TestPlainDir1391SecondWorkerStartIsRefusedBeforeAnyWrite(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "plain"
		if force {
			name = "force"
		}
		t.Run(name, func(t *testing.T) {
			dir := plainDir1391(t)
			if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(dir, plainCache1391, "hadron-team-session.json")
			if err := os.WriteFile(cache, []byte(bindingWithTeamFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			// Bob is a DIFFERENT worker (wkr2): with a worker the server
			// resolves to Iris's id, the lock-time check would rightly see a
			// rebind of the same worker, and the pre-check would be untested.
			bob := strings.NewReplacer(`"id":"wkr1"`, `"id":"wkr2"`, `:iris"`, `:bob"`, `"slug":"iris"`, `"slug":"bob"`, `"name":"Iris"`, `"name":"Bob"`, `You are Iris.`, `You are Bob.`).Replace(withLiveness(irisWorkerJSON, "false"))
			bobSession := strings.ReplaceAll(startedSessionJSON, `"workerId":"wkr1"`, `"workerId":"wkr2"`)
			stubs := startStubs1391()
			stubs["GetWorker"] = `{"data":{"worker":` + bob + `}}`
			stubs["Workers"] = `{"data":{"workers":{"total":1,"items":[` + bob + `]}}}`
			stubs["WorkersRoster"] = stubs["Workers"]
			stubs["StartTeamSession"] = `{"data":{"startSession":` + bobSession + `}}`
			stubs["EndTeamSession"] = `{"data":{"endSession":` + startedSessionJSON + `}}`
			gql, captured := captureGraphQL(t, stubs)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "session", "start", "--as", "Bob", "--server", gql.URL}
			if force {
				args = append(args, "--force")
			}
			root.SetArgs(args)
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Conflict {
				t.Fatalf("exit %d, want Conflict: %v", code, err)
			}
			if err == nil || !strings.Contains(err.Error(), "Iris") || !strings.Contains(err.Error(), "one worker") {
				t.Errorf("the refusal must name the directory's worker and the rule: %v", err)
			}
			for _, op := range []string{"StartTeamSession", "EndTeamSession"} {
				if _, called := captured[op]; called {
					t.Errorf("%s must not be sent: the first worker's session is not this start's to touch", op)
				}
			}
			if raw, _ := os.ReadFile(cache); string(raw) != bindingWithTeamFixture {
				t.Error("the first worker's binding must be left byte-identical")
			}
		})
	}
}

// Gizelle's LOCAL F1 on #1968: a worker NAME is App-scoped and a URN's last
// segment is only a slug, so "Iris" (or hrn:worker:<root>:<other-app>:iris)
// may resolve to ANOTHER App's Iris. The directory check must decide on the
// resolved worker's id, before --force ends the cached session and before any
// start — never on the name or slug alone.
func TestPlainDir1391SameNameWorkerOfAnotherAppIsRefusedBeforeAnyMutation(t *testing.T) {
	for _, ref := range []string{"Iris", "hrn:worker:acme.com:other-team:iris"} {
		for _, force := range []bool{false, true} {
			name := ref + "/plain"
			if force {
				name = ref + "/force"
			}
			t.Run(name, func(t *testing.T) {
				dir := plainDir1391(t)
				if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
					t.Fatal(err)
				}
				cache := filepath.Join(dir, plainCache1391, "hadron-team-session.json")
				if err := os.WriteFile(cache, []byte(bindingWithTeamFixture), 0o600); err != nil {
					t.Fatal(err)
				}
				other := strings.NewReplacer(`"id":"wkr1"`, `"id":"wkr2"`, `capp100000000000000000000`, `otherapp`, `:eng-team:iris`, `:other-team:iris`).Replace(withLiveness(irisWorkerJSON, "false"))
				stubs := startStubs1391()
				stubs["GetWorker"] = `{"data":{"worker":` + other + `}}`
				stubs["GetWorkerInApp"] = `{"data":{"worker":` + other + `}}`
				stubs["Workers"] = `{"data":{"workers":{"total":1,"items":[` + other + `]}}}`
				stubs["WorkersRoster"] = stubs["Workers"]
				stubs["StartTeamSession"] = `{"data":{"startSession":` + strings.NewReplacer(`"workerId":"wkr1"`, `"workerId":"wkr2"`, `"id":"s-new"`, `"id":"s-other"`).Replace(startedSessionJSON) + `}}`
				stubs["EndTeamSession"] = `{"data":{"endSession":` + startedSessionJSON + `}}`
				gql, captured := captureGraphQL(t, stubs)
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "session", "start", "--as", ref, "--app", "acme.com:other-team", "--server", gql.URL}
				if force {
					args = append(args, "--force")
				}
				root.SetArgs(args)
				err := root.Execute()
				if code := exitCodeFor(err); code != exitcode.Conflict {
					t.Fatalf("exit %d, want Conflict: %v", code, err)
				}
				if err == nil || !strings.Contains(err.Error(), "one worker") {
					t.Errorf("the refusal must state the one-worker rule: %v", err)
				}
				for _, op := range []string{"StartTeamSession", "EndTeamSession"} {
					if _, called := captured[op]; called {
						t.Errorf("%s must not be sent: the cached worker's session is not this start's to touch", op)
					}
				}
				if raw, _ := os.ReadFile(cache); string(raw) != bindingWithTeamFixture {
					t.Error("the cached binding must be left byte-identical")
				}
			})
		}
	}
}

// The other side of F1: the directory's OWN worker, named by name, still
// replaces its binding with --force (the documented worktree replacement).
func TestPlainDir1391SameWorkerForceRebindStillReplaces(t *testing.T) {
	dir := plainDir1391(t)
	if err := os.MkdirAll(filepath.Join(dir, plainCache1391), 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, plainCache1391, "hadron-team-session.json")
	if err := os.WriteFile(cache, []byte(strings.Replace(bindingWithTeamFixture, `"sessionId":"s-new"`, `"sessionId":"s-old"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := startStubs1391()
	stubs["GetWorkerInApp"] = `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`
	stubs["EndTeamSession"] = `{"data":{"endSession":` + endedSessionJSON + `}}`
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the directory's own worker must rebind with --force: %v", err)
	}
	for _, op := range []string{"EndTeamSession", "StartTeamSession"} {
		if _, called := captured[op]; !called {
			t.Errorf("%s must be sent on a forced rebind of the same worker", op)
		}
	}
	if got := readPlainBinding1391(t, dir)["sessionId"]; got != "s-new" {
		t.Errorf("the binding must name the new session, got %v", got)
	}
}
