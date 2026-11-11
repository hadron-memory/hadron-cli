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
	t.Setenv("HADRON_LAUNCH_GRANT", "")
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

// Delta (LOCAL P2s on 2823fac2).

func mismatchWith(ext string) map[string]string {
	st := mismatchStubs("CLAIM")
	st["ResumeTeamSession"] = `{"errors":[{"message":"refused","extensions":{"code":"RESUME_REFUSED","reason":"WORKER_MISMATCH"` + strings.TrimSuffix(","+ext, ",") + `}}],"data":null}`
	return st
}

func runMismatch(t *testing.T, ext string, jsonMode bool) (string, error) {
	t.Helper()
	dir := teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	writeBinding(t, dir)
	gql, _ := captureGraphQL(t, mismatchWith(ext))
	f, out := testFactory(t)
	f.JSON = jsonMode
	root := NewRootCmd(f)
	args := []string{"team", "session", "start", "--server", gql.URL}
	if jsonMode {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatal("want a refusal")
	}
	if jsonMode {
		renderError(f, err)
	}
	return out.String(), err
}

// A key the server sent as null stays in the envelope as null: the envelope
// renders what the server sent, not only its non-null parts.
func TestResumeMismatchJSONKeepsNullNames(t *testing.T) {
	out, _ := runMismatch(t, `"picked":false,"expectedWorkerId":"wkr1","expectedWorkerName":null,"foundWorkerId":"wkr9","foundWorkerName":"Greer","foundSource":"CLAIM"`, true)
	var env struct {
		Error struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	v, present := env.Error.Extensions["expectedWorkerName"]
	if !present || v != nil {
		t.Errorf("expectedWorkerName must be present as null: %+v", env.Error.Extensions)
	}
}

// picked:true means --session chose the other worker's item: do not say
// that only the other worker's item waits.
func TestResumeMismatchPickedWording(t *testing.T) {
	_, err := runMismatch(t, `"picked":true,"expectedWorkerId":"wkr1","expectedWorkerName":"Iris","foundWorkerId":"wkr9","foundWorkerName":"Greer","foundSource":"CLAIM"`, false)
	msg := err.Error()
	if strings.Contains(msg, "found only") {
		t.Errorf("a picked mismatch must not claim only the other worker's item waits: %s", msg)
	}
	for _, w := range []string{"picked", "Greer", "Iris", "untouched"} {
		if !strings.Contains(msg, w) {
			t.Errorf("must say %q: %s", w, msg)
		}
	}
}

// Server-sent names reach a terminal: control characters cannot forge a line
// or move the cursor.
func TestResumeMismatchNamesAreSanitized(t *testing.T) {
	_, err := runMismatch(t, `"picked":false,"expectedWorkerId":"wkr1","expectedWorkerName":"Iris\u001b[31m","foundWorkerId":"wkr9","foundWorkerName":"Gre\ner\u001b]0;x\u0007","foundSource":"CLAIM"`, false)
	for _, r := range err.Error() {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control character %q in the refusal: %q", r, err.Error())
		}
	}
}

