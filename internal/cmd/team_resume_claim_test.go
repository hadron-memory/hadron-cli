package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#842 over hadron-server#1956 (cor:agt:020:03): a post or a work
// record is never silently attributed to the human, and a fresh context
// recovers its worker from the server with a resume claim.

const resumeCandidatesJSON = `[
	{"sessionId":"s-a","workerId":"wkr1","workerName":"Iris","description":"cli#842","source":"CLAIM","expiresAt":"2026-10-15T12:00:60Z"},
	{"sessionId":"s-b","workerId":"wkr2","workerName":"Dara","description":null,"source":"LIVE_SESSION","expiresAt":null}]`

func resumeStubs(outcome string) map[string]string {
	session, candidates := startedSessionJSON, `[]`
	if outcome == "CHOOSE" {
		session, candidates = "null", resumeCandidatesJSON
	}
	return map[string]string{
		"GetWorker":         `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"ResumeTeamSession": `{"data":{"resumeSession":{"outcome":"` + outcome + `","session":` + session + `,"candidates":` + candidates + `}}}`,
	}
}

func resumeInput(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("ResumeTeamSession variables: %v (%s)", err, raw)
	}
	return vars.Input
}

// THE RED: an unbound context used to post as the human. It now refuses, with
// nothing sent, and names the recovery path without naming any binding.
func TestTeamChatPostUnboundRefusesInsteadOfPostingAsTheHuman(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		args  []string
	}{
		{"no binding, --app", func(t *testing.T) { teamGitDir(t) }, []string{"--app", "acme.com:eng-team"}},
		{"no binding, ambient App", func(t *testing.T) { teamGitDir(t); configuredApp(t, "acme.com:eng-team") }, nil},
		{"outside any worktree, --app", func(t *testing.T) {
			t.Setenv("HADRON_TEAM_GIT_DIR", "")
			t.Chdir(t.TempDir())
		}, []string{"--app", "acme.com:eng-team"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatHumanMsgJSON + `}}`,
				"TeamAppIdentity":       teamAppIdentityJSON,
			})
			f, _ := testFactory(t)
			c.setup(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "chat", "post", "hi", "--server", gql.URL}, c.args...))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.NotFound {
				t.Errorf("exit = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
			}
			if _, posted := captured["CreateTeamChatMessage"]; posted {
				t.Fatal("an unbound post must never reach the server: it would be attributed to the human")
			}
			for _, want := range []string{"team session start", "--as-me"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must name %q: %v", want, err)
				}
			}
		})
	}
}

// --as-me stays the explicit way to post as yourself, bound or not.
func TestTeamChatPostAsMeUnboundStillPostsAsTheHuman(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatHumanMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "hi", "--as-me", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--as-me must post: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
	if _, sent := vars["sessionRef"]; sent {
		t.Errorf("--as-me must send no sessionRef: %v", vars)
	}
}

// One claim (or one live session): start with no worker named binds it and
// writes the optional cache, with the briefing, like any bind.
func TestTeamSessionStartWithNoWorkerRedeems(t *testing.T) {
	for _, outcome := range []string{"RESUMED", "STARTED"} {
		t.Run(outcome, func(t *testing.T) {
			dir := teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			gql, captured := captureGraphQL(t, resumeStubs(outcome))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--tool", "claude-code", "--transcript", "/tmp/t.jsonl",
				"--model", "claude-opus-5-5", "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("a resume must bind: %v", err)
			}
			in := resumeInput(t, captured["ResumeTeamSession"])
			if in["tool"] != "claude-code" || in["transcriptPath"] != "/tmp/t.jsonl" || in["llmModel"] != "claude-opus-5-5" {
				t.Errorf("provenance must ride along: %v", in)
			}
			if h, _ := in["host"].(string); h == "" {
				t.Errorf("host must default to the hostname: %v", in)
			}
			for _, k := range []string{"session", "description", "family"} {
				if _, sent := in[k]; sent {
					t.Errorf("unset %q must be omitted, got %v", k, in[k])
				}
			}
			if _, called := captured["StartTeamSession"]; called {
				t.Error("a resume must not also call startSession")
			}
			data, err := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
			if err != nil {
				t.Fatalf("the binding cache must be written: %v", err)
			}
			var b map[string]any
			_ = json.Unmarshal(data, &b)
			if b["sessionId"] != "s-new" || b["workerId"] != "wkr1" || b["workerName"] != "Iris" || b["appId"] != "capp100000000000000000000" {
				t.Errorf("binding: %s", data)
			}
			if fi, _ := os.Stat(filepath.Join(dir, "hadron-team-session.json")); fi != nil && fi.Mode().Perm() != 0o600 {
				t.Errorf("the binding cache must be 0600, got %v", fi.Mode().Perm())
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatalf("json: %v (%s)", err, out.String())
			}
			if dto["resumeOutcome"] != outcome {
				t.Errorf("resumeOutcome = %v, want %s", dto["resumeOutcome"], outcome)
			}
		})
	}
}

// An ordinary bind keeps its shape, with resumeOutcome null.
func TestTeamSessionStartAsReportsNoResumeOutcome(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	gql, _ := captureGraphQL(t, startWithAffinityStubs(irisWorkerJSON, nil))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"resumeOutcome": null`) {
		t.Errorf("an ordinary bind must carry resumeOutcome null:\n%s", out.String())
	}
}

