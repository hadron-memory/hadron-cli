package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1353 — the CLI side of hadron-server#1353/#1362: `team chat mark-read` and
// the worker-session header on `team chat read`. The attention commands this
// file also covered are retired (cli#935, team_retire_935_test.go).

// attnCall is one request as the server saw it: which operation, with what
// variables, and whether it carried the worker-session header.
type attnCall struct {
	Op      string
	Vars    map[string]any
	Session string
	Query   string
	RawOp   string
	// RawVars is the request's `variables` exactly as sent — for a test that
	// must see the wire bytes, not a decode of them (an explicit null vs an
	// omitted key survives a decode, but key order and encoding do not).
	RawVars json.RawMessage
}

// attnServer answers by operation name and records every call in order. An
// operation with no canned answer fails the test: a command that sends one
// it should not is the bug.
func attnServer(t *testing.T, responses map[string]string) (*httptest.Server, *[]attnCall) {
	t.Helper()
	return attnServerHook(t, responses, nil)
}

// attnServerHook is attnServer with a hook run as each operation arrives —
// how a test stages something another agent does mid-command.
// attnRespMu guards the responses map handed to attnServerHook: the fake answers
// requests concurrently, and a hook that restages a response must not race the
// lookup. Hooks write through setAttnResponse and never hold this lock while
// blocking.
var attnRespMu sync.RWMutex

func setAttnResponse(m map[string]string, op, resp string) {
	attnRespMu.Lock()
	defer attnRespMu.Unlock()
	m[op] = resp
}

