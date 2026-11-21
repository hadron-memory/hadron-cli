package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2631 slice 1: `secret client issue` and `secret client ls`. Fake HTTP only.

const (
	clientIssued2631 = `{"data":{"issueSecretReadClient":{"clientId":"cl-2631","grantId":"gr-2631","secretName":"deploy-token","selfIssued":true}}}`
	ceremonyURL2631  = "https://portal.example/grant-signing/sr-2631"
)

func signingRequest2631(state, url string) string {
	return `{"id":"sr-2631","grantId":"gr-2631","version":1,"termsHash":"h","payloadVersion":"v1","state":"` + state +
		`","requestedBy":{"userId":"u1","appId":null,"workerId":null,"sessionId":null},"createdAt":"2026-11-20T00:00:00Z",` +
		`"expiresAt":"2026-11-21T00:00:00Z","endedAt":null,"lastRefusedAt":null,"ceremonyUrl":` + mustJSON2631(url) + `}`
}

func mustJSON2631(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func issueResponses2631(state string) map[string]string {
	return map[string]string{
		"IssueSecretReadClient":     clientIssued2631,
		"TeamRequestGrantSignature": `{"data":{"requestGrantSignature":` + signingRequest2631("OPEN", ceremonyURL2631) + `}}`,
		"TeamGrantSigningRequest":   `{"data":{"grantSigningRequest":` + signingRequest2631(state, ceremonyURL2631) + `}}`,
	}
}

func runSecretClient2631(t *testing.T, srvURL string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"secret", "client"}, args...), "--server", srvURL))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func opsOf2631(calls *[]attnCall) []string {
	var ops []string
	for _, c := range *calls {
		ops = append(ops, c.Op)
	}
	return ops
}

// Acceptance 1: one issue, then one signing request on the returned grant id,
// and the ceremony URL byte for byte. Red on main: the command does not exist.
func TestSecretClientIssueSendsOnceAndPrintsTheURL2631(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "hrn:secret:acme.com:ops:deploy-token", "--reason", "deploy window")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := opsOf2631(calls); len(got) != 2 || got[0] != "IssueSecretReadClient" || got[1] != "TeamRequestGrantSignature" {
		t.Fatalf("want one issue then one signing request, got %v", got)
	}
	if v := (*calls)[0].Vars["input"].(map[string]any); v["secretRef"] != "hrn:secret:acme.com:ops:deploy-token" || v["reason"] != "deploy window" || v["holderKind"] != "USER" {
		t.Errorf("issue input: %v", v)
	}
	if (*calls)[1].Vars["grantRef"] != "gr-2631" {
		t.Errorf("the signing request must ask for the returned grant id: %v", (*calls)[1].Vars)
	}
	for _, want := range []string{"clientId: cl-2631", "secret: deploy-token", "selfIssued: true", "grantId: gr-2631", "signingRequest: sr-2631", "ceremonyUrl: " + ceremonyURL2631 + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "pending") {
		t.Errorf("stderr must say the grant is pending: %q", errOut)
	}
}

// Optional flags are omitted when unset and sent when set; --worker makes the
// holder a worker; the client id never reaches the second request.
func TestSecretClientIssueInputPresence2631(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	if _, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r"); err != nil {
		t.Fatal(err)
	}
	var in map[string]any
	if err := json.Unmarshal((*calls)[0].RawVars, &struct {
		Input *map[string]any `json:"input"`
	}{&in}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"holderRef", "startsAt", "expiresAt", "maxReads"} {
		if _, present := in[k]; present {
			t.Errorf("an unset flag must be omitted, but %s was sent: %v", k, in)
		}
	}
	srv2, calls2 := attnServer(t, issueResponses2631("OPEN"))
	if _, _, err := runSecretClient2631(t, srv2.URL, "issue", "s1", "--reason", "r", "--worker", "w1", "--max-reads", "1",
		"--starts-at", "2026-12-01T00:00:00Z", "--expires-at", "2026-12-02T00:00:00Z", "--auto-delete"); err != nil {
		t.Fatal(err)
	}
	v := (*calls2)[0].Vars["input"].(map[string]any)
	if v["holderKind"] != "WORKER" || v["holderRef"] != "w1" || v["maxReads"] != float64(1) ||
		v["startsAt"] != "2026-12-01T00:00:00Z" || v["expiresAt"] != "2026-12-02T00:00:00Z" || v["autoDelete"] != true {
		t.Errorf("set flags must be sent as given: %v", v)
	}
	if strings.Contains(string((*calls2)[1].RawVars), "cl-2631") {
		t.Errorf("the client id must not be sent to the signing request: %s", (*calls2)[1].RawVars)
	}
}

