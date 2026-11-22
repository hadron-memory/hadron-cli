package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2940, remedy 1 only (CHECKED): a binding whose tool is empty (bound
// before --tool existed, or without it) can never record a worklog milestone —
// the server refuses an empty tool and the CLI sent the binding's value
// unconditionally. `session log --tool <name>` is the repair:
//
//   - FILL, NEVER CHANGE: the flag applies only when the binding's tool is
//     empty; a binding that already records a tool keeps it, and a DIFFERENT
//     --tool there is refused with one line, not silently used.
//   - ONE SPELLING: the tool on the milestone equals what the CLI then stores
//     locally, so later milestones are consistent (and need no flag).
//   - NO SERVER WRITE for the repair: the local binding is what changes.
//   - A LABEL, NEVER AUTHORIZATION.
//   - Blank/whitespace-only --tool is refused client-side, and so is a
//     toolless binding without the flag — before any request, so the server's
//     exit-2 "tool must be a non-empty string" cannot recur from here.

// bindingNoToolFixture is bindingWithTeamFixture with the tool empty — the
// legacy shape whose only defect is the missing tool.
const bindingNoToolFixture = `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","appId":"capp100000000000000000000","startedAt":"2026-08-11T10:00:00Z","appBound":true,
	"teamMemory":"hrn:mem:acme.com:eng-team","tool":"",
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// bindingDegradedNoToolFixture records neither an App nor a team memory (the
// pre-#399 degraded path: --pr writes only the Session denormalization, no
// worklog row) AND no tool.
const bindingDegradedNoToolFixture = `{"sessionId":"s-old","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","startedAt":"2026-08-01T10:00:00Z",
	"tool":"","repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// logStubs2940 is the operation set a successful `session log --pr` against an
// App-recording binding issues (record, session touch, chat-note reads).
func logStubs2940() map[string]string {
	return map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
		"TeamDefaultChannel": `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`,
		"CheckChatCursor":    `{"data":{"channelReadState":{"channelId":"ch1","channelIncarnation":3,"cursorIncarnation":3,"lastSeenSeq":90}}}`,
		"TeamChatMessages":   `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
	}
}

func bindFixture2940(t *testing.T, fixture string) string {
	t.Helper()
	path := filepath.Join(teamGitDir(t), "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runLog2940(t *testing.T, url string, args ...string) (string, string, error) {
	t.Helper()
	f, out := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "log", "--server", url}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func recordedTool2940(t *testing.T, captured map[string]json.RawMessage) string {
	t.Helper()
	raw, ok := captured["RecordTeamWork"]
	if !ok {
		t.Fatal("RecordTeamWork was not called")
	}
	var vars struct {
		Tool string `json:"tool"`
		Ref  string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("decode RecordTeamWork variables: %v", err)
	}
	if vars.Ref != "hadron-memory/hadron-cli#371" {
		t.Errorf("unexpected ref %q", vars.Ref)
	}
	return vars.Tool
}

func boundTool2940(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("binding is no longer valid JSON: %v", err)
	}
	return b.Tool
}

// THE REPAIR: the first --tool records the milestone under the named tool and
// fills the binding, so a SECOND log without the flag reuses the same
// spelling — condition 2 end to end.
func TestSessionLogToolFillsToollessBindingAndPersists(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	out, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "claude-code")
	if err != nil {
		t.Fatalf("log with --tool on a toolless binding: %v", err)
	}
	if got := recordedTool2940(t, captured); got != "claude-code" {
		t.Errorf("the milestone must record the flag's tool, got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the fill must persist to the binding (one spelling), binding still records %q", got)
	}
	if !strings.Contains(out, "✓ logged") {
		t.Errorf("missing success line: %q", out)
	}

	// Later milestones need no flag: the filled binding supplies the tool.
	_, _, err = runLog2940(t, gql.URL, "--pr", "371")
	if err != nil {
		t.Fatalf("second log after the fill: %v", err)
	}
	if got := recordedTool2940(t, captured); got != "claude-code" {
		t.Errorf("a later milestone must reuse the filled spelling, got %q", got)
	}
}

// FILL, NEVER CHANGE: a binding that already records a tool keeps it; a
// DIFFERENT --tool is refused with one line and nothing is sent. The SAME
// value is accepted (an idempotent restatement, not a change).
func TestSessionLogToolRefusedWhenBindingAlreadyHasOne(t *testing.T) {
	path := bindFixture2940(t, bindingWithTeamFixture)

	gql, captured := captureGraphQL(t, logStubs2940())
	_, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "codex-cli")
	if err == nil {
		t.Fatal("a different --tool on a recorded tool must be refused")
	}
	if !strings.Contains(err.Error(), "already records tool") || !strings.Contains(err.Error(), "claude-code") {
		t.Errorf("the refusal must name the recorded tool in one line: %v", err)
	}
	for _, op := range []string{"RecordTeamWork", "UpdateTeamSession"} {
		if _, called := captured[op]; called {
			t.Errorf("the refusal must precede every write: %s was called", op)
		}
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the binding must keep its tool, got %q", got)
	}

	// The same value is a restatement, not a change: the milestone records.
	if _, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "claude-code"); err != nil {
		t.Fatalf("the same --tool must be accepted: %v", err)
	}
	if got := recordedTool2940(t, captured); got != "claude-code" {
		t.Errorf("the milestone must record the binding's tool, got %q", got)
	}
}

// The defect itself, closed client-side: a toolless binding without the flag
// is refused BEFORE any request, with the repair named — the server's
// exit-2 "tool must be a non-empty string" cannot recur from this command.
func TestSessionLogToollessWithoutFlagRefusesClientSide(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	// The report's own argv (issue #2940), verbatim.
	_, _, err := runLog2940(t, gql.URL, "--pr", "hadron-memory/hadron-cli#2932", "--action", "pushed")
	if err == nil {
		t.Fatal("a toolless binding without --tool must be refused client-side")
	}
	if !strings.Contains(err.Error(), "--tool") {
		t.Errorf("the refusal must name the repair: %v", err)
	}
	for _, op := range []string{"RecordTeamWork", "UpdateTeamSession"} {
		if _, called := captured[op]; called {
			t.Errorf("nothing may be half-applied: %s was called", op)
		}
	}
	if got := boundTool2940(t, path); got != "" {
		t.Errorf("the refusal must not write the binding, got %q", got)
	}
}

// Condition 5: blank is refused client-side before any call.
func TestSessionLogToolBlankRefused(t *testing.T) {
	bindFixture2940(t, bindingNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	_, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "   ")
	if err == nil {
		t.Fatal("a whitespace-only --tool must be refused")
	}
	for _, op := range []string{"RecordTeamWork", "UpdateTeamSession"} {
		if _, called := captured[op]; called {
			t.Errorf("the refusal must precede every write: %s was called", op)
		}
	}
}

// A whitespace-recorded tool is the same as none, and ONE SPELLING means the
// server's: the row stores the server's normalized token (vocabToken trims
// and lowercases — the fake here echoes "claude-code" for a "Claude-Code "
// flag), so the binding must persist the RESPONSE's spelling, not the flag's,
// or later milestones and the provenance query would diverge from the row.
func TestSessionLogToolTreatsWhitespaceToolAsNone(t *testing.T) {
	path := bindFixture2940(t, strings.Replace(bindingNoToolFixture, `"tool":""`, `"tool":"   "`, 1))
	gql, captured := captureGraphQL(t, logStubs2940())

	if _, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", " Claude-Code "); err != nil {
		t.Fatalf("a whitespace tool is empty for this purpose: %v", err)
	}
	if got := recordedTool2940(t, captured); got != "Claude-Code" {
		t.Errorf("the CLI sends the trimmed flag value (the server normalizes), got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the binding must store the server's stored spelling, got %q", got)
	}
}

// The degraded path writes NO worklog row, so there is no provenance to
// label: --tool is refused there rather than silently unused. Without the
// flag the degraded behavior is unchanged (Session denormalization only).
func TestSessionLogToolOnDegradedBinding(t *testing.T) {
	path := bindFixture2940(t, bindingDegradedNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	_, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "claude-code")
	if err == nil {
		t.Fatal("--tool must be refused when no worklog row will be written")
	}
	if _, called := captured["RecordTeamWork"]; called {
		t.Error("the degraded path never records a worklog row")
	}

	// Without the flag the degraded path keeps today's shape: the milestone
	// goes to the Session field only, and the binding keeps no tool.
	out, errOut, err := runLog2940(t, gql.URL, "--pr", "371")
	if err != nil {
		t.Fatalf("degraded log without --tool must keep working: %v", err)
	}
	if !strings.Contains(out, "✓ logged") || !strings.Contains(errOut, "Session field") {
		t.Errorf("degraded note missing: out=%q errOut=%q", out, errOut)
	}
	if _, called := captured["RecordTeamWork"]; called {
		t.Error("the degraded path never records a worklog row")
	}
	if got := boundTool2940(t, path); got != "" {
		t.Errorf("the degraded path must not fill a tool, got %q", got)
	}
}
