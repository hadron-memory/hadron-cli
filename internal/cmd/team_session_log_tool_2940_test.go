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
//     empty; a binding that already records a tool keeps it, and a --tool
//     naming a DIFFERENT tool there (the server's token equality is
//     trim+lowercase, so case and space do not make a tool different) is
//     refused with one line, not silently used.
//   - ONE SPELLING: the tool on the milestone is the SERVER's stored spelling
//     (the response's, not the flag's), and the binding persists that same
//     spelling, so later milestones are consistent (and need no flag).
//   - NO SERVER WRITE for the repair: the local binding is what changes; the
//     tool rides the milestone's row as always.
//   - A LABEL, NEVER AUTHORIZATION.
//   - Blank/whitespace-only --tool is refused client-side, and so is a
//     toolless binding without the flag — before any request, and wherever a
//     worklog row will be written (recorded App, team memory, or -m), so the
//     server's exit-2 "tool must be a non-empty string" cannot recur.

// bindingNoToolFixture is bindingWithTeamFixture with the tool empty — the
// legacy shape whose only defect is the missing tool.
const bindingNoToolFixture = `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","appId":"capp100000000000000000000","startedAt":"2026-08-11T10:00:00Z","appBound":true,
	"teamMemory":"hrn:mem:acme.com:eng-team","tool":"",
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// bindingTeamMemoryNoToolFixture records NO App (appId empty) but a team
// memory: pre-App-recording, yet `session log` resolves the worklog App from
// the team memory, so its rows need a tool exactly like a modern binding's —
// the toolless-without-flag refusal must cover it too (PR #2966 blind F2).
const bindingTeamMemoryNoToolFixture = `{"sessionId":"s-old","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","startedAt":"2026-08-01T10:00:00Z",
	"teamMemory":"hrn:mem:acme.com:eng-team","tool":"","repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// bindingDegradedNoToolFixture records neither an App nor a team memory (the
// pre-#399 degraded path: --pr writes only the Session denormalization, no
// worklog row) AND no tool.
const bindingDegradedNoToolFixture = `{"sessionId":"s-old","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","startedAt":"2026-08-01T10:00:00Z",
	"tool":"","repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// logStubs2940 is the operation set a successful `session log --pr` against an
// App-recording binding issues (record, session touch, chat-note reads). The
// record answer ECHOES the server's normalized spelling "claude-code" —
// deliberately different from the mixed-case flags the tests send, so a
// passing spelling assertion proves the client used the RESPONSE's value, not
// its own (PR #2966 blind F5).
func logStubs2940() map[string]string {
	return map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
		"TeamMemoryApp":      `{"data":{"memory":{"id":"mem1","appId":"capp100000000000000000000"}}}`,
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
	errOutBuilder, ok := f.IOStreams.ErrOut.(*strings.Builder)
	if !ok {
		t.Fatalf("testFactory ErrOut is %T, want *strings.Builder", f.IOStreams.ErrOut)
	}
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "log", "--server", url}, args...))
	err := root.Execute()
	return out.String(), errOutBuilder.String(), err
}

// bindingWithTool2940 returns fixture with its tool set to tool, rewritten
// through JSON rather than a string replace, so reformatting the fixture cannot
// turn the rewrite into a silent no-op (cli#3177 item 3). It fails when the
// fixture has no tool key at all.
func bindingWithTool2940(t *testing.T, fixture, tool string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(fixture), &m); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	if _, ok := m["tool"]; !ok {
		t.Fatal("fixture has no tool key to rewrite")
	}
	m["tool"] = tool
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// assertNoFillNote2940 fails when either stream carries a fill note: the
// concurrent-divergence note or the failed-local-fill note. Both streams, so a
// note moved to stdout cannot pass vacuously (cli#3177 item 4).
func assertNoFillNote2940(t *testing.T, out, errOut, why string) {
	t.Helper()
	for name, text := range map[string]string{"stdout": out, "stderr": errOut} {
		if strings.Contains(text, "concurrent log") || strings.Contains(text, "filling this binding's tool locally failed") {
			t.Errorf("%s: no fill note may print on %s, got %q", why, name, text)
		}
	}
}

// recordedTool2940 returns the tool sent on the LAST RecordTeamWork capture,
// failing when that capture's ref is not wantRef — the ref check is what makes
// a last-capture read trustworthy across a test's second log (a stale first
// capture would carry the earlier ref).
func recordedTool2940(t *testing.T, captured map[string]json.RawMessage, wantRef string) string {
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
	if vars.Ref != wantRef {
		t.Fatalf("RecordTeamWork ref = %q, want %q (a stale capture from an earlier log would show the earlier ref)", vars.Ref, wantRef)
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

// THE REPAIR, end to end: the first --tool records the milestone and fills the
// binding with the SERVER's stored spelling (the fake echoes "claude-code" for
// a " Claude-Code " flag — same token after the server's trim+lowercase), so a
// SECOND log without the flag reuses that one spelling.
func TestSessionLogToolFillsToollessBindingAndPersists(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	out, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", " Claude-Code ")
	if err != nil {
		t.Fatalf("log with --tool on a toolless binding: %v", err)
	}
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#371"); got != "Claude-Code" {
		t.Errorf("the CLI sends the trimmed flag value (the server normalizes), got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the fill must persist the SERVER's spelling, not the flag's, binding still records %q", got)
	}
	if !strings.Contains(out, "✓ logged") {
		t.Errorf("missing success line: %q", out)
	}

	// Later milestones need no flag: the filled binding supplies the tool, in
	// the stored spelling. The second log uses a DIFFERENT ref, so the
	// last-capture read below cannot pass on the first log's capture.
	_, _, err = runLog2940(t, gql.URL, "--pr", "372")
	if err != nil {
		t.Fatalf("second log after the fill: %v", err)
	}
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#372"); got != "claude-code" {
		t.Errorf("a later milestone must reuse the server-stored spelling, got %q", got)
	}
}

// FILL, NEVER CHANGE: a binding that already records a tool keeps it; a --tool
// naming a DIFFERENT tool is refused with one line and nothing is sent. The
// SAME token restates — whatever its case or surrounding space was — because
// the server's token equality is trim+lowercase: refusing "Claude-Code" after
// storing "claude-code" would call a restatement a change (PR #2966 blind F1).
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

	// The same token is a restatement, not a change — in any case or spacing.
	if _, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "claude-code"); err != nil {
		t.Fatalf("the same --tool must be accepted: %v", err)
	}
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#371"); got != "claude-code" {
		t.Errorf("the milestone must record the binding's tool, got %q", got)
	}
	if _, _, err := runLog2940(t, gql.URL, "--pr", "372", "--tool", "  Claude-Code  "); err != nil {
		t.Fatalf("a case/space variant of the recorded token must be accepted as the same tool: %v", err)
	}
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#372"); got != "claude-code" {
		t.Errorf("the restatement must record the binding's stored spelling, got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the binding must keep its tool through a restatement, got %q", got)
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

// The same refusal covers a pre-App-recording binding that resolves its
// worklog App from a recorded TEAM MEMORY: its rows are worklog rows too, and
// before this guard they surfaced the defect as the opaque server exit 2
// (PR #2966 blind F2). The repair lands there — the fill is not App-gated —
// so refusing with --tool named is honest.
func TestSessionLogToollessTeamMemoryBindingRefusesClientSide(t *testing.T) {
	path := bindFixture2940(t, bindingTeamMemoryNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	_, _, err := runLog2940(t, gql.URL, "--pr", "371")
	if err == nil {
		t.Fatal("a toolless team-memory binding without --tool must be refused client-side")
	}
	if !strings.Contains(err.Error(), "--tool") {
		t.Errorf("the refusal must name the repair: %v", err)
	}
	for _, op := range []string{"RecordTeamWork", "UpdateTeamSession"} {
		if _, called := captured[op]; called {
			t.Errorf("the refusal must precede every write: %s was called", op)
		}
	}
	if got := boundTool2940(t, path); got != "" {
		t.Errorf("the refusal must not write the binding, got %q", got)
	}

	// And the repair lands: with --tool the row records and the binding fills.
	if _, _, err := runLog2940(t, gql.URL, "--pr", "372", "--tool", "Claude-Code"); err != nil {
		t.Fatalf("--tool must repair a toolless team-memory binding: %v", err)
	}
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#372"); got != "Claude-Code" {
		t.Errorf("the row must record the trimmed flag, got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the binding must fill with the server's spelling, got %q", got)
	}
}

// A blank --tool is refused client-side before any call, with a message that
// says WHY (not a bare err != nil: the refusal text is the contract).
func TestSessionLogToolBlankRefused(t *testing.T) {
	bindFixture2940(t, bindingNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	_, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "   ")
	if err == nil {
		t.Fatal("a whitespace-only --tool must be refused")
	}
	if !strings.Contains(err.Error(), "--tool") || !strings.Contains(err.Error(), "cannot be blank") {
		t.Errorf("the refusal must say the flag cannot be blank: %v", err)
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
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#371"); got != "Claude-Code" {
		t.Errorf("the CLI sends the trimmed flag value (the server normalizes), got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the binding must store the server's stored spelling, got %q", got)
	}
}

// The degraded path writes NO worklog row, so there is no provenance to
// label: --tool is refused there rather than silently unused — with nothing
// half-applied and a message that says why. Without the flag the degraded
// behavior is unchanged (Session denormalization only).
func TestSessionLogToolOnDegradedBinding(t *testing.T) {
	path := bindFixture2940(t, bindingDegradedNoToolFixture)
	gql, captured := captureGraphQL(t, logStubs2940())

	_, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "claude-code")
	if err == nil {
		t.Fatal("--tool must be refused when no worklog row will be written")
	}
	if !strings.Contains(err.Error(), "no worklog row") {
		t.Errorf("the refusal must say no worklog row is written: %v", err)
	}
	for _, op := range []string{"RecordTeamWork", "UpdateTeamSession"} {
		if _, called := captured[op]; called {
			t.Errorf("the refusal must precede every write: %s was called", op)
		}
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

// A fill that fails AFTER the row recorded degrades to a stderr note and a
// successful exit: the row already carries the tool, the binding self-heals on
// the next log. Here the failure is the session changing under the fill (the
// fake rewrites the binding to another session while the record is in
// flight), which is the errBindingChangedSession branch (PR #2966 blind F5).
func TestSessionLogToolFillFailureNotesButRecords(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	stubs := logStubs2940()
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if op == "RecordTeamWork" {
			rewritten := strings.Replace(bindingNoToolFixture, `"sessionId":"s-new"`, `"sessionId":"s-other"`, 1)
			if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
				t.Errorf("rewrite binding mid-flight: %v", err)
			}
		}
		return stubs[op]
	})

	out, errOut, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "Claude-Code")
	if err != nil {
		t.Fatalf("a failed local fill must not fail the log (the row is recorded): %v", err)
	}
	if !strings.Contains(out, "✓ logged") {
		t.Errorf("missing success line: %q", out)
	}
	if !strings.Contains(errOut, "filling this binding's tool locally failed") {
		t.Errorf("the failed fill must leave a stderr note naming --tool for the next log: %q", errOut)
	}
}

// A concurrent log that filled a DIFFERENT tool while this row was in flight
// leaves the binding's tool and this row's tool diverging: fill-never-change
// must keep the concurrent value (nothing overwritten), the row keeps its own
// tool, and the divergence gets a stderr note, not silence (PR #2966 blind F6
// / local P3 — the note branch is pinned, deleting it fails here).
func TestSessionLogToolConcurrentDifferentFillNotes(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	stubs := logStubs2940()
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if op == "RecordTeamWork" {
			rewritten := bindingWithTool2940(t, bindingNoToolFixture, "codex-cli")
			if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
				t.Errorf("rewrite binding mid-flight: %v", err)
			}
		}
		return stubs[op]
	})

	out, errOut, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "Claude-Code")
	if err != nil {
		t.Fatalf("a concurrent fill must not fail the log: %v", err)
	}
	if !strings.Contains(out, "✓ logged") {
		t.Errorf("missing success line: %q", out)
	}
	if !strings.Contains(errOut, "concurrent log already filled this binding with tool \"codex-cli\"") ||
		!strings.Contains(errOut, `"claude-code"`) {
		t.Errorf("the divergence note must name both tools: %q", errOut)
	}
	if got := boundTool2940(t, path); got != "codex-cli" {
		t.Errorf("fill-never-change must keep the concurrent value, got %q", got)
	}
}

// The same-token arm of the concurrent-fill pin above (cli#2973): a concurrent
// log that filled the SAME token while this row was in flight is a
// restatement, not a divergence — no note (the EqualFold same-tool rule; the
// note's condition made always true — mutant M6 on PR #2966 — dies here), and
// fill-never-change keeps the concurrent value. The token's other case and
// spacing appears on both sides, one per scenario:
//
//   - the concurrent fill holds the server's spelling "claude-code" while
//     this invocation passes --tool "  Claude-Code  ": the binding's stored
//     spelling and the milestone's (the response's echo) agree — asserted as
//     a consistency check only, for the spellings coincide by construction
//     and a fill that overwrote would print the same value;
//   - the concurrent fill holds "Claude-Code", as a pre-#2940 binding spells
//     it: the binding must KEEP that spelling (an overwriting fill would
//     store the row's "claude-code" instead — this assertion discriminates),
//     and dropping the EqualFold (comparing case-sensitively) must not turn
//     the restatement into a raced divergence either.
func TestSessionLogToolConcurrentSameTokenFillStaysSilent(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	stubs := logStubs2940()
	rewrote := false
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		if op == "RecordTeamWork" {
			rewritten := bindingWithTool2940(t, bindingNoToolFixture, "claude-code")
			if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
				t.Errorf("rewrite binding mid-flight: %v", err)
			}
			rewrote = true
		}
		return stubs[op]
	})

	out, errOut, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "  Claude-Code  ")
	if err != nil {
		t.Fatalf("a same-token concurrent fill must not fail the log: %v", err)
	}
	if !strings.Contains(out, "✓ logged") {
		t.Errorf("missing success line: %q", out)
	}
	if !rewrote {
		t.Fatal("the mid-flight rewrite never ran: RecordTeamWork was not sent")
	}
	assertNoFillNote2940(t, out, errOut, "a restatement is not a divergence")
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#371"); got != "Claude-Code" {
		t.Errorf("the CLI sends the trimmed flag value (the server normalizes), got %q", got)
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the binding keeps the concurrent fill's stored spelling, got %q", got)
	}

	// The same race with the concurrent value spelled as a pre-#2940 binding
	// holds it: still the same token, so still silent, and still kept.
	path = bindFixture2940(t, bindingNoToolFixture)
	rewrote = false
	gql, captured = captureGraphQLFunc(t, func(op string) string {
		if op == "RecordTeamWork" {
			rewritten := bindingWithTool2940(t, bindingNoToolFixture, "Claude-Code")
			if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
				t.Errorf("rewrite binding mid-flight: %v", err)
			}
			rewrote = true
		}
		return stubs[op]
	})

	out, errOut, err = runLog2940(t, gql.URL, "--pr", "371", "--tool", "claude-code")
	if err != nil {
		t.Fatalf("a same-token concurrent fill in another spelling must not fail the log: %v", err)
	}
	if !rewrote {
		t.Fatal("the mid-flight rewrite never ran: RecordTeamWork was not sent")
	}
	if !strings.Contains(out, "✓ logged") {
		t.Errorf("missing success line: %q", out)
	}
	assertNoFillNote2940(t, out, errOut, "a restatement in another spelling is not a divergence")
	if got := recordedTool2940(t, captured, "hadron-memory/hadron-cli#371"); got != "claude-code" {
		t.Errorf("the CLI sends the trimmed flag value, got %q", got)
	}
	if got := boundTool2940(t, path); got != "Claude-Code" {
		t.Errorf("fill-never-change keeps the concurrent spelling it found, got %q", got)
	}
}

// An old server without per-milestone model attribution answers the modern
// record with a validation refusal for "model" and the CLI falls back to the
// legacy operation; the fill there must use the LEGACY response's spelling
// for the same one-spelling rule (PR #2966 blind F5).
func TestSessionLogToolLegacyServerFillUsesLegacySpelling(t *testing.T) {
	path := bindFixture2940(t, bindingNoToolFixture)
	stubs := logStubs2940()
	stubs["RecordTeamWork"] = `{"errors":[{"message":"Unknown argument \"model\" on field 'Mutation.recordTeamWork'.","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	stubs["RecordTeamWorkLegacy"] = `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
		"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
		"at":"2026-08-13T10:00:00Z","detail":null}}}`
	gql, captured := captureGraphQL(t, stubs)

	if _, _, err := runLog2940(t, gql.URL, "--pr", "371", "--tool", "Claude-Code"); err != nil {
		t.Fatalf("legacy-server log with --tool: %v", err)
	}
	if _, called := captured["RecordTeamWorkLegacy"]; !called {
		t.Fatal("the old server must be answered through the legacy operation")
	}
	if got := boundTool2940(t, path); got != "claude-code" {
		t.Errorf("the fill must persist the legacy response's spelling, got %q", got)
	}
}