// Acceptance 2: with the worktree bound to a worker, issue sends no session
// header on either call; ls sends one only with --as-worker.
func TestSecretClientSendsASessionOnlyWithAsWorker2631(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{
		"IssueSecretReadClient":     clientIssued2631,
		"TeamRequestGrantSignature": `{"data":{"requestGrantSignature":` + signingRequest2631("OPEN", ceremonyURL2631) + `}}`,
		listOp2758:                  `{"data":{"mySecretReadClients":[]}}`,
		"TeamAppIdentity":           teamAppIdentityJSON,
	})
	setTeamBindingServer(t, srv.URL)
	if _, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r"); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Errorf("%s: issue is a person's act and must not send a worker session, got %q", c.Op, c.Session)
		}
	}
	*calls = nil
	if _, _, err := runSecretClient2631(t, srv.URL, "ls"); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == listOp2758 && c.Session != "" {
			t.Errorf("ls without --as-worker must send no session, got %q", c.Session)
		}
	}
	*calls = nil
	if _, _, err := runSecretClient2631(t, srv.URL, "ls", "--as-worker"); err != nil {
		t.Fatal(err)
	}
	var sent bool
	for _, c := range *calls {
		if c.Op == listOp2758 {
			sent = c.Session != ""
		}
	}
	if !sent {
		t.Errorf("ls --as-worker must send the bound session: %+v", *calls)
	}
}

// Acceptance 3: --wait polls until the state is not OPEN. COMPLETED exits 0; the
// ended states exit 5 with the record still printed; neither withdraws.
func TestSecretClientIssueWaitEndStates2631(t *testing.T) {
	for state, want := range map[string]int{"COMPLETED": 0, "DECLINED": 5, "EXPIRED": 5, "STALE": 5, "WITHDRAWN": 5} {
		t.Run(state, func(t *testing.T) {
			srv, calls := attnServer(t, issueResponses2631(state))
			out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--wait", "--json")
			if exitCodeFor(err) != want {
				t.Fatalf("exit=%d want=%d err=%v", exitCodeFor(err), want, err)
			}
			var d struct {
				SigningRequest struct{ State, CeremonyURL string } `json:"signingRequest"`
			}
			if e := json.Unmarshal([]byte(out), &d); e != nil || d.SigningRequest.State != state || d.SigningRequest.CeremonyURL != ceremonyURL2631 {
				t.Fatalf("the record must still be printed with the last state: %v\n%s", e, out)
			}
			if want == 5 && !strings.Contains(err.Error(), state) {
				t.Errorf("the ended state must be named: %v", err)
			}
			if !strings.Contains(errOut, ceremonyURL2631) {
				t.Errorf("--wait must put the URL on stderr first so it can be passed on: %q", errOut)
			}
			for _, op := range opsOf2631(calls) {
				if strings.Contains(op, "Withdraw") {
					t.Errorf("wait must never withdraw: %v", opsOf2631(calls))
				}
			}
		})
	}
}

