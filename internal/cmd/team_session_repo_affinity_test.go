package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #456 — `session start` warns when --repo sits outside the bound worker's
// role affinity (hadron-server#1024's `Worker.repos`).
//
// The shared irisWorkerJSON carries no `repos`, so it decodes to an EMPTY
// affinity — which per the server's contract means "never warn". That makes it
// the silent case by default, and means every warning test has to build its
// affinity explicitly or it would watch nothing happen and pass.
func withRepos(repos ...string) string {
	quoted := make([]string, 0, len(repos))
	for _, r := range repos {
		quoted = append(quoted, `"`+r+`"`)
	}
	out := strings.Replace(irisWorkerJSON, `"memoryId":"mw1"`,
		`"repos":[`+strings.Join(quoted, ",")+`],"memoryId":"mw1"`, 1)
	if out == irisWorkerJSON {
		panic("withRepos matched nothing — fixture shape changed")
	}
	return out
}

// stderrOf reads the factory's stderr buffer — testFactory returns stdout
// only, and this warning is deliberately not on stdout.
func stderrOf(t *testing.T, f *cmdutil.Factory) string {
	t.Helper()
	b, ok := f.IOStreams.ErrOut.(*strings.Builder)
	if !ok {
		t.Fatalf("stderr is not a *strings.Builder: %T", f.IOStreams.ErrOut)
	}
	return b.String()
}

func startWithAffinityStubs(worker string, extra map[string]string) map[string]string {
	m := map[string]string{
		"GetWorker":        `{"data":{"worker":` + worker + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// The mismatch: bound to a worker whose role claims one repo, --repo names
// another. Warns, and critically does NOT refuse.
func TestSessionStartWarnsOnRepoAffinityMismatch(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, startWithAffinityStubs(
		withRepos("hadron-memory/hadron-server"),
		map[string]string{"WorkersRoster": `{"data":{"workers":{"total":0,"items":[]}}}`},
	))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
	err := root.Execute()

	// WARN, NEVER REFUSE. The session started, the binding is written, and the
	// exit code is untouched — cross-repo work is legitimate.
	if err != nil {
		t.Fatalf("a mismatch must not fail the command: %v", err)
	}
	if code := exitcode.FromError(err); code != exitcode.OK {
		t.Errorf("exit code = %d, want OK", code)
	}
	if !strings.Contains(stderrOf(t, f), "hadron-memory/hadron-server") ||
		!strings.Contains(stderrOf(t, f), "hadron-memory/hadron-cli") {
		t.Errorf("the warning must name both the affinity and the --repo:\n%s", stderrOf(t, f))
	}
	if !strings.Contains(stderrOf(t, f), "nudge, not a refusal") {
		t.Errorf("the warning must say it is not a refusal:\n%s", stderrOf(t, f))
	}
	if !strings.Contains(out.String(), "✓ started session") {
		t.Errorf("the session must still start:\n%s", out.String())
	}
}

// THE PLACEMENT IS THE FEATURE. The bind receipt has always printed the role,
// so the information was never missing — it is missable, because the
// several-hundred-word briefing prints right after and pushes it off screen.
// A warning emitted BEFORE the briefing reproduces the bug being fixed.
func TestSessionStartWarningComesAfterTheBootBriefing(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, startWithAffinityStubs(
		withRepos("hadron-memory/hadron-server"),
		map[string]string{"WorkersRoster": `{"data":{"workers":{"total":0,"items":[]}}}`},
	))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "You are Iris.") {
		t.Fatalf("the boot briefing must print:\n%s", out.String())
	}
	if strings.Contains(out.String(), "normally works in") {
		t.Errorf("the warning must go to stderr, not into the briefing's stream:\n%s", out.String())
	}
	if !strings.Contains(stderrOf(t, f), "normally works in") {
		t.Errorf("the warning must reach stderr:\n%s", stderrOf(t, f))
	}
}

// ...and the ORDER, which the test above cannot see. With stdout and stderr as
// separate buffers, a warning emitted from inside the render callback lands in
// the same place as one emitted after it — so that test pins the STREAM and
// this one pins the SEQUENCE, by pointing both streams at one buffer the way a
// terminal does.
//
// Without this, "print it after the briefing" — the entire point of #456 — has
// no guard at all: the warning could move back above the briefing, reproducing
// the bug being fixed, and every other test here would still pass.
func TestSessionStartWarningIsSequencedAfterTheBriefingOnOneStream(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, startWithAffinityStubs(
		withRepos("hadron-memory/hadron-server"),
		map[string]string{"WorkersRoster": `{"data":{"workers":{"total":0,"items":[]}}}`},
	))
	f, _ := testFactory(t)
	// One buffer for both, as a terminal interleaves them.
	both := &strings.Builder{}
	f.IOStreams.Out = both
	f.IOStreams.ErrOut = both

	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	combined := both.String()
	briefingAt := strings.Index(combined, "You are Iris.")
	warningAt := strings.Index(combined, "normally works in")
	if briefingAt < 0 || warningAt < 0 {
		t.Fatalf("expected both the briefing and the warning:\n%s", combined)
	}
	if warningAt < briefingAt {
		t.Errorf("the warning must come AFTER the boot briefing — before it, it scrolls away, "+
			"which is the defect #456 exists to fix:\n%s", combined)
	}
}