func attnServerHook(t *testing.T, responses map[string]string, onOp func(op string)) (*httptest.Server, *[]attnCall) {
	t.Helper()
	calls := &[]attnCall{}
	var mu sync.Mutex // independent reads may arrive together (cli#1677)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
			Query         string         `json:"query"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		var rawBody struct {
			Variables json.RawMessage `json:"variables"`
		}
		_ = json.Unmarshal(raw, &rawBody)
		rawOp := body.OperationName
		body.OperationName = legacyChatReadOperation(body.OperationName)
		// #914's ended-binding read is answered (open) but not recorded: these
		// tests pin their command's own request order, and the notice's read
		// is pinned by team_ended_binding_test.go.
		if body.OperationName != "GetBoundSessionEnd" {
			// onOp runs OUTSIDE the lock: a test that stages a barrier across
			// concurrent requests (cli#1677) must be able to block in it.
			if onOp != nil {
				onOp(body.OperationName)
			}
			mu.Lock()
			*calls = append(*calls, attnCall{Op: body.OperationName, Vars: body.Variables, Session: r.Header.Get("X-Hadron-Session"), Query: body.Query, RawOp: rawOp, RawVars: rawBody.Variables})
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		// A hook may restage a response (setAttnResponse) while a concurrent
		// request is being answered (cli#1677), so reads take the same lock.
		attnRespMu.RLock()
		resp, ok := responses[rawOp]
		if !ok {
			resp, ok = responses[body.OperationName]
		}
		if !ok {
			resp, ok = workerFromStaff(responses, body.OperationName, rawBody.Variables)
		}
		if !ok && (body.OperationName == "TeamChatReadHead" || body.OperationName == "TeamChatReadMetadata") {
			resp, ok = teamReadHeadFixture(responses["TeamChatMessages"]), true
		}
		attnRespMu.RUnlock()
		if !ok && (body.OperationName == "ChannelReadState" || body.OperationName == "GetBoundSessionEnd" || body.OperationName == "GetWorkerProfile" || body.OperationName == "GetWorkerTerminal" || body.OperationName == "GetWorkerModelGroups" || body.OperationName == "GetWorkerRendered" || body.OperationName == "GetWorkerAvailability" || body.OperationName == "GetWorkerBootPromptServe" || body.OperationName == "AppAgentEffectiveRoster" || body.OperationName == "GetWorkerRetireAdvisory") {
			resp, ok = unstubbedDefault(body.OperationName)
		}
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func gqlErrorJSON(code string) string {
	return `{"errors":[{"message":"refused: ` + code + `","extensions":{"code":"` + code + `"}}]}`
}

func exitOf(err error) int {
	var coded *exitcode.CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	if err == nil {
		return exitcode.OK
	}
	return exitcode.Error
}

func writeTeamBinding(t *testing.T) {
	t.Helper()
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatalf("write binding: %v", err)
	}
}

func setTeamBindingServer(t *testing.T, server string) {
	t.Helper()
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := json.Marshal(server)
	if err != nil {
		t.Fatal(err)
	}
	bound := strings.Replace(string(b), `"startedAt"`, `"server":`+string(quoted)+`,"startedAt"`, 1)
	if err := os.WriteFile(path, []byte(bound), 0o600); err != nil {
		t.Fatal(err)
	}
}

func missingFieldJSON(field string) string {
	return `{"errors":[{"message":"Cannot query field \"` + field + `\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
}

// ── team chat read marks the bound worker read AFTER delivery ───────────────
//
// PR #732 kept reads headerless to prevent pre-delivery marks. #817 now
// carries attribution with explicit suppression. After a successful render, a read
// the binding's watermark rule counts is marked read explicitly, through what it
// delivered.

func chatReadResponses() map[string]string {
	return map[string]string{
		"TeamChatMessages":    teamChatPage(2, 1, 2),
		"TeamAppIdentity":     teamAppIdentityJSON,
		"TeamDefaultChannel":  `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`,
		"MarkOwnTeamChatRead": `{"data":{"markOwnTeamChatRead":{"workerId":"wkr1","channelId":"ch1","lastSeenSeq":2}}}`,
	}
}

func opsOf(calls []attnCall) []string {
	ops := []string{}
	for _, c := range calls {
		ops = append(ops, c.Op)
	}
	return ops
}

func TestTeamChatReadMarksTheBoundWorkerReadAfterDelivery(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, chatReadResponses())
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var mark *attnCall
	for i, c := range *calls {
		switch c.Op {
		case "TeamChatMessages":
			// Attribution is safe only with explicit suppression (#817).
			if c.Session != "s-new" || !strings.Contains(c.Query, "advanceReadState: false") {
				t.Errorf("read attribution lacks suppression: %+v", c)
			}
		case "MarkOwnTeamChatRead":
			mark = &(*calls)[i]
		}
	}
	if mark == nil {
		t.Fatalf("a counted read must be marked read on the server, got %v", opsOf(*calls))
	}
	if mark.Vars["seq"] != float64(2) || mark.Vars["sessionRef"] != "s-new" || mark.Vars["channelRef"] != "ch1" {
		t.Errorf("mark through the highest DELIVERED seq, for the bound session, on the team Channel: %v", mark.Vars)
	}
	if mark.Session != "s-new" {
		t.Errorf("the mark carries the session header, got %q", mark.Session)
	}
	if last := (*calls)[len(*calls)-1]; last.Op != "MarkOwnTeamChatRead" {
		t.Errorf("the mark must be the LAST call, after the read; got %v", opsOf(*calls))
	}
}

// A contiguous forward --limit page is a genuine prefix, so it counts; a
// filtered read does not.
func TestTeamChatReadMarksABoundedPageButNotAFilteredRead(t *testing.T) {
	for _, tc := range []struct {
		extra []string
		mark  bool
	}{{[]string{"--since", "0", "--limit", "5"}, true}, {[]string{"--limit", "5"}, false},
		{[]string{"--mentions-me"}, false}, {[]string{"--before", "3"}, false}} {
		t.Run(strings.Join(tc.extra, " "), func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, chatReadResponses())
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "chat", "read", "--json", "--server", srv.URL}, tc.extra...))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			marked := false
			for _, c := range *calls {
				marked = marked || c.Op == "MarkOwnTeamChatRead"
			}
			if marked != tc.mark {
				t.Errorf("marked = %v, want %v (%v)", marked, tc.mark, opsOf(*calls))
			}
		})
	}
}

