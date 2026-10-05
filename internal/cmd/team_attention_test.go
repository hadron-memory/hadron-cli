package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
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
	return attnServerHook(t, responses, nil)
}

// attnServerHook is attnServer with a hook run as each operation arrives —
// how a test stages something another agent does mid-command.
func attnServerHook(t *testing.T, responses map[string]string, onOp func(op string)) (*httptest.Server, *[]attnCall) {
	t.Helper()
	calls := &[]attnCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if onOp != nil {
			onOp(body.OperationName)
		}
		*calls = append(*calls, attnCall{Op: body.OperationName, Vars: body.Variables, Session: r.Header.Get("X-Hadron-Session")})
		w.Header().Set("Content-Type", "application/json")
		resp, ok := responses[body.OperationName]
		if !ok && body.OperationName == "TeamChatReadHead" {
			resp, ok = teamReadHeadFixture(responses["TeamChatMessages"]), true
		}
		if !ok && body.OperationName == "ChannelReadState" {
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

const attentionJSON = `{"data":{"teamAttentionPage":{"scanComplete":true,"nextPage":null,"adoptableSince":"tok-2","items":[
	{"worker":"wkr1","workerName":"Iris","workerUrn":"hrn:worker:acme.com:eng-team:iris",
	 "channel":"ch1","channelName":"team","unread":3,"unreadMentions":1,"firstUnreadSeq":41,"lastSeq":43}]}}}`

const legacyAttentionJSON = `{"data":{"teamAttention":{"token":"old-token","workers":[
	{"worker":"wkr1","name":"Iris","urn":null,"live":true,"channels":[
		{"channel":"ch1","name":"team","unread":3,"unreadMentions":1,"firstUnreadSeq":41,"lastSeq":43}]}]}}}`

func missingFieldJSON(field string) string {
	return `{"errors":[{"message":"Cannot query field \"` + field + `\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
}

func TestTeamAttentionFallsBackOnlyWhenPagedFieldIsMissing(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{
		"TeamAttentionPage": missingFieldJSON("teamAttentionPage"),
		"TeamAttention":     legacyAttentionJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := opsOf(*calls); !reflect.DeepEqual(got, []string{"TeamAttentionPage", "TeamAttention"}) {
		t.Fatalf("fallback calls = %v", got)
	}
	if !strings.Contains(out.String(), `"token": "old-token"`) {
		t.Fatalf("legacy result was lost: %s", out.String())
	}
}

func TestTeamAttentionHTTP400ValidationFallsBackForPollAndPreview(t *testing.T) {
	teamGitDir(t)
	var ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		ops = append(ops, body.OperationName)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamAttentionPage":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(missingFieldJSON("teamAttentionPage")))
		case "TeamAttentionPreviewPage":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(missingFieldJSON("teamAttentionPreviewPage")))
		case "TeamAttention":
			_, _ = w.Write([]byte(legacyAttentionJSON))
		case "TeamAttentionSwitchoverPreview":
			_, _ = w.Write([]byte(legacySwitchoverPreviewJSON))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
		}
	}))
	defer srv.Close()
	for _, command := range [][]string{
		{"team", "attention"},
		{"team", "attention", "switchover", "preview"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(command, "--app", "acme.com:eng-team", "--json", "--server", srv.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", command, err)
		}
	}
	if want := []string{"TeamAttentionPage", "TeamAttention", "TeamAttentionPreviewPage", "TeamAttentionSwitchoverPreview"}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("HTTP 400 fallback calls = %v, want %v", ops, want)
	}
}

func TestTeamAttentionPollPassesTheTokenThroughAndNeverSendsAnEmptySince(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttentionPage": attentionJSON})
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
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--since", " tok-2 ", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (*calls)[0].Vars["since"]; got != " tok-2 " {
		t.Errorf("--since must reach the server verbatim, got %v", got)
	}
}

func TestTeamAttentionDrainsPagesBeforeReturningAnAdoptableToken(t *testing.T) {
	teamGitDir(t)
	calls := []attnCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		calls = append(calls, attnCall{Op: body.OperationName, Vars: body.Variables})
		w.Header().Set("Content-Type", "application/json")
		if len(calls) == 1 {
			_, _ = w.Write([]byte(`{"data":{"teamAttentionPage":{"scanComplete":false,"nextPage":"page-two","adoptableSince":null,"items":[{"worker":"w1","workerName":"Ada","workerUrn":null,"channel":"c1","channelName":"team","unread":2,"unreadMentions":1,"firstUnreadSeq":4,"lastSeq":8}]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"teamAttentionPage":{"scanComplete":true,"nextPage":null,"adoptableSince":"final-token","items":[{"worker":"w2","workerName":"Jonas","workerUrn":null,"channel":"c1","channelName":"team","unread":1,"unreadMentions":0,"firstUnreadSeq":7,"lastSeq":8}]}}}`))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--since", "old-token", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Vars["since"] != "old-token" || calls[1].Vars["page"] != "page-two" {
		t.Fatalf("paged call sequence = %+v", calls)
	}
	if _, ok := calls[0].Vars["page"]; ok {
		t.Errorf("first pull sent page: %+v", calls[0].Vars)
	}
	if _, ok := calls[1].Vars["since"]; ok {
		t.Errorf("continuation resent since: %+v", calls[1].Vars)
	}
	var dto struct {
		Token   string `json:"token"`
		Workers []struct {
			Name string `json:"name"`
		} `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Token != "final-token" || len(dto.Workers) != 2 || dto.Workers[0].Name != "Ada" || dto.Workers[1].Name != "Jonas" {
		t.Fatalf("incomplete or reordered report: %+v", dto)
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
		"TeamAttentionPage": `{"data":{"teamAttentionPage":{"scanComplete":true,"nextPage":null,"adoptableSince":"tok-3","items":[]}}}`,
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

// Attention token/page/proof refusals exit per the documented contract.
// FEATURE_NOT_AVAILABLE remains mapped for an older server during rollout.
func TestTeamAttentionRefusalsMapToExitCodes(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"FEATURE_NOT_AVAILABLE", exitcode.Forbidden},
		{"INVALID_ATTENTION_TOKEN", exitcode.Usage},
		{"INVALID_ATTENTION_PAGE", exitcode.Usage},
		{"SWITCHOVER_CONFIRMATION_REQUIRED", exitcode.Usage},
		{"ATTENTION_TOKEN_STALE", exitcode.Conflict},
		{"TEAM_ATTENTION_STALE_PAGE", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			teamGitDir(t)
			srv, _ := attnServer(t, map[string]string{"TeamAttentionPage": gqlErrorJSON(tc.code)})
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
	dir := teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttentionPage": attentionJSON})
	bound := strings.Replace(bindingFixture, `"startedAt"`, `"server":"`+srv.URL+`","startedAt"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bound), 0o600); err != nil {
		t.Fatal(err)
	}
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

const switchoverPreviewJSON = `{"data":{"teamAttentionPreviewPage":{"scanComplete":true,"nextPage":null,"proof":"prf-1","expiresAt":"2026-09-25T20:40:00Z","items":[
	{"worker":"wkr1","workerName":"Iris","workerUrn":null,"channel":"ch1","channelName":"team","firstUnreadSeq":1,"lastSeq":1878,"unread":1878,"unreadMentions":12}]}}}`

const legacySwitchoverPreviewJSON = `{"data":{"teamAttentionSwitchoverPreview":{"proof":"old-proof","expiresAt":"2026-09-25T20:40:00Z","workers":[
	{"worker":"wkr1","name":"Iris","urn":null,"channels":[{"channel":"ch1","name":"team","fromSeq":0,"throughSeq":1878,"unread":1878,"unreadMentions":12}]}]}}}`

func TestTeamAttentionSwitchoverPreviewFallsBackOnlyWhenPagedFieldIsMissing(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{
		"TeamAttentionPreviewPage":       missingFieldJSON("teamAttentionPreviewPage"),
		"TeamAttentionSwitchoverPreview": legacySwitchoverPreviewJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := opsOf(*calls); !reflect.DeepEqual(got, []string{"TeamAttentionPreviewPage", "TeamAttentionSwitchoverPreview"}) {
		t.Fatalf("fallback calls = %v", got)
	}
	if !strings.Contains(out.String(), `"proof": "old-proof"`) || !strings.Contains(out.String(), `"fromSeq": 0`) {
		t.Fatalf("legacy preview was lost: %s", out.String())
	}
}

func TestTeamAttentionLegacyPreviewShowsExactCursor(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{
		"TeamAttentionPreviewPage":       missingFieldJSON("teamAttentionPreviewPage"),
		"TeamAttentionSwitchoverPreview": legacySwitchoverPreviewJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CURSOR") || !strings.Contains(out.String(), "#0") || !strings.Contains(out.String(), "#1878") {
		t.Fatalf("legacy cursor not displayed: %s", out.String())
	}
}

func TestTeamAttentionSwitchoverPreviewIsReadOnlyAndPrintsTheApplyCommand(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttentionPreviewPage": switchoverPreviewJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "TeamAttentionPreviewPage" {
		t.Fatalf("preview is ONE read, got %+v", *calls)
	}
	for _, want := range []string{"#1", "#1878", "Nothing has been written", "switchover apply --app hrn:app:acme.com:eng-team --server " + srv.URL + " --proof prf-1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTeamAttentionPagedPreviewDoesNotInventAnExactCursor(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{"TeamAttentionPreviewPage": switchoverPreviewJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, `"fromSeq"`) || !strings.Contains(got, `"firstUnreadSeq": 1`) || !strings.Contains(got, `"proof": "prf-1"`) {
		t.Fatalf("paged preview should report its actual fields: %s", got)
	}
}

func TestTeamAttentionSwitchoverPreviewQuotesOpaqueProof(t *testing.T) {
	teamGitDir(t)
	resp := strings.Replace(switchoverPreviewJSON, "prf-1", "p' ; echo wrong", 1)
	srv, _ := attnServer(t, map[string]string{"TeamAttentionPreviewPage": resp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `--proof 'p'\'' ; echo wrong'`) {
		t.Errorf("proof must be one quoted shell argument:\n%s", out.String())
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
	root.SetArgs([]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--proof", " prf-1 ", "--yes", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["proof"] != " prf-1 " {
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

// ── team chat read marks the bound worker read AFTER delivery ───────────────
//
// PR #732, @codex P1: the read itself carries NO session, so the server never
// marks a page the command has not shown yet. After a successful render, a read
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
			// The read must NOT carry the session: a header-bound read lets the
			// server mark a page before this command has shown it.
			if c.Session != "" {
				t.Errorf("the read itself must not carry X-Hadron-Session, got %q", c.Session)
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
		if c.Op == "MarkOwnTeamChatRead" || c.Session != "" {
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

// A poll whose output cannot be written must FAIL (PR #732, @codex P2): the
// caller then keeps its previous token instead of believing it holds a new
// one it never received.
func TestTeamAttentionFailsWhenTheTokenCannotBeWritten(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{"TeamAttentionPage": attentionJSON})
	f, _ := testFactory(t)
	f.IOStreams.Out = failOnWrite{substr: "token:"} // everything lands except the token line
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a poll whose token line was lost must not exit 0")
	}
}

// failOnWrite rejects exactly the write containing substr, so a test can lose
// ONE specific line — a write budget cannot, since a table's rows arrive in an
// unknown number of writes.
type failOnWrite struct{ substr string }

func (w failOnWrite) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.substr) {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
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

// PR #732 round 2, @copilot: every human receipt that follows a completed
// action is CHECKED, so a lost receipt cannot exit 0.
func TestTeamAttentionReceiptsFailWhenTheyCannotBeWritten(t *testing.T) {
	for _, tc := range []struct {
		name, lose string
		responses  map[string]string
		args       []string
	}{
		{"switchover preview", "app:", map[string]string{"TeamAttentionPreviewPage": switchoverPreviewJSON},
			[]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team"}},
		{"switchover apply", "Switchover applied", map[string]string{
			"ConfirmTeamAttentionSwitchover": `{"data":{"confirmTeamAttentionSwitchover":{"applied":true,"workersAdvanced":1,"channelsAdvanced":1}}}`},
			[]string{"team", "attention", "switchover", "apply", "--app", "acme.com:eng-team", "--proof", "p", "--yes"}},
		{"mark-read", "Marked read", map[string]string{"MarkOwnTeamChatRead": markReadJSON},
			[]string{"team", "chat", "mark-read", "--through", "1878", "--channel", "ch1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			srv, _ := attnServer(t, tc.responses)
			if tc.name == "mark-read" {
				setTeamBindingServer(t, srv.URL)
			}
			f, _ := testFactory(t)
			f.IOStreams.Out = failOnWrite{substr: tc.lose}
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", srv.URL))
			if err := root.Execute(); err == nil {
				t.Fatalf("a %s whose receipt was lost must not exit 0", tc.name)
			}
		})
	}
}

// PR #732 round 2, @codex/@copilot: a binding made against ANOTHER server
// must not supply the App to the attention commands — App ids are not unique
// across deployments. Refused before any request; an explicit --app still
// reaches this server.
func TestTeamAttentionRefusesABindingFromAnotherServer(t *testing.T) {
	for _, args := range [][]string{
		{"team", "attention"},
		{"team", "attention", "switchover", "preview"},
		{"team", "attention", "switchover", "apply", "--proof", "p", "--yes"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			dir := teamGitDir(t)
			other := strings.Replace(bindingFixture, `"startedAt"`, `"server":"https://elsewhere.example","startedAt"`, 1)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(other), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, calls := attnServer(t, map[string]string{
				"TeamAttentionPage":              attentionJSON,
				"TeamAttentionPreviewPage":       switchoverPreviewJSON,
				"ConfirmTeamAttentionSwitchover": `{"data":{"confirmTeamAttentionSwitchover":{"applied":true,"workersAdvanced":1,"channelsAdvanced":1}}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, args...), "--server", srv.URL))
			if got := exitOf(root.Execute()); got != exitcode.Usage {
				t.Errorf("binding-derived App on another server: exit = %d, want %d", got, exitcode.Usage)
			}
			if len(*calls) != 0 {
				t.Errorf("refused before any request, got %v", opsOf(*calls))
			}
			// Naming the App explicitly is a deliberate choice, and allowed.
			f, _ = testFactory(t)
			root = NewRootCmd(f)
			root.SetArgs(append(append([]string{}, args...), "--app", "acme.com:eng-team", "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Errorf("an explicit --app must still work: %v", err)
			}
		})
	}
}

func TestTeamAttentionRefusesABindingWithoutServer(t *testing.T) {
	writeTeamBinding(t) // The old binding format has no server field.
	srv, calls := attnServer(t, map[string]string{"TeamAttentionPreviewPage": switchoverPreviewJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("unknown binding server: exit = %d, want %d", got, exitcode.Usage)
	}
	if len(*calls) != 0 {
		t.Errorf("unknown binding server must refuse before a request: %+v", *calls)
	}
}

func TestTeamAttentionRefusesConfiguredAppAfterServerOverride(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{"TeamAttentionPreviewPage": switchoverPreviewJSON})
	f, _ := testFactory(t)
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("server = \"https://elsewhere.example\"\napp = \"hrn:app:acme.com:eng-team\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("configured App on another server: exit = %d, want %d", got, exitcode.Usage)
	}
	if len(*calls) != 0 {
		t.Errorf("configured App on another server must refuse before a request: %+v", *calls)
	}
	// A later `config set server` makes the old App look local if we compare
	// only today's configured server with today's selected server. The App has
	// no stored deployment provenance, so that equality proves nothing.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("server = \""+srv.URL+"\"\napp = \"hrn:app:acme.com:eng-team\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--server", srv.URL})
	if got := exitOf(root.Execute()); got != exitcode.Usage {
		t.Errorf("unproven configured App on selected server: exit = %d, want %d", got, exitcode.Usage)
	}
	if len(*calls) != 0 {
		t.Errorf("unproven configured App must refuse before a request: %+v", *calls)
	}
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Errorf("explicit App must work on another server: %v", err)
	}
}

func TestTeamAttentionAcceptsConfiguredURNForBoundApp(t *testing.T) {
	writeTeamBinding(t)
	f, _ := testFactory(t)
	srv, calls := attnServer(t, map[string]string{
		"TeamAppIdentity":          teamAppIdentityJSON,
		"TeamAttentionPreviewPage": switchoverPreviewJSON,
	})
	setTeamBindingServer(t, srv.URL)
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("server = \""+srv.URL+"\"\napp = \"hrn:app:acme.com:eng-team\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("configured URN resolves to the bound App: %v", err)
	}
	if got := opsOf(*calls); len(got) != 2 || got[0] != "TeamAppIdentity" || got[1] != "TeamAttentionPreviewPage" {
		t.Errorf("identity must be checked before the preview: %v", got)
	}
}

func runConfiguredBoundAttentionPreview(t *testing.T, serverURL string) error {
	t.Helper()
	writeTeamBinding(t)
	f, _ := testFactory(t)
	setTeamBindingServer(t, serverURL)
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("server = \""+serverURL+"\"\napp = \"hrn:app:acme.com:eng-team\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "switchover", "preview", "--server", serverURL})
	return root.Execute()
}

func TestTeamAttentionPreservesConfiguredAppIdentityLookupErrors(t *testing.T) {
	t.Run("authentication", func(t *testing.T) {
		srv, calls := attnServer(t, map[string]string{"TeamAppIdentity": gqlErrorJSON("UNAUTHENTICATED")})
		if err := runConfiguredBoundAttentionPreview(t, srv.URL); exitOf(err) != exitcode.AuthRequired {
			t.Errorf("identity auth failure: exit = %d, want %d (%v)", exitOf(err), exitcode.AuthRequired, err)
		}
		if got := opsOf(*calls); len(got) != 1 || got[0] != "TeamAppIdentity" {
			t.Errorf("auth failure must stop before preview: %v", got)
		}
	})
	t.Run("transport", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()
		if err := runConfiguredBoundAttentionPreview(t, url); exitOf(err) != exitcode.Unavailable {
			t.Errorf("identity transport failure: exit = %d, want %d (%v)", exitOf(err), exitcode.Unavailable, err)
		}
	})
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