// Several: nothing is bound or consumed; the candidates are listed for the
// human to pick, and the exit is non-zero so a script cannot read it as bound.
func TestTeamSessionStartResumeChooseListsAndBindsNothing(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "text"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			dir := teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			gql, _ := captureGraphQL(t, resumeStubs("CHOOSE"))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "session", "start", "--server", gql.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Errorf("exit = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "hadron-team-session.json")); statErr == nil {
				t.Error("CHOOSE must not write a binding")
			}
			if asJSON {
				var dto struct {
					ResumeOutcome string `json:"resumeOutcome"`
					Candidates    []struct {
						SessionID  string  `json:"sessionId"`
						WorkerName string  `json:"workerName"`
						Source     string  `json:"source"`
						ExpiresAt  *string `json:"expiresAt"`
					} `json:"candidates"`
				}
				if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
					t.Fatalf("json: %v (%s)", err, out.String())
				}
				if dto.ResumeOutcome != "CHOOSE" || len(dto.Candidates) != 2 || dto.Candidates[0].SessionID != "s-a" ||
					dto.Candidates[1].WorkerName != "Dara" || dto.Candidates[1].ExpiresAt != nil {
					t.Errorf("CHOOSE json: %s", out.String())
				}
				return
			}
			for _, want := range []string{"Iris", "Dara", "cli#842", "--session s-a", "--session s-b"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("the choice must show %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// --session picks a listed candidate.
func TestTeamSessionStartResumePicksASession(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	gql, captured := captureGraphQL(t, resumeStubs("RESUMED"))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--session", "s-a", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := resumeInput(t, captured["ResumeTeamSession"])["session"]; got != "s-a" {
		t.Errorf("session = %v, want s-a", got)
	}
}

// Nothing to resume is a typed refusal, never a fallback.
func TestTeamSessionStartResumeServerRefusals(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"NOTHING_TO_RESUME", exitcode.NotFound},
		{"RESUME_REFUSED", exitcode.Conflict},
		{"RESUME_AMBIGUOUS", exitcode.Usage},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			dir := teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			gql, captured := captureGraphQL(t, map[string]string{
				"ResumeTeamSession": `{"errors":[{"message":"refused","extensions":{"code":"` + c.code + `"}}],"data":null}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
			err := root.Execute()
			if code := exitCodeFor(err); code != c.want {
				t.Errorf("exit = %d, want %d; err: %v", code, c.want, err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "hadron-team-session.json")); statErr == nil {
				t.Error("a refused resume must not write a binding")
			}
			if _, called := captured["StartTeamSession"]; called {
				t.Error("a refused resume must not fall back to anything")
			}
		})
	}
}

// Flags a resume cannot honour are refused before the call that spends a claim.
func TestTeamSessionStartResumeFlagRefusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--session with --as", []string{"--as", "Iris", "--session", "s-a"}, "--session"},
		{"--session with a grant", []string{"--launch-grant", "hlg_x", "--session", "s-a"}, "--session"},
		{"--force on a resume", []string{"--force"}, "--force"},
		{"--repo on a resume", []string{"--repo", "a/b"}, "--repo"},
		{"--branch on a resume", []string{"--branch", "main"}, "--branch"},
		{"--reports-to on a resume", []string{"--reports-to", "Ada"}, "--reports-to"},
		{"-m on a resume", []string{"-m", "acme.com::eng-team"}, "--memory"},
		{"empty --session", []string{"--session", " "}, "--session"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			gql, captured := captureGraphQL(t, resumeStubs("RESUMED"))
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "session", "start", "--server", gql.URL}, c.args...))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Errorf("exit = %d, want %d; err: %v", code, exitcode.Usage, err)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error must name %q: %v", c.want, err)
			}
			if _, called := captured["ResumeTeamSession"]; called {
				t.Error("a refused start must not spend a claim")
			}
		})
	}
}

// A worktree already bound to a LIVE session refuses before the redemption:
// redeeming spends a claim, and the binding here already answers.
func TestTeamSessionStartResumeRefusesOverALiveBinding(t *testing.T) {
	dir := teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := resumeStubs("RESUMED")
	stubs["GetBoundSessionLiveness"] = `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":null,"isLive":true}}}`
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Errorf("exit = %d, want %d; err: %v", code, exitcode.Conflict, err)
	}
	if _, called := captured["ResumeTeamSession"]; called {
		t.Error("a live binding must refuse before a claim is spent")
	}
}

// Outside a git worktree there is nowhere to cache the binding: refuse before
// the claim is spent rather than strand it.
func TestTeamSessionStartResumeOutsideAWorktreeRefusesFirst(t *testing.T) {
	t.Setenv("HADRON_TEAM_GIT_DIR", "")
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	t.Chdir(t.TempDir())
	gql, captured := captureGraphQL(t, resumeStubs("RESUMED"))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("exit = %d, want %d; err: %v", code, exitcode.Usage, err)
	}
	if _, called := captured["ResumeTeamSession"]; called {
		t.Error("no worktree: the claim must not be spent")
	}
}