// A cursorless tail is one page, not a checkpoint, even when it appears to
// contain the complete chat. Count and fetch can race (#1538).
func TestTeamChatReadTailNeverMarksServerRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		page string
	}{
		{"partial tail", teamChatPage(402, 401, 402)},
		{"apparently complete tail", teamChatPage(2, 1, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			r := chatReadResponses()
			r["TeamChatMessages"] = tc.page
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--limit", "2", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Op == "MarkOwnTeamChatRead" || c.Op == "TeamDefaultChannel" {
					t.Errorf("cursorless tail must not be marked read: %v", opsOf(*calls))
				}
			}
		})
	}
}

// Nothing delivered, nothing marked: a render that fails must not mark the
// messages it failed to show.
func TestTeamChatReadMarksNothingWhenTheRenderFails(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, chatReadResponses())
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	f.IOStreams.Out = failingWriter{}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a failed render must fail the read")
	}
	for _, c := range *calls {
		if c.Op == "MarkOwnTeamChatRead" || (c.Session != "" && !strings.Contains(c.Query, "advanceReadState: false")) {
			t.Errorf("nothing may be marked read when nothing was shown: %v (session %q)", c.Op, c.Session)
		}
	}
}

// An older server may refuse the mark with FEATURE_NOT_AVAILABLE: silent, and
// the read still succeeds. Any other failure is a stderr note, never a failure.
func TestTeamChatReadMarkFailureNeverFailsTheRead(t *testing.T) {
	for _, tc := range []struct {
		code string
		note bool
	}{{"FEATURE_NOT_AVAILABLE", false}, {"SESSION_NOT_LIVE", true}} {
		t.Run(tc.code, func(t *testing.T) {
			writeTeamBinding(t)
			r := chatReadResponses()
			r["MarkOwnTeamChatRead"] = gqlErrorJSON(tc.code)
			srv, _ := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("a failed server mark must not fail the read: %v", err)
			}
			if !strings.Contains(out.String(), `"nextSince": 2`) {
				t.Errorf("the read's output must be intact: %s", out.String())
			}
			stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
			if got := strings.Contains(stderr, "not recorded on the server"); got != tc.note {
				t.Errorf("stderr note = %v, want %v: %q", got, tc.note, stderr)
			}
		})
	}
}

func TestTeamChatReadWithoutABindingMarksNothing(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, chatReadResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, c := range *calls {
		if c.Session != "" || c.Op == "MarkOwnTeamChatRead" {
			t.Errorf("an unbound read must neither carry a session nor mark: %v %q", c.Op, c.Session)
		}
	}
}

func TestTeamChatReadWithSessionlessBindingMarksNothing(t *testing.T) {
	dir := teamGitDir(t)
	bound := strings.Replace(bindingFixture, `"sessionId":"s-new"`, `"sessionId":""`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bound), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, calls := attnServer(t, chatReadResponses())
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, c := range *calls {
		if c.Op == "MarkOwnTeamChatRead" {
			t.Errorf("a binding without a session must not mark server read state: %v", opsOf(*calls))
		}
	}
}