// A timeout leaves the request open, exits 13, withdraws nothing, and a hung
// poll request is cut by the wait deadline (acceptance 3 and 4).
func TestSecretClientIssueWaitTimeout2631(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	_, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--wait", "--wait-timeout", "50ms", "--json")
	if exitCodeFor(err) != exitcode.WaitTimeout || !strings.Contains(err.Error(), "sr-2631") || !strings.Contains(err.Error(), "signing-request get") {
		t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
	}
	for _, op := range opsOf2631(calls) {
		if strings.Contains(op, "Withdraw") {
			t.Fatalf("a timeout must not withdraw: %v", opsOf2631(calls))
		}
	}

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv2, _ := attnServerHook(t, issueResponses2631("OPEN"), func(op string) {
		if op == "TeamGrantSigningRequest" {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
	})
	// Registered after the server so it runs BEFORE the server's Close, which
	// would otherwise wait forever for the parked handler.
	t.Cleanup(func() { close(release) })
	start := time.Now()
	_, _, err = runSecretClient2631(t, srv2.URL, "issue", "s1", "--reason", "r", "--wait", "--wait-timeout", "200ms")
	if exitCodeFor(err) != exitcode.WaitTimeout || time.Since(start) > 5*time.Second {
		t.Fatalf("a hung poll must be cut by the deadline: exit=%d after %s err=%v", exitCodeFor(err), time.Since(start), err)
	}
	select {
	case <-entered:
	default:
		t.Fatal("the poll never reached the server, so the hung request was not what the deadline cut")
	}
}

// Acceptance 5: refusals map to their exit codes; a lost answer is sent once,
// exits 7 and names `client ls`.
func TestSecretClientIssueRefusalsAndLostAnswer2631(t *testing.T) {
	for code, want := range map[string]int{
		"FORBIDDEN": 8, "GRANT_HOLDER_NOT_ALLOWED": 8, "SECRET_AUTO_DELETE_NOT_AVAILABLE": 2,
		"SECRET_AUTO_DELETE_NEEDS_LIMIT": 2, "SECRET_READ_CLIENT_SUPERSEDED": 5, "SECRET_READ_CLIENT_LIMIT": 5,
	} {
		t.Run(code, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"IssueSecretReadClient": gqlErrorJSON(code)})
			out, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--json")
			if exitCodeFor(err) != want || out != "" || len(*calls) != 1 {
				t.Fatalf("exit=%d want=%d out=%q calls=%v err=%v", exitCodeFor(err), want, out, opsOf2631(calls), err)
			}
		})
	}
	var n int
	var mu sync.Mutex
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(502)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer bad.Close()
	_, _, err := runSecretClient2631(t, bad.URL, "issue", "s1", "--reason", "r")
	mu.Lock()
	defer mu.Unlock()
	if exitCodeFor(err) != exitcode.Unavailable || n != 1 || !strings.Contains(err.Error(), "secret client list") {
		t.Fatalf("a lost answer must exit 7 naming `client list` and never be resent: exit=%d sends=%d err=%v", exitCodeFor(err), n, err)
	}
}