// Delta (BLIND P2 on 2823fac2): a server that omits fields. The envelope
// carries only what was sent; the message falls back through the binding's own
// name and generic wording rather than printing blanks.
func TestResumeMismatchServerOmitsFields(t *testing.T) {
	out, err := runMismatch(t, ``, true)
	var env struct {
		Error struct {
			ServerCode string         `json:"serverCode"`
			Extensions map[string]any `json:"extensions"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatal(jerr)
	}
	if env.Error.ServerCode != "RESUME_REFUSED" || len(env.Error.Extensions) != 2 ||
		env.Error.Extensions["code"] != "RESUME_REFUSED" || env.Error.Extensions["reason"] != "WORKER_MISMATCH" {
		t.Errorf("omitted fields must stay omitted: %+v", env.Error)
	}
	// The binding file's own name stands in for the unsent expected name, and
	// the unsent found worker and source read generically.
	msg := err.Error()
	for _, want := range []string{"Iris", "another worker of yours", "claim or live session", "untouched"} {
		if !strings.Contains(msg, want) {
			t.Errorf("fallback wording must say %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "  ") || strings.Contains(msg, "''") {
		t.Errorf("no blank labels: %s", msg)
	}
}

// A whitespace-only WorkerID in the binding file is not a worker: send nothing.
func TestResumeBlankBindingWorkerSendsNothing(t *testing.T) {
	dir := teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	blank := strings.Replace(bindingFixture, `"workerId":"wkr1"`, `"workerId":"  "`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(blank), 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := resumeStubs("RESUMED")
	stubs["GetBoundSessionLiveness"] = `{"data":{"session":{"id":"s-new","startedAt":"2026-08-11T10:00:00Z","endedAt":null,"isLive":false}}}`
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, sent := resumeInput(t, captured["ResumeTeamSession"])["expectedWorkerId"]; sent {
		t.Error("a blank worker id must not be sent (the server refuses a blank as BAD_USER_INPUT)")
	}
}

// Delta (BLIND P2-1/P2-2 on 79e0cf58). A server without #3822 rejects the
// unknown input field: the refusal says so and names the way forward, instead
// of the generic schema-skew line, and never retries without the guard.
func TestResumeServerWithoutExpectedWorkerRefusesActionably(t *testing.T) {
	for _, tc := range []struct{ name, errs string }{
		{"validation code", `{"errors":[{"message":"Variable \"$input\" got invalid value; Field \"expectedWorkerId\" is not defined by type \"ResumeSessionInput\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`},
		{"bad user input", `{"errors":[{"message":"Field \"expectedWorkerId\" is not defined by type \"ResumeSessionInput\".","extensions":{"code":"BAD_USER_INPUT"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			t.Setenv("HADRON_LAUNCH_GRANT", "")
			path := writeBinding(t, dir)
			stubs := mismatchStubs("CLAIM")
			stubs["ResumeTeamSession"] = tc.errs
			gql, captured := captureGraphQL(t, stubs)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--server", gql.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("want a refusal")
			}
			for _, w := range []string{"hadron-server#3822", "--as", "No claim"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("must say %q: %v", w, err)
				}
			}
			if strings.Contains(err.Error(), "out of sync") {
				t.Errorf("the generic skew line must not stand in for the specific one: %v", err)
			}
			if n := strings.Count(string(captured["ResumeTeamSession"]), "expectedWorkerId"); n == 0 {
				t.Error("the field was sent")
			}
			if data, _ := os.ReadFile(path); string(data) != bindingFixture {
				t.Error("binding must stay unchanged")
			}
		})
	}
}

// Other RESUME_REFUSED reasons (and none) keep the ordinary refusal: exit 5,
// the server's message, and no mismatch rendering or envelope extensions.
func TestResumeOtherRefusalsAreNotMismatch(t *testing.T) {
	for _, ext := range []string{`"code":"RESUME_REFUSED","reason":"WORKER_HELD"`, `"code":"RESUME_REFUSED"`} {
		dir := teamGitDir(t)
		t.Setenv("HADRON_LAUNCH_GRANT", "")
		path := writeBinding(t, dir)
		stubs := mismatchStubs("CLAIM")
		stubs["ResumeTeamSession"] = `{"errors":[{"message":"the worker is dormant","extensions":{` + ext + `}}],"data":null}`
		gql, _ := captureGraphQL(t, stubs)
		f, out := testFactory(t)
		f.JSON = true
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "start", "--json", "--server", gql.URL})
		err := root.Execute()
		if exitCodeFor(err) != exitcode.Conflict || strings.Contains(err.Error(), "untouched") {
			t.Errorf("%s: ordinary refusal expected: %v", ext, err)
		}
		renderError(f, err)
		if strings.Contains(out.String(), "expectedWorkerId") || strings.Contains(out.String(), `"reason":"WORKER_MISMATCH"`) {
			t.Errorf("%s: no mismatch extensions: %s", ext, out.String())
		}
		if data, _ := os.ReadFile(path); string(data) != bindingFixture {
			t.Error("binding must stay unchanged")
		}
	}
}

// C1 controls (CSI as U+009B) are controls too: no bytes a terminal acts on.
func TestResumeMismatchSanitizesC1(t *testing.T) {
	_, err := runMismatch(t, `"picked":false,"expectedWorkerId":"wkr1","expectedWorkerName":"Ir\u009b31mis","foundWorkerId":"wkr9","foundWorkerName":"Gre\u0085er","foundSource":"CLAIM"`, false)
	for _, r := range err.Error() {
		if r >= 0x80 && r <= 0x9f {
			t.Fatalf("C1 control %U in the refusal", r)
		}
	}
}