// A binding made against ANOTHER server: its session id means nothing here.
func TestTeamChatReadAgainstAnotherServerMarksNothing(t *testing.T) {
	dir := teamGitDir(t)
	other := strings.Replace(bindingFixture, `"startedAt"`, `"server":"https://elsewhere.example","startedAt"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, calls := attnServer(t, chatReadResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, c := range *calls {
		if c.Session != "" || c.Op == "MarkOwnTeamChatRead" {
			t.Errorf("a cross-server read must neither carry the binding's session nor mark: %v %q", c.Op, c.Session)
		}
	}
}

// ── team chat mark-read ─────────────────────────────────────────────────────

const markReadJSON = `{"data":{"markOwnTeamChatRead":{"workerId":"wkr1","channelId":"ch1","lastSeenSeq":1878}}}`

func TestTeamChatMarkReadDefaultsToTheTeamChannelAndPinsTheSession(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{
		"TeamDefaultChannel":  `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`,
		"MarkOwnTeamChatRead": markReadJSON,
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "1878", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 2 || (*calls)[0].Op != "TeamDefaultChannel" || (*calls)[1].Op != "MarkOwnTeamChatRead" {
		t.Fatalf("want the default-Channel lookup then the mark, got %+v", *calls)
	}
	m := (*calls)[1]
	for k, want := range map[string]any{"appRef": "capp100000000000000000000", "sessionRef": "s-new", "channelRef": "ch1", "seq": float64(1878)} {
		if m.Vars[k] != want {
			t.Errorf("%s = %v, want %v", k, m.Vars[k], want)
		}
	}
	if m.Session != "s-new" {
		t.Errorf("mark-read must carry X-Hadron-Session, got %q", m.Session)
	}
	if !strings.Contains(out.String(), `"lastSeenSeq": 1878`) {
		t.Errorf("result must pass through: %s", out.String())
	}
}

func TestTeamChatMarkReadWithAChannelSkipsTheLookup(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{"MarkOwnTeamChatRead": markReadJSON})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "1878", "--channel", "chats:dm-ada", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["channelRef"] != "chats:dm-ada" {
		t.Fatalf("--channel must be forwarded with no lookup, got %+v", *calls)
	}
}

func TestTeamChatMarkReadRefusesLegacyBindingWithoutServer(t *testing.T) {
	writeTeamBinding(t) // Pre-server binding: session and App IDs have no deployment provenance.
	srv, calls := attnServer(t, map[string]string{"MarkOwnTeamChatRead": markReadJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--channel", "ch1", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("unknown binding server: exit = %d, want 2", got)
	}
	if len(*calls) != 0 {
		t.Errorf("no cursor may be advanced through an unverified deployment: %+v", *calls)
	}
}

func TestTeamChatReadNeverAutoMarksLegacyBindingWithoutServer(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, chatReadResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the read itself must still work: %v", err)
	}
	for _, c := range *calls {
		if c.Op == "MarkOwnTeamChatRead" {
			t.Errorf("a legacy binding must not auto-mark on an unknown server: %v", opsOf(*calls))
		}
	}
}

// A cursor only moves forward: asking to mark BELOW it changes nothing, and
// the human output says so rather than claiming a mark.
func TestTeamChatMarkReadBelowTheCursorSaysNothingChanged(t *testing.T) {
	writeTeamBinding(t)
	srv, _ := attnServer(t, map[string]string{"MarkOwnTeamChatRead": markReadJSON})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "10", "--channel", "ch1", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "already read through #1878") {
		t.Errorf("got %s", out.String())
	}
}

func TestTeamChatMarkReadRefusals(t *testing.T) {
	t.Run("no binding", func(t *testing.T) {
		teamGitDir(t)
		srv, calls := attnServer(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--app", "acme.com:eng-team", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.Usage {
			t.Errorf("exit = %d, want %d", got, exitcode.Usage)
		}
		if len(*calls) != 0 {
			t.Errorf("refused before any request, got %+v", *calls)
		}
	})
	t.Run("no --through", func(t *testing.T) {
		writeTeamBinding(t)
		srv, calls := attnServer(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.Usage {
			t.Errorf("exit = %d, want %d", got, exitcode.Usage)
		}
		if len(*calls) != 0 {
			t.Errorf("refused before any request, got %+v", *calls)
		}
	})
	t.Run("beyond the watermark", func(t *testing.T) {
		writeTeamBinding(t)
		srv, _ := attnServer(t, map[string]string{"MarkOwnTeamChatRead": gqlErrorJSON("SEQ_BEYOND_WATERMARK")})
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--through", "999999", "--channel", "ch1", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.Usage {
			t.Errorf("exit = %d, want %d", got, exitcode.Usage)
		}
	})
	t.Run("legacy server refusal", func(t *testing.T) {
		writeTeamBinding(t)
		srv, _ := attnServer(t, map[string]string{"MarkOwnTeamChatRead": gqlErrorJSON("FEATURE_NOT_AVAILABLE")})
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--channel", "ch1", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.Forbidden {
			t.Errorf("exit = %d, want %d", got, exitcode.Forbidden)
		}
	})
	t.Run("App without a team chat", func(t *testing.T) {
		writeTeamBinding(t)
		srv, _ := attnServer(t, map[string]string{
			"TeamDefaultChannel": `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":null}}}`,
		})
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.NotFound {
			t.Errorf("exit = %d, want %d", got, exitcode.NotFound)
		}
	})
}