// Acceptance 6 and 9: a server that does not offer signing exits 2 naming the
// grant that was created; the issue is still printed; no error line carries the
// client id.
func TestSecretClientIssueWhereSigningIsOff2631(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{
		"IssueSecretReadClient":     clientIssued2631,
		"TeamRequestGrantSignature": `{"errors":[{"message":"Cannot query field \"requestGrantSignature\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--json")
	if exitCodeFor(err) != 2 || len(*calls) != 2 {
		t.Fatalf("exit=%d calls=%v err=%v", exitCodeFor(err), opsOf2631(calls), err)
	}
	if !strings.Contains(err.Error(), "does not offer") || !strings.Contains(err.Error(), "gr-2631") {
		t.Errorf("the error must say signing is not offered and name the created grant: %v", err)
	}
	if strings.Contains(err.Error(), "cl-2631") || strings.Contains(errOut, "cl-2631") {
		t.Errorf("the client id must appear in issue and ls output only, never an error line: %v / %q", err, errOut)
	}
	var d struct {
		ClientID       string `json:"clientId"`
		SigningRequest any    `json:"signingRequest"`
	}
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.ClientID != "cl-2631" || d.SigningRequest != nil {
		t.Errorf("the issue must still print with signingRequest null: %v\n%s", e, out)
	}
}

// A ceremony URL that could act on a terminal is refused as text (exit 1) and
// carried exactly by --json; it is never rewritten.
func TestSecretClientIssueUnsafeURL2631(t *testing.T) {
	const evil = "https://portal.example/x\u202Eevil"
	resp := issueResponses2631("OPEN")
	resp["TeamRequestGrantSignature"] = `{"data":{"requestGrantSignature":` + signingRequest2631("OPEN", evil) + `}}`
	srv, _ := attnServer(t, resp)
	out, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r")
	if exitCodeFor(err) != 1 || strings.Contains(out, "\u202E") {
		t.Fatalf("text must refuse an unsafe URL: exit=%d out=%q err=%v", exitCodeFor(err), out, err)
	}
	// The client and grant already exist: the safe fields are still printed, so
	// the caller keeps the ids needed to recover (only the URL is withheld).
	for _, want := range []string{"clientId: cl-2631", "grantId: gr-2631", "signingRequest: sr-2631"} {
		if !strings.Contains(out, want) {
			t.Errorf("an unsafe URL must not hide the ids: stdout lacks %q:\n%s", want, out)
		}
	}
	srv2, _ := attnServer(t, resp)
	out, _, err = runSecretClient2631(t, srv2.URL, "issue", "s1", "--reason", "r", "--json")
	var d struct {
		SigningRequest struct{ CeremonyURL string } `json:"signingRequest"`
	}
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.SigningRequest.CeremonyURL != evil {
		t.Fatalf("--json must carry the URL exactly: err=%v out=%s", err, out)
	}
}

// Local validation sends nothing.
func TestSecretClientIssueLocalRefusalsSendNothing2631(t *testing.T) {
	for name, args := range map[string][]string{
		"no reason":      {"issue", "s1"},
		"blank reason":   {"issue", "s1", "--reason", "  "},
		"blank secret":   {"issue", " ", "--reason", "r"},
		"blank worker":   {"issue", "s1", "--reason", "r", "--worker", " "},
		"zero timeout":   {"issue", "s1", "--reason", "r", "--wait-timeout", "0s"},
		"all and offset": {"ls", "--all", "--offset", "1"},
		"negative limit": {"ls", "--limit", "-1"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{})
			_, _, err := runSecretClient2631(t, srv.URL, args...)
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("exit=%d calls=%v err=%v", exitCodeFor(err), opsOf2631(calls), err)
			}
		})
	}
}

// Acceptance 7: ls prints [] for none, the nine fields (revealUrl added by cli#2758, null
// here), never a null list; paging
// flags pass through; --all walks until an empty page.
func TestSecretClientLs2631(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{listOp2758: `{"data":{"mySecretReadClients":[]}}`})
	out, _, err := runSecretClient2631(t, srv.URL, "ls", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("an empty answer must print []: err=%v out=%q", err, out)
	}
	row := `{"clientId":"c1","clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":false,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":false}`
	srv2, calls := attnServer(t, map[string]string{listOp2758: `{"data":{"mySecretReadClients":[` + row + `]}}`})
	out, _, err = runSecretClient2631(t, srv2.URL, "ls", "--json", "--limit", "5", "--offset", "2")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 1 || len(rows[0]) != 10 || rows[0]["endReason"] != nil || rows[0]["clientPrefix"] == nil || rows[0]["revealUrl"] != nil || rows[0]["destroyOnEnd"] != false {
		t.Fatalf("ten fields (clientPrefix added by the server, revealUrl by cli#2758, destroyOnEnd by cli#2720), endReason and revealUrl null: %v\n%s", e, out)
	}
	if (*calls)[0].Vars["limit"] != float64(5) || (*calls)[0].Vars["offset"] != float64(2) {
		t.Errorf("paging must pass through: %v", (*calls)[0].Vars)
	}
	text, _, _ := runSecretClient2631(t, srv2.URL, "ls")
	for _, want := range []string{"CLIENT", "c1", "tok", "g1"} {
		if !strings.Contains(text, want) {
			t.Errorf("table lacks %q:\n%s", want, text)
		}
	}
}

func TestSecretClientLsAllWalksPages2631(t *testing.T) {
	var offsets []float64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		off, _ := body.Variables["offset"].(float64)
		mu.Lock()
		offsets = append(offsets, off)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if off == 0 {
			var rows []string
			for i := 0; i < 3; i++ {
				rows = append(rows, fmt.Sprintf(`{"clientId":"c%d","grantId":"g","secretId":"s","secretName":"n","selfIssued":true,"endReason":null,"createdAt":"t","destroyOnEnd":false}`, i))
			}
			_, _ = fmt.Fprintf(w, `{"data":{"mySecretReadClients":[%s]}}`, strings.Join(rows, ","))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"mySecretReadClients":[]}}`))
	}))
	defer srv.Close()
	out, _, err := runSecretClient2631(t, srv.URL, "ls", "--all", "--json")
	var rows []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 3 {
		t.Fatalf("err=%v out=%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 3 {
		t.Errorf("--all must walk by what it received until an empty page: %v", offsets)
	}
}

// Text output is neutralised: a control character in a server-authored secret
// name must not reach the terminal.
func TestSecretClientLsNeutralisesText2631(t *testing.T) {
	row := `{"clientId":"c1","grantId":"g1","secretId":"s1","secretName":"to\u001b[31mk\nx","selfIssued":true,"endReason":"PROTECTED","createdAt":"t","destroyOnEnd":false}`
	srv, _ := attnServer(t, map[string]string{listOp2758: `{"data":{"mySecretReadClients":[` + row + `]}}`})
	out, _, err := runSecretClient2631(t, srv.URL, "ls")
	if err != nil || strings.ContainsAny(out, "\x1b") || strings.Count(out, "\n") != 2 {
		t.Fatalf("header plus one control-free row expected: err=%v out=%q", err, out)
	}
}

// Dara's P2: --wait must not poll when the signer could not be shown the URL.
// Both modes print the issue (text refuses the URL; --json carries it) and send
// no TeamGrantSigningRequest at all.
func TestSecretClientIssueWaitDoesNotPollWithAnUnshowableURL2631(t *testing.T) {
	const evil = "https://portal.example/x\u202Eevil"
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			resp := issueResponses2631("OPEN")
			resp["TeamRequestGrantSignature"] = `{"data":{"requestGrantSignature":` + signingRequest2631("OPEN", evil) + `}}`
			srv, calls := attnServer(t, resp)
			args := []string{"issue", "s1", "--reason", "r", "--wait", "--wait-timeout", "30s"}
			if asJSON {
				args = append(args, "--json")
			}
			start := time.Now()
			out, errOut, err := runSecretClient2631(t, srv.URL, args...)
			if exitCodeFor(err) != 1 || time.Since(start) > 5*time.Second {
				t.Fatalf("must stop at once with exit 1: exit=%d after %s err=%v", exitCodeFor(err), time.Since(start), err)
			}
			for _, op := range opsOf2631(calls) {
				if op == "TeamGrantSigningRequest" {
					t.Fatalf("no poll may be sent when the URL cannot be shown: %v", opsOf2631(calls))
				}
			}
			if strings.Contains(errOut, "\u202E") {
				t.Errorf("the unsafe URL must not reach stderr: %q", errOut)
			}
			if asJSON {
				var d struct {
					SigningRequest struct{ CeremonyURL string } `json:"signingRequest"`
				}
				if json.Unmarshal([]byte(out), &d) != nil || d.SigningRequest.CeremonyURL != evil {
					t.Errorf("--json must carry the URL exactly, promptly: %s", out)
				}
			} else {
				if strings.Contains(out, "\u202E") {
					t.Errorf("text must not print the unsafe URL: %q", out)
				}
				if !strings.Contains(out, "clientId: cl-2631") || !strings.Contains(out, "grantId: gr-2631") {
					t.Errorf("text must still print the ids: %q", out)
				}
			}
		})
	}
}

// Dara's P2: cancelling the parent context while a poll is in flight is a
// cancellation (exit 6, exitcode.Cancelled), not a signing timeout, and says so.
func TestSecretClientIssueWaitCancellationIsNotATimeout2631(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	srv, _ := attnServerHook(t, issueResponses2631("OPEN"), func(op string) {
		if op == "TeamGrantSigningRequest" {
			cancel()
			<-release
		}
	})
	t.Cleanup(func() { close(release) })
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "client", "issue", "s1", "--reason", "r", "--wait", "--json", "--server", srv.URL})
	err := root.ExecuteContext(ctx)
	if exitCodeFor(err) != exitcode.Cancelled || err == nil || strings.Contains(err.Error(), "not signed within") || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("a cancelled parent must exit %d as cancelled, not as a timeout: exit=%d err=%v", exitcode.Cancelled, exitCodeFor(err), err)
	}
}

// Dara's P3: OPEN on one read, COMPLETED on the next: the poll must be
// scheduled again, and the final record carries the last state.
func TestSecretClientIssueWaitPollsAcrossOpenToCompleted2631(t *testing.T) {
	var polls int
	resp := issueResponses2631("OPEN")
	srv, calls := attnServerHook(t, resp, func(op string) {
		if op == "TeamGrantSigningRequest" {
			polls++
			state := "OPEN"
			if polls >= 2 {
				state = "COMPLETED"
			}
			setAttnResponse(resp, "TeamGrantSigningRequest", `{"data":{"grantSigningRequest":`+signingRequest2631(state, ceremonyURL2631)+`}}`)
		}
	})
	out, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	ops := opsOf2631(calls)
	if len(ops) != 4 || ops[2] != "TeamGrantSigningRequest" || ops[3] != "TeamGrantSigningRequest" {
		t.Fatalf("want issue, request, then two polls: %v", ops)
	}
	if !strings.Contains(out, `"state": "COMPLETED"`) && !strings.Contains(out, `"state":"COMPLETED"`) {
		t.Errorf("the final record must carry COMPLETED: %s", out)
	}
}

// Dara's P3: a lost answer to the signing request is an unknown outcome: sent
// once, exit 7, and the error says how to check or ask again.
func TestSecretClientIssueLostSigningAnswer2631(t *testing.T) {
	var sigCalls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName == "TeamRequestGrantSignature" {
			mu.Lock()
			sigCalls++
			mu.Unlock()
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(clientIssued2631))
	}))
	defer srv.Close()
	out, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--json")
	mu.Lock()
	defer mu.Unlock()
	if exitCodeFor(err) != exitcode.Unavailable || sigCalls != 1 {
		t.Fatalf("exit=%d sends=%d err=%v", exitCodeFor(err), sigCalls, err)
	}
	if !strings.Contains(err.Error(), "gr-2631") || !strings.Contains(err.Error(), "may exist") || !strings.Contains(err.Error(), "request-signature") {
		t.Errorf("the error must say a signing request may exist and how to ask again: %v", err)
	}
	if !strings.Contains(out, `"signingRequest"`) || !strings.Contains(out, "cl-2631") {
		t.Errorf("the issue must still print: %s", out)
	}
}

// Joost's F1: a signing failure whose server text or code echoes the client id
// must not carry it into the error (the real renderer's JSON error message
// included); the client record stays on stdout.
func TestSecretClientIssueSigningErrorDoesNotEchoTheClientID2631(t *testing.T) {
	for name, body := range map[string]string{
		"message echoes": `{"errors":[{"message":"signing refused for cl-2631","extensions":{"code":"SERVER_BUSY"}}]}`,
		"code echoes":    `{"errors":[{"message":"busy","extensions":{"code":"BUSY_cl-2631"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := attnServer(t, map[string]string{"IssueSecretReadClient": clientIssued2631, "TeamRequestGrantSignature": body})
			f, out := testFactory(t)
			var errOut strings.Builder
			f.IOStreams.ErrOut = &errOut
			f.JSON = true
			root := NewRootCmd(f)
			root.SetArgs([]string{"secret", "client", "issue", "s1", "--reason", "r", "--json", "--server", srv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected the signing refusal")
			}
			renderError(f, err)
			if strings.Contains(err.Error(), "cl-2631") || strings.Contains(errOut.String(), "cl-2631") {
				t.Errorf("the client id must not reach the error or the rendered envelope: %v / %q", err, errOut.String())
			}
			if !strings.Contains(out.String(), "cl-2631") {
				t.Errorf("the client record must stay on stdout: %q", out.String())
			}
		})
	}
}

// Joost's F2: a missing or null list, or a null row, is a malformed answer
// (exit 1), never a successful empty list; a genuine [] stays a success. Both the
// paged and the --all paths.
func TestSecretClientListMalformedAnswerIsAnError2631(t *testing.T) {
	for name, body := range map[string]string{
		"missing data": `{"data":{}}`,
		"null list":    `{"data":{"mySecretReadClients":null}}`,
		"null element": `{"data":{"mySecretReadClients":[null]}}`,
		"null data":    `{"data":null}`,
	} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/all=%t", name, all), func(t *testing.T) {
				srv, _ := attnServer(t, map[string]string{listOp2758: body})
				args := []string{"list", "--json"}
				if all {
					args = append(args, "--all")
				}
				out, _, err := runSecretClient2631(t, srv.URL, args...)
				if err == nil || exitCodeFor(err) == 0 || strings.TrimSpace(out) == "[]" {
					t.Fatalf("a malformed answer must be an error, not []: exit=%d out=%q err=%v", exitCodeFor(err), out, err)
				}
			})
		}
	}
}

