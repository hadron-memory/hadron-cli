package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#947: `team session start --as` printed the briefing from its PRE-bind
// worker read, which the server rendered while the previous session was the
// newest open one, so {{session.llmModel}} named the old model. The briefing
// now comes from the bind response's own worker.prompt: the server's
// rendering as of the response, not a record of what the session was served.

const preBindWorkerJSON = `{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"capp100000000000000000000","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":"You are Iris. MODEL=model-one","promptOverride":null,"memoryId":"mw1","retiredAt":null,"retiredBy":null,
	"createdAt":"2026-08-14T00:00:00Z","createdBy":"u-holger"}`

// startedWith is the bind response with its boundWorker selection; raw is the
// JSON value of boundWorker ("null", or an object).
func startedWith(raw string) string {
	return `{"data":{"startSession":` + strings.TrimSuffix(startedSessionJSON, "}") + `,"boundWorker":` + raw + `}}}`
}

func runStartAs(t *testing.T, startResp string, extra ...string) (string, string, *[]attnCall) {
	t.Helper()
	teamGitDir(t)
	stubs := map[string]string{
		"GetWorker":        `{"data":{"worker":` + preBindWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": startResp,
	}
	if resp, ok := unstubbedDefault("GetWorkerContinuity"); ok {
		stubs["GetWorkerContinuity"] = resp
	}
	srv, calls := attnServer(t, stubs)
	f, out := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "start", "--as", "wkr1", "--model", "model-two", "--server", srv.URL}, extra...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String(), errOut.String(), calls
}

// Pin 1: the human briefing is the post-bind rendering.
func TestC947StartPrintsTheBindResponseBriefing(t *testing.T) {
	out, errOut, _ := runStartAs(t, startedWith(`{"prompt":"You are Iris. MODEL=model-two"}`))
	if !strings.Contains(out, "MODEL=model-two") || strings.Contains(out, "MODEL=model-one") {
		t.Errorf("the briefing must be the bind response's, not the pre-bind read:\n%s", out)
	}
	if strings.Contains(errOut, "read before this bind") {
		t.Errorf("no fallback note when the response carries the briefing:\n%s", errOut)
	}
}

// Pin 2: --json carries the same post-bind prompt, with no new field.
func TestC947StartJSONCarriesTheBindResponseBriefing(t *testing.T) {
	out, _, _ := runStartAs(t, startedWith(`{"prompt":"You are Iris. MODEL=model-two"}`), "--json")
	var v struct {
		Worker struct {
			Prompt *string `json:"prompt"`
		} `json:"worker"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if v.Worker.Prompt == nil || !strings.Contains(*v.Worker.Prompt, "model-two") {
		t.Errorf("--json worker.prompt must be the post-bind rendering: %v", v.Worker.Prompt)
	}
}

// Pin 5: a NULL worker or a NULL prompt on the response falls back to the
// pre-bind briefing with a note, exit 0, and the bind is sent exactly once.
func TestC947NullResponseFallsBackWithANoteAndNoRetry(t *testing.T) {
	for _, raw := range []string{`null`, `{"prompt":null}`} {
		out, errOut, calls := runStartAs(t, startedWith(raw))
		if !strings.Contains(out, "MODEL=model-one") {
			t.Errorf("%s: the pre-bind briefing must still print:\n%s", raw, out)
		}
		if !strings.Contains(errOut, "read before this bind") {
			t.Errorf("%s: the fallback must say so on stderr:\n%s", raw, errOut)
		}
		if n := len(callsFor(calls, "StartTeamSession")); n != 1 {
			t.Errorf("%s: the bind must be sent exactly once, got %d", raw, n)
		}
	}
}

// Pin 5b: an EMPTY prompt is a legitimate answer (today), not a null: no
// fallback, no stale pre-bind briefing, no note.
func TestC947EmptyResponsePromptIsNotAFallback(t *testing.T) {
	out, errOut, _ := runStartAs(t, startedWith(`{"prompt":""}`))
	if strings.Contains(out, "MODEL=model-one") {
		t.Errorf("an empty post-bind prompt must not bring back the pre-bind one:\n%s", out)
	}
	if strings.Contains(errOut, "read before this bind") {
		t.Errorf("empty is not null; no fallback note:\n%s", errOut)
	}
}

// Pin 6: the briefing is selected on the bind operation only, aliased, and
// the shared session fragment (used by session lists) still carries no prompt.
func TestC947OnlyTheBindOperationSelectsThePrompt(t *testing.T) {
	if !strings.Contains(gen.StartTeamSession_Operation, "boundWorker: worker") {
		t.Errorf("StartTeamSession must select boundWorker: worker { prompt }:\n%s", gen.StartTeamSession_Operation)
	}
	frag := gen.TeamSessions_Operation
	if i := strings.Index(frag, "fragment TeamSessionFields"); i >= 0 {
		frag = frag[i:]
	}
	if strings.Contains(frag, "prompt") {
		t.Errorf("TeamSessionFields must not drag a prompt into session lists:\n%s", frag)
	}
}

// Pins 3 and 4 (characterization): a launch and a resume already read the
// worker AFTER the bind (launchedWorker), so their briefing is the post-bind
// rendering without the bind-response selection, and no fallback note.
func TestC947LaunchAndResumeReadTheWorkerAfterTheBind(t *testing.T) {
	postBind := strings.Replace(preBindWorkerJSON, "MODEL=model-one", "MODEL=model-two", 1)
	for _, tc := range []struct {
		name  string
		stubs map[string]string
		env   string
	}{
		{"launch grant", map[string]string{
			"GetWorker":        `{"data":{"worker":` + postBind + `}}`,
			"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
		}, "hlg_abc"},
		{"resume", map[string]string{
			"GetWorker":         `{"data":{"worker":` + postBind + `}}`,
			"ResumeTeamSession": `{"data":{"resumeSession":{"outcome":"RESUMED","session":` + startedSessionJSON + `,"candidates":[]}}}`,
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", tc.env)
			gql, _ := captureGraphQL(t, tc.stubs)
			f, out := testFactory(t)
			errOut := f.IOStreams.ErrOut.(*strings.Builder)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--tool", "claude-code", "--model", "model-two", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "MODEL=model-two") {
				t.Errorf("the post-bind briefing must print:\n%s", out.String())
			}
			if strings.Contains(errOut.String(), "read before this bind") {
				t.Errorf("a post-bind read needs no fallback note:\n%s", errOut.String())
			}
		})
	}
}