// An EMPTY --channel is an unset variable, not "the team chat": refused before
// any request, where the absent path would mark a different Channel read.
func TestTeamChatMarkReadRefusesAnEmptyChannel(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--channel", "", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("exit = %d, want %d", got, exitcode.Usage)
	}
	if len(*calls) != 0 {
		t.Errorf("refused before any request, got %+v", *calls)
	}
}

// The receipt names WHERE it marked — the Channel, the App and which branch
// resolved the App — since the App came from an ambient binding.
func TestTeamChatMarkReadReceiptNamesItsScope(t *testing.T) {
	writeTeamBinding(t)
	srv, _ := attnServer(t, map[string]string{"MarkOwnTeamChatRead": markReadJSON})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "1878", "--channel", "ch1", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "Marked read through #1878 for Iris on channel ch1 in capp100000000000000000000 (from the worktree binding)."
	if !strings.Contains(out.String(), want) {
		t.Errorf("got %q, want it to contain %q", out.String(), want)
	}
}

// PR #732 round 2, @copilot: the server mark goes out only if the worktree is
// STILL bound to the session the read was for. A `session end` or a rebind
// that wins while the messages render retires that session here, and marking
// its cursor anyway would act for a binding that no longer exists. "Someone
// already read further" is still ours, so that one still marks.
func TestTeamChatReadMarksOnlyWhileTheBindingIsStillOurs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(path string)
		marks bool
	}{
		{"a rebind to another session", func(path string) {
			_ = os.WriteFile(path, []byte(strings.Replace(bindingFixture, `"s-new"`, `"s-other"`, 1)), 0o600)
		}, false},
		{"session end removed the binding", func(path string) { _ = os.Remove(path) }, false},
		{"the same session read further meanwhile", func(path string) {
			_ = os.WriteFile(path, []byte(strings.Replace(bindingFixture, `"startedAt"`, `"chatSeenSeq":99,"startedAt"`, 1)), 0o600)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			srv, calls := attnServerHook(t, chatReadResponses(), func(op string) {
				if op == "TeamChatMessages" {
					tc.edit(path) // lands while this command is mid-read
				}
			})
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--app", "capp100000000000000000000", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			marked := false
			for _, c := range *calls {
				marked = marked || c.Op == "MarkOwnTeamChatRead"
			}
			if marked != tc.marks {
				t.Errorf("marked = %v, want %v (%v)", marked, tc.marks, opsOf(*calls))
			}
		})
	}
}

// PR #732 round 2, @copilot: explicit mark-read re-checks the binding right
// before the mutation — a rebind during the Channel lookup must not mark the
// retired session's cursor.
func TestTeamChatMarkReadRefusesARebindDuringTheCommand(t *testing.T) {
	writeTeamBinding(t)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	srv, calls := attnServerHook(t, map[string]string{
		"TeamDefaultChannel":  `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`,
		"MarkOwnTeamChatRead": markReadJSON,
	}, func(op string) {
		if op == "TeamDefaultChannel" {
			_ = os.WriteFile(path, []byte(strings.Replace(bindingFixture, `"s-new"`, `"s-other"`, 1)), 0o600)
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Conflict {
		t.Errorf("exit = %d, want %d", got, exitcode.Conflict)
	}
	for _, c := range *calls {
		if c.Op == "MarkOwnTeamChatRead" {
			t.Errorf("a retired session's cursor must not be marked: %v", opsOf(*calls))
		}
	}
}

// …and the same for `chat read`'s post-delivery mark, whose Channel lookup
// sits between its lock check and the mark.
func TestTeamChatReadSkipsTheMarkOnARebindDuringTheLookup(t *testing.T) {
	writeTeamBinding(t)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	srv, calls := attnServerHook(t, chatReadResponses(), func(op string) {
		if op == "TeamDefaultChannel" {
			_ = os.WriteFile(path, []byte(strings.Replace(bindingFixture, `"s-new"`, `"s-other"`, 1)), 0o600)
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "capp100000000000000000000", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the read itself must still succeed: %v", err)
	}
	for _, c := range *calls {
		if c.Op == "MarkOwnTeamChatRead" {
			t.Errorf("rebound mid-lookup: no mark, got %v", opsOf(*calls))
		}
	}
}
