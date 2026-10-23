package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #995 B1: `team session sync-model` / `set-model` report the holder's model
// through the existing updateSession. Fixtures are synthetic files only.

const syncNative = "aaaaaaaa-0000-0000-0000-000000000001"

func sessionWithTranscript(path string) string {
	tp := "null"
	if path != "" {
		b, _ := json.Marshal(path)
		tp = string(b)
	}
	return `{"data":{"session":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
	"type":"DEVELOPER","repo":null,"branch":null,"prNumber":null,
	"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
	"transcriptPath":` + tp + `,"llmModel":null}}}`
}

func syncRecord(model string) string {
	return `{"type":"assistant","sessionId":"` + syncNative + `","message":{"model":"` + model + `"}}` + "\n"
}

type syncEnv struct {
	root, transcript, binding string
}

// setupSync points the Claude transcript root at a temp dir, binds s-new, and
// writes a transcript there.
func setupSync(t *testing.T, body string) syncEnv {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CODEX_HOME", t.TempDir())
	root := filepath.Join(cfg, "projects")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	tp := filepath.Join(root, "t.jsonl")
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(teamGitDir(t), "hadron-team-session.json")
	if err := os.WriteFile(b, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return syncEnv{root: root, transcript: tp, binding: b}
}

func hookJSON(t *testing.T, env syncEnv) string {
	t.Helper()
	cwd, _ := os.Getwd()
	b, _ := json.Marshal(map[string]string{"session_id": syncNative, "transcript_path": env.transcript, "cwd": cwd})
	return string(b)
}

func runSync(t *testing.T, stdin string, gqlURL string, args ...string) (string, string, error) {
	t.Helper()
	f, out := testFactory(t)
	var errOut bytes.Buffer
	f.IOStreams.In = strings.NewReader(stdin)
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session"}, append(args, "--server", gqlURL)...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func updateVars(t *testing.T, captured map[string]json.RawMessage) map[string]any {
	t.Helper()
	raw, ok := captured["UpdateTeamSession"]
	if !ok {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSyncModelReportsOneModelOnlyWrite(t *testing.T) {
	env := setupSync(t, syncRecord("claude-opus-5-5")+syncRecord("claude-sonnet-5-5"))
	gql, captured := captureGraphQL(t, map[string]string{
		"GetTeamSession":    sessionWithTranscript(env.transcript),
		"UpdateTeamSession": modelLogSessionJSON,
	})
	if _, _, err := runSync(t, hookJSON(t, env), gql.URL, "sync-model", "--hook"); err != nil {
		t.Fatal(err)
	}
	v := updateVars(t, captured)
	if v == nil {
		t.Fatal("no UpdateTeamSession")
	}
	if v["id"] != "s-new" || v["llmModel"] != "claude-sonnet-5-5" {
		t.Errorf("vars = %v", v)
	}
	for _, k := range []string{"family", "prNumber", "branch"} {
		if _, present := v[k]; present {
			t.Errorf("%s must be omitted, vars = %v", k, v)
		}
	}
}

func TestSessionLogLeavesLLMModelOmitted(t *testing.T) {
	bindWorklogModelTest(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": modelLogSessionJSON,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null,"model":null}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, present := updateVars(t, captured)["llmModel"]; present {
		t.Error("session log must not send llmModel")
	}
}

func TestSyncModelZeroReports(t *testing.T) {
	cases := map[string]func(t *testing.T, env *syncEnv, stdin *string) string{
		"no recorded transcriptPath": func(t *testing.T, env *syncEnv, _ *string) string { return sessionWithTranscript("") },
		"hook path is not the recorded path": func(t *testing.T, env *syncEnv, _ *string) string {
			other := filepath.Join(env.root, "other.jsonl")
			_ = os.WriteFile(other, []byte(syncRecord("m")), 0o600)
			return sessionWithTranscript(other)
		},
		"native id mismatch": func(t *testing.T, env *syncEnv, stdin *string) string {
			*stdin = strings.Replace(*stdin, syncNative, "bbbbbbbb", 1)
			return sessionWithTranscript(env.transcript)
		},
		"hook cwd is another tree": func(t *testing.T, env *syncEnv, stdin *string) string {
			*stdin = strings.Replace(*stdin, `"cwd":"`, `"cwd":"/nonexistent-tree`, 1)
			return sessionWithTranscript(env.transcript)
		},
		"transcript outside roots": func(t *testing.T, env *syncEnv, _ *string) string {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			return sessionWithTranscript(env.transcript)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := setupSync(t, syncRecord("claude-opus-5-5"))
			stdin := hookJSON(t, env)
			resp := mutate(t, &env, &stdin)
			gql, captured := captureGraphQL(t, map[string]string{"GetTeamSession": resp, "UpdateTeamSession": modelLogSessionJSON})
			_, errOut, err := runSync(t, stdin, gql.URL, "sync-model", "--hook")
			if err != nil {
				t.Fatalf("hook mode must exit 0: %v", err)
			}
			if updateVars(t, captured) != nil {
				t.Error("must report nothing")
			}
			if strings.Contains(errOut, env.root) || strings.Contains(errOut, "t.jsonl") {
				t.Errorf("diagnostic leaks a path: %q", errOut)
			}
		})
	}
}

func TestSyncModelNoBindingZeroReports(t *testing.T) {
	env := setupSync(t, syncRecord("m"))
	if err := os.Remove(env.binding); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTeamSession": modelLogSessionJSON})
	if _, _, err := runSync(t, hookJSON(t, env), gql.URL, "sync-model", "--hook"); err != nil {
		t.Fatal(err)
	}
	if updateVars(t, captured) != nil {
		t.Error("no binding: zero reports")
	}
}

func TestSyncModelBindingReplacedDuringReadDiscards(t *testing.T) {
	env := setupSync(t, syncRecord("claude-opus-5-5"))
	calls := 0
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "GetTeamSession":
			// A rebind lands while the observation is in flight.
			calls++
			swapped := strings.Replace(bindingWithTeamFixture, `"sessionId":"s-new"`, `"sessionId":"s-successor"`, 1)
			_ = os.WriteFile(env.binding, []byte(swapped), 0o600)
			return sessionWithTranscript(env.transcript)
		case "UpdateTeamSession":
			return modelLogSessionJSON
		}
		return ""
	})
	if _, _, err := runSync(t, hookJSON(t, env), gql.URL, "sync-model", "--hook"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("successor must not be retried: GetTeamSession calls = %d", calls)
	}
	if updateVars(t, captured) != nil {
		t.Error("replaced binding: discard, zero reports")
	}
}