// Every uncertainty resolves to SILENCE. Empty affinity is the server's answer
// to "no role", "role has no definition", "system memory unreadable" AND "you
// may not read this field" alike — so a denied caller simply gets no warning,
// which is the safe direction for a signal that must never become a refusal.
func TestSessionStartRepoAffinitySilentCases(t *testing.T) {
	for _, tc := range []struct {
		name, worker string
		args         []string
	}{
		{"no affinity at all", irisWorkerJSON,
			[]string{"--repo", "hadron-memory/hadron-cli"}},
		{"affinity present and matching", withRepos("hadron-memory/hadron-cli"),
			[]string{"--repo", "hadron-memory/hadron-cli"}},
		{"empty affinity list", withRepos(),
			[]string{"--repo", "hadron-memory/hadron-cli"}},
		{"--repo omitted", withRepos("hadron-memory/hadron-server"), nil},
		// GitHub repo paths are case-insensitive, so this is the SAME repo.
		// Warning here would fire at somebody who typed their own repo right.
		{"case differs only", withRepos("hadron-memory/Hadron-CLI"),
			[]string{"--repo", "hadron-memory/hadron-cli"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			gql, _ := captureGraphQL(t, startWithAffinityStubs(tc.worker, nil))
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"team", "session", "start", "--as", "wkr1",
				"--tool", "claude-code", "--server", gql.URL}, tc.args...)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if strings.Contains(stderrOf(t, f), "normally works in") {
				t.Errorf("must be silent (%s):\n%s", tc.name, stderrOf(t, f))
			}
		})
	}
}

// The suggestion is offered ONLY when it is unambiguous — exactly one
// non-retired teammate claims the repo. It is what makes the nudge actionable
// rather than merely correct.
func TestSessionStartSuggestsTheSoleWorkerForTheRepo(t *testing.T) {
	teamGitDir(t)
	jonas := strings.NewReplacer(
		`"id":"wkr1"`, `"id":"wkr9"`,
		`"name":"Iris"`, `"name":"Jonas"`,
		`"role":"backend-engineer"`, `"role":"cli-engineer"`,
		`"slug":"iris"`, `"slug":"jonas"`,
	).Replace(withRepos("hadron-memory/hadron-cli"))

	gql, _ := captureGraphQL(t, startWithAffinityStubs(
		withRepos("hadron-memory/hadron-server"),
		map[string]string{"WorkersRoster": `{"data":{"workers":{"total":1,"items":[` + jonas + `]}}}`},
	))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stderrOf(t, f), "bind Jonas (cli-engineer) if that is what you meant") {
		t.Errorf("the sole matching worker must be suggested:\n%s", stderrOf(t, f))
	}
}

// Two candidates: name the mismatch, suggest nothing. A guess would be worse
// than silence, because the whole nudge rests on the reader trusting it.
//
// And a RETIRED worker is never suggested — `session start` refuses one
// outright, so naming it would hand the reader a remedy that cannot be run.
func TestSessionStartSuggestsNothingWhenAmbiguousOrRetired(t *testing.T) {
	mk := func(id, name, role, slug string, retired bool) string {
		s := strings.NewReplacer(
			`"id":"wkr1"`, `"id":"`+id+`"`,
			`"name":"Iris"`, `"name":"`+name+`"`,
			`"role":"backend-engineer"`, `"role":"`+role+`"`,
			`"slug":"iris"`, `"slug":"`+slug+`"`,
		).Replace(withRepos("hadron-memory/hadron-cli"))
		if retired {
			s = strings.Replace(s, `"retiredAt":null`, `"retiredAt":"2026-08-01T00:00:00Z"`, 1)
		}
		return s
	}

	t.Run("two candidates", func(t *testing.T) {
		teamGitDir(t)
		roster := mk("wkr9", "Jonas", "cli-engineer", "jonas", false) + "," +
			mk("wkr8", "Kira", "cli-engineer", "kira", false)
		gql, _ := captureGraphQL(t, startWithAffinityStubs(
			withRepos("hadron-memory/hadron-server"),
			map[string]string{"WorkersRoster": `{"data":{"workers":{"total":2,"items":[` + roster + `]}}}`},
		))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
			"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(stderrOf(t, f), "normally works in") {
			t.Errorf("the mismatch itself must still be named:\n%s", stderrOf(t, f))
		}
		if strings.Contains(stderrOf(t, f), "if that is what you meant") {
			t.Errorf("an ambiguous roster must suggest nobody:\n%s", stderrOf(t, f))
		}
	})

	t.Run("only candidate is retired", func(t *testing.T) {
		teamGitDir(t)
		gql, _ := captureGraphQL(t, startWithAffinityStubs(
			withRepos("hadron-memory/hadron-server"),
			map[string]string{"WorkersRoster": `{"data":{"workers":{"total":1,"items":[` +
				mk("wkr9", "Jonas", "cli-engineer", "jonas", true) + `]}}}`},
		))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
			"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(stderrOf(t, f), "if that is what you meant") {
			t.Errorf("a retired worker must never be suggested — start refuses one:\n%s", stderrOf(t, f))
		}
	})
}