// George's P2: if the URL cannot be written to stderr under --wait, nothing is
// polled: the issue goes to stdout and the error names the signing request.
type failingWriter2631 struct{}

func (failingWriter2631) Write([]byte) (int, error) { return 0, fmt.Errorf("stderr closed") }

func TestSecretClientIssueWaitStderrWriteFailureStopsBeforeThePoll2631(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	f, out := testFactory(t)
	f.IOStreams.ErrOut = failingWriter2631{}
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "client", "issue", "s1", "--reason", "r", "--wait", "--wait-timeout", "30s", "--server", srv.URL})
	start := time.Now()
	err := root.Execute()
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("must stop at once with an error: after %s err=%v", time.Since(start), err)
	}
	for _, op := range opsOf2631(calls) {
		if op == "TeamGrantSigningRequest" {
			t.Fatalf("no poll may be sent when the link could not be delivered: %v", opsOf2631(calls))
		}
	}
	if !strings.Contains(err.Error(), "sr-2631") || !strings.Contains(out.String(), "clientId: cl-2631") {
		t.Errorf("the issue goes to stdout and the error names the request: out=%q err=%v", out.String(), err)
	}
}

// George's P3: an issue answer with an empty client or grant id is unusable: no
// signing request is asked for, and withoutClientID never replaces an empty id.
func TestSecretClientIssueEmptyIDsAreRefused2631(t *testing.T) {
	for name, body := range map[string]string{
		"empty grant":  `{"data":{"issueSecretReadClient":{"clientId":"cl-2631","grantId":"","secretName":"n","selfIssued":true}}}`,
		"empty client": `{"data":{"issueSecretReadClient":{"clientId":"","grantId":"gr-2631","secretName":"n","selfIssued":true}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"IssueSecretReadClient": body})
			_, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r")
			if exitCodeFor(err) != 1 || len(*calls) != 1 || strings.Contains(err.Error(), "[client id]") {
				t.Fatalf("exit=%d calls=%v err=%v", exitCodeFor(err), opsOf2631(calls), err)
			}
		})
	}
}

// George's P3: exactly 1000 non-empty pages followed by the empty terminator is
// success, not the page-cap error.
func TestSecretClientLsAllAtThePageCap2631(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		off, _ := body.Variables["offset"].(float64)
		w.Header().Set("Content-Type", "application/json")
		if off < 1000 {
			_, _ = fmt.Fprintf(w, `{"data":{"mySecretReadClients":[{"clientId":"c%d","grantId":"g","secretId":"s","secretName":"n","selfIssued":true,"endReason":null,"createdAt":"t","destroyOnEnd":false}]}}`, int(off))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"mySecretReadClients":[]}}`))
	}))
	defer srv.Close()
	out, _, err := runSecretClient2631(t, srv.URL, "list", "--all", "--json")
	var rows []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1000 {
		t.Fatalf("1000 pages then an empty terminator must succeed: err=%v rows=%d", err, len(rows))
	}
}

