package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1959: the follow-ups from the cli#1954 BLIND review. Both documents
// (the default test and the opt-in chat probe) go through their own request
// branch and a typed adapter, so each case runs on both.

// A completed but FAILED run: every detail the server sends must survive.
const aiTestFailed1959 = `{"ok":false,"provider":"openai","model":"m-1959","endpoint":"https://llm.example.test/v1","apiStyle":"CHAT_COMPLETIONS","latencyMs":431,"reply":null,"error":{"code":"PROVIDER_ERROR","message":"upstream said no","transient":true},"used":{"provider":"SAVED","model":"OVERRIDE","endpoint":"OVERRIDE","apiStyle":"SAVED","key":"OVERRIDE","params":"DEFAULT"},"knobs":[{"param":"temperature","state":"REJECTED","detail":"out of range"},{"param":"top_p","state":"ACCEPTED","detail":null}]}`

func aiTestFailedWithChat1959(status string) string {
	return strings.TrimSuffix(aiTestFailed1959, "}") + `,"chat":{"status":"` + status + `"}}`
}

type aiRun1959 struct {
	stdout, stderr string
	vars           map[string]json.RawMessage
	calls          []string
	exit           int
}

func runAiTest1959(t *testing.T, respond func(op string) string, args ...string) aiRun1959 {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		calls = append(calls, op)
		mu.Unlock()
		return respond(op)
	})
	f, out := testFactory(t)
	var stderr bytes.Buffer
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"ai-config", "test", "cfg1", "--server", gql.URL}, args...))
	exit := exitcode.OK
	if err := root.Execute(); err != nil {
		exit = renderError(f, err)
	}
	mu.Lock()
	defer mu.Unlock()
	return aiRun1959{stdout: out.String(), stderr: stderr.String(), vars: captured, calls: append([]string{}, calls...), exit: exit}
}

func aiBranches1959() []struct {
	name, op string
	args     []string
	result   func() string
} {
	return []struct {
		name, op string
		args     []string
		result   func() string
	}{
		{"default", "TestAiServiceConfig", nil, func() string { return aiTestFailed1959 }},
		{"chat-probe", "TestAiServiceConfigWithChatProbe", []string{"--chat-probe"}, func() string { return aiTestFailedWithChat1959("FAIL_TEXT_ONLY") }},
	}
}

// Case 1 + 4: ok:false together with a chat outcome is a COMPLETED diagnostic,
// so it exits 0 for every chat status, and the two outcomes print apart.
func TestAiConfig1959FailedRunWithChatOutcomeExitsZeroAndPrintsBoth(t *testing.T) {
	for _, status := range []string{"PASS", "FAIL_TEXT_ONLY", "FAIL_PROVIDER_UNAVAILABLE", "UNSUPPORTED_PROVIDER", "FAIL_SOME_FUTURE_VALUE"} {
		t.Run(status, func(t *testing.T) {
			resp := func(string) string {
				return `{"data":{"testAiServiceConfig":` + aiTestFailedWithChat1959(status) + `}}`
			}
			j := runAiTest1959(t, resp, "--chat-probe", "--json")
			if j.exit != exitcode.OK {
				t.Fatalf("a completed failed run must exit 0 whatever the chat status, got %d: %s", j.exit, j.stderr)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(j.stdout), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["ok"] != false || dto["chat"] != status {
				t.Fatalf("json must carry both outcomes as sent: %s", j.stdout)
			}
			text := runAiTest1959(t, resp, "--chat-probe")
			if text.exit != exitcode.OK {
				t.Fatalf("text exit %d", text.exit)
			}
			if !strings.HasPrefix(text.stdout, "failed  ") || !strings.Contains(text.stdout, "\nchat: "+status+"\n") {
				t.Fatalf("want the failed run state and a separate chat line:\n%s", text.stdout)
			}
		})
	}
}

// Case 4 on the default document too: ok:false is not an exit status.
func TestAiConfig1959FailedRunExitsZeroOnBothDocuments(t *testing.T) {
	for _, b := range aiBranches1959() {
		t.Run(b.name, func(t *testing.T) {
			r := runAiTest1959(t, func(string) string { return `{"data":{"testAiServiceConfig":` + b.result() + `}}` }, b.args...)
			if r.exit != exitcode.OK || r.stderr != "" {
				t.Fatalf("exit %d stderr %q", r.exit, r.stderr)
			}
			if len(r.calls) != 1 || r.calls[0] != b.op {
				t.Fatalf("calls %v", r.calls)
			}
		})
	}
}

// Case 3: a failed run's details are preserved field for field through both
// request branches and the shared conversion.
func TestAiConfig1959FailedRunDetailsArePreserved(t *testing.T) {
	want := map[string]any{
		"ok": false, "provider": "openai", "model": "m-1959", "endpoint": "https://llm.example.test/v1", "apiStyle": "CHAT_COMPLETIONS",
		"latencyMs": float64(431), "reply": nil,
		"error": map[string]any{"code": "PROVIDER_ERROR", "message": "upstream said no", "transient": true},
		"used":  map[string]any{"provider": "SAVED", "model": "OVERRIDE", "endpoint": "OVERRIDE", "apiStyle": "SAVED", "key": "OVERRIDE", "params": "DEFAULT"},
		"knobs": []any{
			map[string]any{"param": "temperature", "state": "REJECTED", "detail": "out of range"},
			map[string]any{"param": "top_p", "state": "ACCEPTED", "detail": nil},
		},
	}
	for _, b := range aiBranches1959() {
		t.Run(b.name, func(t *testing.T) {
			r := runAiTest1959(t, func(string) string { return `{"data":{"testAiServiceConfig":` + b.result() + `}}` }, append(b.args, "--json")...)
			if r.exit != exitcode.OK {
				t.Fatalf("exit %d: %s", r.exit, r.stderr)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
				t.Fatal(err)
			}
			wantChat := any(nil)
			if b.name == "chat-probe" {
				wantChat = "FAIL_TEXT_ONLY"
			}
			if got["chat"] != wantChat {
				t.Fatalf("chat %v, want %v", got["chat"], wantChat)
			}
			delete(got, "chat")
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("failed-run details changed in conversion:\n got %s\nwant %s", gotJSON, wantJSON)
			}
			text := runAiTest1959(t, func(string) string { return `{"data":{"testAiServiceConfig":` + b.result() + `}}` }, b.args...)
			for _, line := range []string{
				"failed  openai/m-1959  431 ms\n",
				"endpoint: https://llm.example.test/v1\n",
				"API style: CHAT_COMPLETIONS\n",
				"PROVIDER_ERROR: upstream said no (transient: true)\n",
				"used: provider=SAVED model=OVERRIDE endpoint=OVERRIDE apiStyle=SAVED key=OVERRIDE params=DEFAULT\n",
				"temperature: REJECTED out of range\n",
				"top_p: ACCEPTED \n",
			} {
				if !strings.Contains(text.stdout, line) {
					t.Fatalf("text lost %q:\n%s", line, text.stdout)
				}
			}
		})
	}
}