// A roster read that fails must not turn a soft nudge into noise: the mismatch
// is still reported, the suggestion is simply absent, and nothing errors.
func TestSessionStartWarnsEvenWhenTheRosterReadFails(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, startWithAffinityStubs(
		withRepos("hadron-memory/hadron-server"),
		map[string]string{"WorkersRoster": `{"errors":[{"message":"boom"}]}`},
	))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--repo", "hadron-memory/hadron-cli", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a failed suggestion lookup must not fail the command: %v", err)
	}
	if !strings.Contains(stderrOf(t, f), "normally works in") {
		t.Errorf("the mismatch must still be reported:\n%s", stderrOf(t, f))
	}
	if strings.Contains(stderrOf(t, f), "if that is what you meant") {
		t.Errorf("no suggestion is possible when the roster read failed:\n%s", stderrOf(t, f))
	}
}

// --json: the warning belongs on stderr and must NOT enter the JSON a
// consumer parses on stdout. It still prints, because a wrong binding is worth
// telling an agent about too.
func TestSessionStartAffinityWarningStaysOutOfJSON(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, startWithAffinityStubs(
		withRepos("hadron-memory/hadron-server"),
		map[string]string{"WorkersRoster": `{"data":{"workers":{"total":0,"items":[]}}}`},
	))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--repo", "hadron-memory/hadron-cli", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("stdout must stay parseable JSON: %v\n%s", err, out.String())
	}
	if !strings.Contains(stderrOf(t, f), "normally works in") {
		t.Errorf("the warning must still reach stderr under --json:\n%s", stderrOf(t, f))
	}
	// And the affinity is in the --json worker, so an agent can make the same
	// check itself rather than parsing the warning text.
	worker, _ := result["worker"].(map[string]any)
	repos, _ := worker["repos"].([]any)
	if len(repos) != 1 || repos[0] != "hadron-memory/hadron-server" {
		t.Errorf("worker.repos must carry the affinity: %v", worker["repos"])
	}
}

// [] not null: empty is the field's MEANINGFUL value ("never warn"), so a null
// would read as a third state the contract does not have.
func TestWorkerReposRendersAsEmptyArrayNotNull(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker": `{"data":{"worker":` + irisWorkerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"repos": []`) {
		t.Errorf("an absent affinity must render as [], not null:\n%s", out.String())
	}
}

// `worker list --json` is one of the two promised affinity surfaces, and until
// this test it had NO coverage: the existing list tests use fixtures without
// `repos` and decode only name/retired, so dropping `repos` from
// WorkerRosterFields or from workerRosterDTO would have stayed green
// (PR #516 review, Copilot).
//
// Both values are asserted, because they are different claims: a non-empty
// affinity proves the mapping carries data, and [] proves the meaningful
// empty case is not rendered as null.
func TestWorkerListJSONCarriesRepos(t *testing.T) {
	teamGitDir(t)
	withAffinity := withRepos("hadron-memory/hadron-cli")
	noAffinity := strings.Replace(irisWorkerJSON, `"id":"wkr1"`, `"id":"wkr2"`, 1)

	gql, _ := captureGraphQL(t, map[string]string{
		"WorkersRoster": `{"data":{"workers":{"total":2,"items":[` +
			withAffinity + `,` + noAffinity + `]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var workers []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &workers); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if len(workers) != 2 {
		t.Fatalf("want 2 workers, got %d: %s", len(workers), out.String())
	}
	got, _ := workers[0]["repos"].([]any)
	if len(got) != 1 || got[0] != "hadron-memory/hadron-cli" {
		t.Errorf("the roster --json must carry the affinity: %v", workers[0]["repos"])
	}
	// The empty case must be [] and PRESENT — not null, and not absent.
	empty, ok := workers[1]["repos"].([]any)
	if !ok || len(empty) != 0 {
		t.Errorf("an absent affinity must render as [], got %#v", workers[1]["repos"])
	}
	if !strings.Contains(out.String(), `"repos": []`) {
		t.Errorf("the empty affinity must serialize as [], not null:\n%s", out.String())
	}
}