// Rook's P3: the poll path redacts the client id too (message and server code),
// while the record printed first keeps it.
func TestSecretClientIssueWaitPollErrorDoesNotEchoTheClientID2631(t *testing.T) {
	for name, body := range map[string]string{
		"message echoes": `{"errors":[{"message":"poll refused for cl-2631","extensions":{"code":"SERVER_BUSY"}}]}`,
		"code echoes":    `{"errors":[{"message":"busy","extensions":{"code":"BUSY_cl-2631"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := issueResponses2631("OPEN")
			resp["TeamGrantSigningRequest"] = body
			srv, _ := attnServer(t, resp)
			f, out := testFactory(t)
			var errOut strings.Builder
			f.IOStreams.ErrOut = &errOut
			f.JSON = true
			root := NewRootCmd(f)
			root.SetArgs([]string{"secret", "client", "issue", "s1", "--reason", "r", "--wait", "--json", "--server", srv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected the poll refusal")
			}
			renderError(f, err)
			if strings.Contains(err.Error(), "cl-2631") || strings.Contains(errOut.String(), "cl-2631") {
				t.Errorf("the client id must not reach a poll error or its envelope: %v / %q", err, errOut.String())
			}
			if !strings.Contains(out.String(), "cl-2631") {
				t.Errorf("the record must still print first: %q", out.String())
			}
		})
	}
}

// Baloo's P2: a signing request with an empty or missing id or ceremony URL is
// not a success. The issue is preserved on stdout (signingRequest null), nothing
// is polled, and the error (exit 1) says a request may exist and how to ask again.
func TestSecretClientIssueIncompleteSigningRequestIsNotSuccess2631(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"empty id": func(s string) string { return strings.Replace(s, `"id":"sr-2631"`, `"id":""`, 1) },
		"empty url": func(s string) string {
			return strings.Replace(s, `"ceremonyUrl":"`+ceremonyURL2631+`"`, `"ceremonyUrl":""`, 1)
		},
		"blank url": func(s string) string {
			return strings.Replace(s, `"ceremonyUrl":"`+ceremonyURL2631+`"`, `"ceremonyUrl":"  "`, 1)
		},
	} {
		for _, wait := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wait=%t", name, wait), func(t *testing.T) {
				resp := issueResponses2631("OPEN")
				resp["TeamRequestGrantSignature"] = `{"data":{"requestGrantSignature":` + mutate(signingRequest2631("OPEN", ceremonyURL2631)) + `}}`
				srv, calls := attnServer(t, resp)
				args := []string{"issue", "s1", "--reason", "r", "--json"}
				if wait {
					args = append(args, "--wait", "--wait-timeout", "30s")
				}
				start := time.Now()
				out, _, err := runSecretClient2631(t, srv.URL, args...)
				if exitCodeFor(err) != 1 || time.Since(start) > 5*time.Second {
					t.Fatalf("an incomplete signing request must be an error (exit 1), at once: exit=%d err=%v", exitCodeFor(err), err)
				}
				for _, op := range opsOf2631(calls) {
					if op == "TeamGrantSigningRequest" {
						t.Fatalf("nothing may be polled: %v", opsOf2631(calls))
					}
				}
				var d struct {
					ClientID       string `json:"clientId"`
					GrantID        string `json:"grantId"`
					SigningRequest any    `json:"signingRequest"`
				}
				if e := json.Unmarshal([]byte(out), &d); e != nil || d.ClientID != "cl-2631" || d.GrantID != "gr-2631" || d.SigningRequest != nil {
					t.Errorf("the issue must be preserved with signingRequest null: %v\n%s", e, out)
				}
				for _, want := range []string{"gr-2631", "may exist", "request-signature"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the error must give recovery guidance (%q): %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "cl-2631") {
					t.Errorf("the client id must not be in the error: %v", err)
				}
			})
		}
	}
}

// Joaquin's P3: a manager who is neither the issuer nor the holder gets only a
// prefix from the server: clientId is null, clientPrefix is distinct. The JSON
// carries both fields and the table shows the prefix with an ellipsis.
func TestSecretClientLsManagerRowHasANullIDAndAPrefix2631(t *testing.T) {
	row := `{"clientId":null,"clientPrefix":"cl-ab12","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":false,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":false}`
	srv, _ := attnServer(t, map[string]string{listOp2758: `{"data":{"mySecretReadClients":[` + row + `]}}`})
	out, _, err := runSecretClient2631(t, srv.URL, "list", "--json")
	var rows []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if v, ok := rows[0]["clientId"]; !ok || v != nil || rows[0]["clientPrefix"] != "cl-ab12" {
		t.Fatalf("clientId must be null and clientPrefix distinct: %v", rows[0])
	}
	text, _, err := runSecretClient2631(t, srv.URL, "list")
	if err != nil || !strings.Contains(text, "cl-ab12…") {
		t.Fatalf("the table shows the prefix with an ellipsis: err=%v\n%s", err, text)
	}
}