// Case 2, pinning cor:dmo:050:10: an override carrying a submitted key is
// forwarded on BOTH request branches, and the key is never printed — not in
// text output, not in --json output, not in any error message.
func TestAiConfig1959SubmittedKeyIsForwardedAndNeverPrinted(t *testing.T) {
	const key = "sk-1959-submitted-secret"
	quoted, _ := json.Marshal(key)
	echo := "provider rejected apiKey=" + key + " encoded=" + string(quoted)
	path := filepath.Join(t.TempDir(), "override.json")
	if err := os.WriteFile(path, []byte(`{"apiKey":"`+key+`","model":"m-1959"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	completedEcho := func(b string) string {
		var v map[string]any
		_ = json.Unmarshal([]byte(b), &v)
		v["error"] = map[string]any{"code": "PROVIDER_ERROR " + key, "message": echo, "transient": false}
		v["reply"] = "fine"
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	refusal, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": echo, "extensions": map[string]any{"code": "BAD_USER_INPUT", "detail": echo}}}})
	for _, b := range aiBranches1959() {
		for _, mode := range []string{"text", "json"} {
			args := append([]string{"--override-file", path}, b.args...)
			if mode == "json" {
				args = append(args, "--json")
			}
			t.Run(b.name+"/"+mode+"/completed", func(t *testing.T) {
				r := runAiTest1959(t, func(string) string { return `{"data":{"testAiServiceConfig":` + completedEcho(b.result()) + `}}` }, args...)
				if r.exit != exitcode.OK {
					t.Fatalf("exit %d: %s", r.exit, r.stderr)
				}
				assertForwarded1959(t, r, b.op, key)
				assertNeverPrinted1959(t, r, key)
				if !strings.Contains(r.stdout, "[redacted]") {
					t.Fatalf("the echoed key must be replaced, not the field dropped:\n%s", r.stdout)
				}
			})
			t.Run(b.name+"/"+mode+"/refused", func(t *testing.T) {
				r := runAiTest1959(t, func(string) string { return string(refusal) }, args...)
				if r.exit != exitcode.Usage {
					t.Fatalf("exit %d, want %d: %s", r.exit, exitcode.Usage, r.stderr)
				}
				if r.stderr == "" && r.stdout == "" {
					t.Fatal("a refusal must say something")
				}
				assertForwarded1959(t, r, b.op, key)
				assertNeverPrinted1959(t, r, key)
			})
		}
	}
}

func assertForwarded1959(t *testing.T, r aiRun1959, op, key string) {
	t.Helper()
	if len(r.calls) != 1 || r.calls[0] != op {
		t.Fatalf("calls %v, want exactly %s", r.calls, op)
	}
	override, ok := unmarshalVars(t, r.vars[op])["override"].(map[string]any)
	if !ok || override["apiKey"] != key || override["model"] != "m-1959" {
		t.Fatalf("the submitted override must reach the server unchanged on %s", op)
	}
}

func assertNeverPrinted1959(t *testing.T, r aiRun1959, key string) {
	t.Helper()
	quoted, _ := json.Marshal(key)
	for _, form := range []string{key, string(quoted[1 : len(quoted)-1]), "sk-1959"} {
		if strings.Contains(r.stdout, form) || strings.Contains(r.stderr, form) {
			t.Fatalf("submitted key printed (%q) in output or error", form)
		}
	}
}

// The help states the exit contract once and unambiguously: a completed test
// exits 0 whatever either outcome says; only request and invalid-response
// errors use the CLI's error exits. The old "follows the run alone" sentence
// contradicted the ok:false line before it.
func TestAiConfig1959HelpStatesOneExitContract(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"ai-config", "test"})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Join(strings.Fields(cmd.Long), " ")
	if strings.Contains(long, "follows the run alone") {
		t.Fatalf("the help must not tie the exit status to the run result:\n%s", cmd.Long)
	}
	for _, want := range []string{
		"exits 0 whatever ok and the chat status say",
		"neither stands for the other",
		"invalid response",
	} {
		if !strings.Contains(long, want) {
			t.Fatalf("help missing %q:\n%s", want, cmd.Long)
		}
	}
}