// A resume never rolls back: ending a RESUMED session would destroy what the
// claim recovered. A failed worker read reports and leaves the session open.
func TestTeamSessionStartResumeNeverEndsTheSessionOnFailure(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	stubs := resumeStubs("RESUMED")
	stubs["GetWorker"] = `{"errors":[{"message":"boom"}],"data":null}`
	stubs["EndTeamSession"] = `{"data":{"endSession":` + startedSessionJSON + `}}`
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a failed worker read must fail the start")
	}
	if _, ended := captured["EndTeamSession"]; ended {
		t.Error("a resume must never end the recovered session")
	}
	if !strings.Contains(err.Error(), "s-new") || !strings.Contains(err.Error(), "team session start") {
		t.Errorf("the error must name the session and the retry: %v", err)
	}
}

// Rotation: end --resume-claim writes the claim with the handoff, through its
// own operation; a plain end keeps the old document.
func TestTeamSessionEndResumeClaim(t *testing.T) {
	for _, withClaim := range []bool{true, false} {
		name := "plain"
		if withClaim {
			name = "with claim"
		}
		t.Run(name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			ended := strings.Replace(startedSessionJSON, `"endedAt":null`, `"endedAt":"2026-08-11T11:00:00Z"`, 1)
			gql, captured := captureGraphQL(t, map[string]string{
				"EndTeamSession":                `{"data":{"endSession":` + ended + `}}`,
				"EndTeamSessionWithResumeClaim": `{"data":{"endSession":` + ended + `}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "session", "end", "--handoff", "all landed", "--server", gql.URL}
			if withClaim {
				args = append(args, "--resume-claim")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			_, plain := captured["EndTeamSession"]
			_, claimed := captured["EndTeamSessionWithResumeClaim"]
			if withClaim != claimed || withClaim == plain {
				t.Errorf("plain=%v claimed=%v for --resume-claim=%v", plain, claimed, withClaim)
			}
			if withClaim {
				var vars map[string]any
				_ = json.Unmarshal(captured["EndTeamSessionWithResumeClaim"], &vars)
				if vars["id"] != "s-new" || vars["handoff"] != "all landed" {
					t.Errorf("vars: %v", vars)
				}
				if !strings.Contains(out.String(), "resume claim") {
					t.Errorf("the receipt must say a claim was written:\n%s", out.String())
				}
			}
		})
	}
}

// Shed: resume-claim writes a claim for the bound session and says how long it
// lives. The receipt carries no claim id: there is nothing to hold.
func TestTeamSessionResumeClaim(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateResumeClaim": `{"data":{"createResumeClaim":{"sessionId":"s-new","workerId":"wkr1","expiresAt":"2026-10-15T12:01:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "resume-claim", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateResumeClaim"], &vars)
	if vars["sessionId"] != "s-new" {
		t.Errorf("vars: %v", vars)
	}
	for _, want := range []string{"Iris", "2026-10-15T12:01:00Z", "60 seconds", "team session start"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("receipt must contain %q:\n%s", want, out.String())
		}
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "resume-claim", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out2.String()), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out2.String())
	}
	if dto["sessionId"] != "s-new" || dto["workerId"] != "wkr1" || dto["expiresAt"] != "2026-10-15T12:01:00Z" {
		t.Errorf("json: %s", out2.String())
	}
}

func TestTeamSessionResumeClaimUnboundRefuses(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateResumeClaim": `{"data":{"createResumeClaim":{"sessionId":"s-new","workerId":"wkr1","expiresAt":"2026-10-15T12:01:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "resume-claim", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.NotFound {
		t.Errorf("exit = %d, want %d; err: %v", code, exitcode.NotFound, err)
	}
	if _, called := captured["CreateResumeClaim"]; called {
		t.Error("no binding and no --session: nothing to claim")
	}

	// --session names one explicitly (recovery), with no binding needed.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "resume-claim", "--session", "s-x", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateResumeClaim"], &vars)
	if vars["sessionId"] != "s-x" {
		t.Errorf("vars: %v", vars)
	}
}
