package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1353 — the CLI side of hadron-server#1353/#1362: `team attention`, its
// switchover, `team chat mark-read`, and the worker-session header on
// `team chat read`.

// attnCall is one request as the server saw it: which operation, with what
// variables, and whether it carried the worker-session header.
type attnCall struct {
	Op      string
	Vars    map[string]any
	Session string
}

// attnServer answers by operation name and records every call in order. An
// operation with no canned answer fails the test: a command that sends one
// it should not is the bug.
func attnServer(t *testing.T, responses map[string]string) (*httptest.Server, *[]attnCall) {
	t.Helper()
	calls := &[]attnCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls = append(*calls, attnCall{Op: body.OperationName, Vars: body.Variables, Session: r.Header.Get("X-Hadron-Session")})
		w.Header().Set("Content-Type", "application/json")
		resp, ok := responses[body.OperationName]
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

const attentionJSON = `{"data":{"teamAttention":{"token":"tok-2","workers":[
	{"worker":"wkr1","name":"Iris","urn":"hrn:worker:acme.com:eng-team:iris","live":true,"channels":[
		{"channel":"ch1","name":"team","unread":3,"unreadMentions":1,"firstUnreadSeq":41,"lastSeq":43}]}]}}}`

func TestTeamAttentionPollPassesTheTokenThroughAndNeverSendsAnEmptySince(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttention": attentionJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("one poll, got %d calls", len(*calls))
	}
	// No --since is "no token" — OMITTED, never sent as null or "".
	if _, sent := (*calls)[0].Vars["since"]; sent {
		t.Errorf("an absent --since must be omitted, got %v", (*calls)[0].Vars["since"])
	}
	var dto struct {
		App     string `json:"app"`
		Token   string `json:"token"`
		Workers []struct {
			Name     string `json:"name"`
			Live     bool   `json:"live"`
			Channels []struct {
				Unread         int  `json:"unread"`
				UnreadMentions int  `json:"unreadMentions"`
				FirstUnreadSeq *int `json:"firstUnreadSeq"`
				LastSeq        int  `json:"lastSeq"`
			} `json:"channels"`
		} `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if dto.Token != "tok-2" || len(dto.Workers) != 1 || dto.Workers[0].Name != "Iris" {
		t.Fatalf("unexpected dto: %+v", dto)
	}
	c := dto.Workers[0].Channels[0]
	if c.Unread != 3 || c.UnreadMentions != 1 || c.FirstUnreadSeq == nil || *c.FirstUnreadSeq != 41 || c.LastSeq != 43 {
		t.Errorf("channel counts must pass through verbatim: %+v", c)
	}

	// The next poll hands the token back verbatim.
	*calls = nil
	f2, _ := testFactory(t)
	root = NewRootCmd(f2)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--since", "tok-2", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (*calls)[0].Vars["since"]; got != "tok-2" {
		t.Errorf("--since must reach the server verbatim, got %v", got)
	}
}

// An EMPTY --since is refused before any request: it is nearly always an unset
// shell variable, and answering the no-token question instead would re-nudge
// every worker's whole backlog.
func TestTeamAttentionRefusesAnEmptySince(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--since", " ", "--server", srv.URL})
	err := root.Execute()
	if exitOf(err) != exitcode.Usage {
		t.Fatalf("exit = %d, want %d: %v", exitOf(err), exitcode.Usage, err)
	}
	if len(*calls) != 0 {
		t.Errorf("refused before any request, got %d", len(*calls))
	}
}

func TestTeamAttentionEmptyListRendersAsAnArray(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{
		"TeamAttention": `{"data":{"teamAttention":{"token":"tok-3","workers":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"workers": []`) {
		t.Errorf("an idle poll must render workers as [], not null: %s", out.String())
	}
}

// The pilot gate and the token/proof refusals exit per the documented
// contract, not the generic 1.
func TestTeamAttentionRefusalsMapToExitCodes(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"FEATURE_NOT_AVAILABLE", exitcode.Forbidden},
		{"INVALID_ATTENTION_TOKEN", exitcode.Usage},
		{"SWITCHOVER_CONFIRMATION_REQUIRED", exitcode.Usage},
		{"ATTENTION_TOKEN_STALE", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			teamGitDir(t)
			srv, _ := attnServer(t, map[string]string{"TeamAttention": gqlErrorJSON(tc.code)})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--since", "x", "--server", srv.URL})
			if got := exitOf(root.Execute()); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}

// The attention poll is an OPERATOR read, not a worker's: it never carries the
// worktree's session, even from a bound worktree.
func TestTeamAttentionNeverSendsTheWorkerSession(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttention": attentionJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (*calls)[0].Session; got != "" {
		t.Errorf("attention must not carry X-Hadron-Session, got %q", got)
	}
	if got := (*calls)[0].Vars["appRef"]; got != "capp100000000000000000000" {
		t.Errorf("with no --app the binding's App answers, got %v", got)
	}
}

const switchoverPreviewJSON = `{"data":{"teamAttentionSwitchoverPreview":{"proof":"prf-1","expiresAt":"2026-09-25T20:40:00Z","workers":[
	{"worker":"wkr1","name":"Iris","urn":null,"channels":[
		{"channel":"ch1","name":"team","fromSeq":0,"throughSeq":1878,"unread":1878,"unreadMentions":12}]}]}}}`

func TestTeamAttentionSwitchoverPreviewIsReadOnlyAndPrintsTheApplyCommand(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttentionSwitchoverPreview": switchoverPreviewJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "TeamAttentionSwitchoverPreview" {
		t.Fatalf("preview is ONE read, got %+v", *calls)
	}
	for _, want := range []string{"#0", "#1878", "Nothing has been written", "switchover apply --proof 'prf-1'"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTeamAttentionSwitchoverApplyNeedsProofAndConsent(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{
		"ConfirmTeamAttentionSwitchover": `{"data":{"confirmTeamAttentionSwitchover":{"applied":true,"workersAdvanced":2,"channelsAdvanced":3}}}`,
	})

	// No --proof: refused before any request.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--yes", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("apply without --proof: exit = %d, want %d", got, exitcode.Usage)
	}

	// Non-interactive without --yes: refused, and NOTHING is sent — the
	// mutation discards a backlog.
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--proof", "prf-1", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("apply without --yes non-interactively: exit = %d, want %d", got, exitcode.Usage)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused apply must send nothing, got %+v", *calls)
	}

	// A TTY that declines: cancelled, nothing sent.
	f, _, _ = testFactoryTTY(t, "n\n")
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--proof", "prf-1", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Cancelled {
		t.Errorf("declined apply: exit = %d, want %d", got, exitcode.Cancelled)
	}
	if len(*calls) != 0 {
		t.Fatalf("a declined apply must send nothing, got %+v", *calls)
	}

	// --yes: the proof reaches the server verbatim.
	f, out := testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--proof", "prf-1", "--yes", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["proof"] != "prf-1" {
		t.Fatalf("apply must send the proof verbatim, got %+v", *calls)
	}
	if !strings.Contains(out.String(), `"workersAdvanced": 2`) {
		t.Errorf("result must pass through: %s", out.String())
	}
}

func TestTeamAttentionSwitchoverStaleProofIsAConflict(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{"ConfirmTeamAttentionSwitchover": gqlErrorJSON("SWITCHOVER_PROOF_STALE")})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--proof", "prf-1", "--yes", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Conflict {
		t.Errorf("stale proof: exit = %d, want %d (preview again)", got, exitcode.Conflict)
	}
}

// ── team chat read carries the bound worker's session ────────────────────────

func chatReadResponses() map[string]string {
	return map[string]string{
		"TeamChatMessages": teamChatPage(2, 1, 2),
		"TeamAppIdentity":  teamAppIdentityJSON,
	}
}

// A bound worker's read carries its session — that is what lets the server
// (under the #1353 pilot) count the read as the worker's own — and ONLY the
// chat read does: the App-identity lookup around it does not.
func TestTeamChatReadCarriesTheBoundSession(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, chatReadResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	saw := false
	for _, c := range *calls {
		switch c.Op {
		case "TeamChatMessages":
			saw = true
			if c.Session != "s-new" {
				t.Errorf("a bound read must carry X-Hadron-Session s-new, got %q", c.Session)
			}
		default:
			if c.Session != "" {
				t.Errorf("%s must not carry the session header, got %q", c.Op, c.Session)
			}
		}
	}
	if !saw {
		t.Fatal("no TeamChatMessages call")
	}
}

// The header rides regardless of filters: the SERVER decides which reads
// count (unfiltered, contiguous, forward — a --limit page included, when it
// is contiguous). The CLI must not second-guess it by withholding the header.
func TestTeamChatReadSendsTheSessionOnBoundedAndFilteredReadsToo(t *testing.T) {
	for _, extra := range [][]string{{"--limit", "5"}, {"--mentions-me"}} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, chatReadResponses())
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "chat", "read", "--json", "--server", srv.URL}, extra...))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			for _, c := range *calls {
				if c.Op == "TeamChatMessages" && c.Session != "s-new" {
					t.Errorf("got %q", c.Session)
				}
			}
		})
	}
}

func TestTeamChatReadWithoutABindingSendsNoSession(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, chatReadResponses())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Errorf("%s: an unbound read must not carry a session, got %q", c.Op, c.Session)
		}
	}
}

// A binding made against ANOTHER server: its session id means nothing here.
func TestTeamChatReadAgainstAnotherServerSendsNoSession(t *testing.T) {
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
		if c.Session != "" {
			t.Errorf("%s: a cross-server read must not carry the binding's session, got %q", c.Op, c.Session)
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

// A cursor only moves forward: asking to mark BELOW it changes nothing, and
// the human output says so rather than claiming a mark.
func TestTeamChatMarkReadBelowTheCursorSaysNothingChanged(t *testing.T) {
	writeTeamBinding(t)
	srv, _ := attnServer(t, map[string]string{"MarkOwnTeamChatRead": markReadJSON})
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
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--through", "999999", "--channel", "ch1", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.Usage {
			t.Errorf("exit = %d, want %d", got, exitcode.Usage)
		}
	})
	t.Run("pilot gate", func(t *testing.T) {
		writeTeamBinding(t)
		srv, _ := attnServer(t, map[string]string{"MarkOwnTeamChatRead": gqlErrorJSON("FEATURE_NOT_AVAILABLE")})
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
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "mark-read", "--through", "5", "--server", srv.URL})
		if got := exitOf(root.Execute()); got != exitcode.NotFound {
			t.Errorf("exit = %d, want %d", got, exitcode.NotFound)
		}
	})
}
