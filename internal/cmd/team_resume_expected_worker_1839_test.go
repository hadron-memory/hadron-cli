package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1839 over hadron-server#3822 (cor:agt:020:03): a no-argument
// resume names the worker its worktree was last bound to, so another worker's
// waiting claim is refused instead of consumed into this worktree.

const mismatchRefusal = `{"errors":[{"message":"refused","extensions":{"code":"RESUME_REFUSED","reason":"WORKER_MISMATCH",` +
	`"picked":false,"expectedWorkerId":"wkr1","expectedWorkerName":"Iris","foundWorkerId":"wkr9","foundWorkerName":"Greer","foundSource":"%s"}}],"data":null}`

func mismatchStubs(source string) map[string]string {
	return map[string]string{
		"GetBoundSessionLiveness": `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":null,"isLive":false}}}`,
		"ResumeTeamSession":       strings.Replace(mismatchRefusal, "%s", source, 1),
	}
}

func writeBinding(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(p, []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResumeSendsTheWorktreesWorkerAndRefusesAMismatch(t *testing.T) {
	endedStubs := mismatchStubs("CLAIM")
	endedStubs["GetBoundSessionLiveness"] = `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":"2026-08-11T11:00:00Z","isLive":false}}}`
	for _, tc := range []struct {
		name   string
		stubs  map[string]string
		source string
		want   []string
	}{
		{"lapsed binding, other worker's claim", mismatchStubs("CLAIM"), "CLAIM", []string{"Iris", "Greer", "claim", "untouched", "--as"}},
		{"ended binding", endedStubs, "CLAIM", []string{"Iris", "Greer", "untouched", "--as"}},
		{"other worker's live session", mismatchStubs("LIVE_SESSION"), "LIVE_SESSION", []string{"Greer", "live session", "untouched", "--as"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			path := writeBinding(t, dir)
			gql, captured := captureGraphQL(t, tc.stubs)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Conflict {
				t.Fatalf("exit = %d, want the Conflict RESUME_REFUSED maps to; err: %v", code, err)
			}
			if got := resumeInput(t, captured["ResumeTeamSession"])["expectedWorkerId"]; got != "wkr1" {
				t.Errorf("expectedWorkerId = %v, want the binding's wkr1", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal must say %q: %v", w, err)
				}
			}
			if strings.Contains(err.Error(), "spent") {
				t.Errorf("a WORKER_MISMATCH refusal spends nothing, and must not say it did: %v", err)
			}
			if data, _ := os.ReadFile(path); string(data) != bindingFixture {
				t.Errorf("binding must stay unchanged: %s", data)
			}
			if _, called := captured["StartTeamSession"]; called {
				t.Error("a refused resume must not start anything")
			}
		})
	}
}

func TestResumeMismatchJSONCarriesOnlyWhatTheServerSent(t *testing.T) {
	dir := teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	writeBinding(t, dir)
	gql, _ := captureGraphQL(t, mismatchStubs("CLAIM"))
	f, out := testFactory(t)
	f.JSON = true
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("want a refusal")
	}
	renderError(f, err)
	var env struct {
		Error struct {
			ServerCode string         `json:"serverCode"`
			Extensions map[string]any `json:"extensions"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
		t.Fatalf("envelope: %v", jerr)
	}
	ext := env.Error.Extensions
	if env.Error.ServerCode != "RESUME_REFUSED" || ext["reason"] != "WORKER_MISMATCH" || ext["expectedWorkerId"] != "wkr1" ||
		ext["expectedWorkerName"] != "Iris" || ext["foundWorkerId"] != "wkr9" || ext["foundWorkerName"] != "Greer" || ext["foundSource"] != "CLAIM" {
		t.Errorf("envelope = %+v", env)
	}
}

func TestResumeSameWorkerAndNoBinding(t *testing.T) {
	t.Run("same worker resumes and sends the id", func(t *testing.T) {
		dir := teamGitDir(t)
		t.Setenv("HADRON_LAUNCH_GRANT", "")
		writeBinding(t, dir)
		stubs := resumeStubs("RESUMED")
		stubs["GetBoundSessionLiveness"] = `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":null,"isLive":false}}}`
		gql, captured := captureGraphQL(t, stubs)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if got := resumeInput(t, captured["ResumeTeamSession"])["expectedWorkerId"]; got != "wkr1" {
			t.Errorf("expectedWorkerId = %v", got)
		}
	})
	t.Run("no binding sends nothing", func(t *testing.T) {
		teamGitDir(t)
		t.Setenv("HADRON_LAUNCH_GRANT", "")
		gql, captured := captureGraphQL(t, resumeStubs("RESUMED"))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if _, sent := resumeInput(t, captured["ResumeTeamSession"])["expectedWorkerId"]; sent {
			t.Error("no binding file: no expected worker may be sent")
		}
	})
}

func TestResumeNeverSentWithAs(t *testing.T) {
	dir := teamGitDir(t)
	writeBinding(t, dir)
	successor := strings.Replace(startedSessionJSON, `"s-new"`, `"s-successor"`, 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetBoundSessionLiveness": `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":null,"isLive":false}}}`,
		"GetWorker":               `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessionsPage":        `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`,
		"StartTeamSession":        `{"data":{"startSession":` + successor + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, called := captured["ResumeTeamSession"]; called {
		t.Error("--as is a named start, never a resume")
	}
}

func TestSessionStartHelpScopesTheSpentClaim(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--help"})
	root.SetOut(out)
	_ = root.Execute()
	help := strings.Join(strings.Fields(out.String()), " ")
	if strings.Contains(help, "A refused resume (exit 5, RESUME_REFUSED) has spent its claim.") {
		t.Error("help still says every refused resume spends its claim")
	}
	if !strings.Contains(help, "WORKER_MISMATCH") || !strings.Contains(help, "spends nothing") {
		t.Errorf("help must say a WORKER_MISMATCH refusal spends nothing")
	}
}