func TestSyncModelManualUnavailableStatusAndHookExit(t *testing.T) {
	env := setupSync(t, syncRecord("m"))
	gql, captured := captureGraphQL(t, map[string]string{"GetTeamSession": sessionWithTranscript(""), "UpdateTeamSession": modelLogSessionJSON})
	_, _, err := runSync(t, hookJSON(t, env), gql.URL, "sync-model")
	var ce *exitcode.CodedError
	if !errors.As(err, &ce) || ce.Code != exitcode.ModelUnavailable {
		t.Fatalf("manual run: want exit %d, got %v", exitcode.ModelUnavailable, err)
	}
	if updateVars(t, captured) != nil {
		t.Error("zero reports")
	}
}

func TestSyncModelMalformedStdin(t *testing.T) {
	env := setupSync(t, syncRecord("m"))
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTeamSession": modelLogSessionJSON})
	if _, _, err := runSync(t, "not json", gql.URL, "sync-model", "--hook"); err != nil {
		t.Fatalf("hook mode exits 0: %v", err)
	}
	_ = env
	if updateVars(t, captured) != nil {
		t.Error("zero reports")
	}
}

func TestSetModelSendsModelOnly(t *testing.T) {
	setupSync(t, "")
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTeamSession": modelLogSessionJSON})
	if _, _, err := runSync(t, "", gql.URL, "set-model", "gpt-5.1-codex"); err != nil {
		t.Fatal(err)
	}
	v := updateVars(t, captured)
	if v["id"] != "s-new" || v["llmModel"] != "gpt-5.1-codex" {
		t.Errorf("vars = %v", v)
	}
	for _, k := range []string{"family", "prNumber", "branch"} {
		if _, present := v[k]; present {
			t.Errorf("%s must be omitted", k)
		}
	}
}

func TestSetModelRejectsUnboundedModel(t *testing.T) {
	setupSync(t, "")
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTeamSession": modelLogSessionJSON})
	for _, bad := range []string{strings.Repeat("a", 129), "a\nb", " "} {
		_, _, err := runSync(t, "", gql.URL, "set-model", bad)
		var ce *exitcode.CodedError
		if !errors.As(err, &ce) || ce.Code != exitcode.Usage {
			t.Errorf("%q: want usage error, got %v", bad, err)
		}
	}
	if updateVars(t, captured) != nil {
		t.Error("nothing may be sent")
	}
}

func TestSetModelNoBindingIsUsageError(t *testing.T) {
	env := setupSync(t, "")
	_ = os.Remove(env.binding)
	gql, _ := captureGraphQL(t, map[string]string{"UpdateTeamSession": modelLogSessionJSON})
	_, _, err := runSync(t, "", gql.URL, "set-model", "m")
	if err == nil {
		t.Fatal("want an error with no binding")
	}
}
