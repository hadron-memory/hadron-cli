package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#960: `session log` refuses when the bound session has no tool, which used
// to surface late, while recording a PR or merge. A successful start/resume that
// leaves the binding without a tool now says so at the bind, on stderr only.

const toolWarningText = "has no tool recorded"

func noToolSession() string {
	return strings.Replace(startedSessionJSON, `"tool":"claude-code"`, `"tool":null`, 1)
}

type bindRun struct {
	out, errOut string
	err         error
	captured    map[string]json.RawMessage
}

func runBind(t *testing.T, stubs map[string]string, args ...string) bindRun {
	t.Helper()
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	gql, captured := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "session", "start"}, args...), "--server", gql.URL))
	err := root.Execute()
	return bindRun{out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err, captured}
}

func TestSessionStartWithoutToolWarnsOnceOnStderr(t *testing.T) {
	stubs := startWithAffinityStubs(irisWorkerJSON, map[string]string{
		"StartTeamSession": `{"data":{"startSession":` + noToolSession() + `}}`,
	})
	for _, jsonMode := range []bool{false, true} {
		args := []string{"--as", "wkr1"}
		if jsonMode {
			args = append(args, "--json")
		}
		r := runBind(t, stubs, args...)
		if r.err != nil {
			t.Fatalf("a warning is not a failed bind: %v", r.err)
		}
		if n := strings.Count(r.errOut, toolWarningText); n != 1 {
			t.Errorf("json=%v: want the warning exactly once on stderr, got %d: %q", jsonMode, n, r.errOut)
		}
		if !strings.Contains(r.errOut, "--tool") {
			t.Errorf("the warning must name the fix (--tool): %q", r.errOut)
		}
		if strings.Contains(r.out, toolWarningText) {
			t.Errorf("json=%v: the warning belongs on stderr, not stdout: %q", jsonMode, r.out)
		}
		if jsonMode {
			var v map[string]any
			if err := json.Unmarshal([]byte(r.out), &v); err != nil {
				t.Fatalf("--json stdout must stay valid JSON: %v", err)
			}
			if _, has := v["warning"]; has || v["session"] == nil {
				t.Errorf("--json shape must be unchanged: %v", v)
			}
		}
		for _, op := range []string{"EndTeamSession", "UpdateTeamSession"} {
			if _, called := r.captured[op]; called {
				t.Errorf("a warning must not mutate: %s was called", op)
			}
		}
		if strings.Contains(r.errOut, "/tmp/t.jsonl") || strings.Contains(r.errOut, "hdr_") {
			t.Errorf("the warning must not print a transcript path or credential: %q", r.errOut)
		}
	}
}

func TestSessionStartWithToolIsQuiet(t *testing.T) {
	r := runBind(t, startWithAffinityStubs(irisWorkerJSON, nil), "--as", "wkr1", "--tool", "claude-code")
	if r.err != nil || strings.Contains(r.errOut, toolWarningText) {
		t.Errorf("a bind that has a tool must not warn: err=%v stderr=%q", r.err, r.errOut)
	}
}

func TestSessionResumeKnownToolIsQuietWithoutTheFlag(t *testing.T) {
	// startedSessionJSON records claude-code; this invocation omits --tool.
	r := runBind(t, resumeStubs("RESUMED"))
	if r.err != nil || strings.Contains(r.errOut, toolWarningText) {
		t.Errorf("a known-tool resume must stay quiet when --tool is omitted: err=%v stderr=%q", r.err, r.errOut)
	}
}

func TestSessionResumeLegacyNoToolWarnsOnceAndTheFlagFillsIt(t *testing.T) {
	legacy := resumeStubs("RESUMED")
	legacy["ResumeTeamSession"] = strings.Replace(legacy["ResumeTeamSession"], `"tool":"claude-code"`, `"tool":null`, 1)
	r := runBind(t, legacy)
	if r.err != nil || strings.Count(r.errOut, toolWarningText) != 1 {
		t.Errorf("a legacy no-tool resume must warn once: err=%v stderr=%q", r.err, r.errOut)
	}
	// With --tool the binding records it (a flag fills only what the session
	// lacks), so log will work and there is nothing to warn about.
	r = runBind(t, legacy, "--tool", "claude-code")
	if r.err != nil || strings.Contains(r.errOut, toolWarningText) {
		t.Errorf("--tool fills the binding, so no warning: err=%v stderr=%q", r.err, r.errOut)
	}
}

func TestSessionStartLaunchGrantWithoutToolWarnsOnce(t *testing.T) {
	stubs := launchGrantStubs()
	stubs["StartTeamSession"] = `{"data":{"startSession":` + noToolSession() + `}}`
	r := runBind(t, stubs, "--launch-grant", "hlg_x")
	if r.err != nil || strings.Count(r.errOut, toolWarningText) != 1 {
		t.Errorf("a launched bind without a tool must warn exactly once: err=%v stderr=%q", r.err, r.errOut)
	}
}
