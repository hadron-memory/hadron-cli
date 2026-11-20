package cmd

import (
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
		"MySecretReadClients":       `{"data":{"mySecretReadClients":[]}}`,
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
		if c.Op == "MySecretReadClients" && c.Session != "" {
			t.Errorf("ls without --as-worker must send no session, got %q", c.Session)
		}
	}
	*calls = nil
	if _, _, err := runSecretClient2631(t, srv.URL, "ls", "--as-worker"); err != nil {
		t.Fatal(err)
	}
	var sent bool
	for _, c := range *calls {
		if c.Op == "MySecretReadClients" {
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
	srv2, _ := attnServerHook(t, issueResponses2631("OPEN"), func(op string) {
		if op == "TeamGrantSigningRequest" {
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

// Acceptance 7: ls prints [] for none, the seven fields, never null; paging
// flags pass through; --all walks until an empty page.
func TestSecretClientLs2631(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{"MySecretReadClients": `{"data":{"mySecretReadClients":[]}}`})
	out, _, err := runSecretClient2631(t, srv.URL, "ls", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("an empty answer must print []: err=%v out=%q", err, out)
	}
	row := `{"clientId":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":false,"endReason":null,"createdAt":"2026-11-20T00:00:00Z"}`
	srv2, calls := attnServer(t, map[string]string{"MySecretReadClients": `{"data":{"mySecretReadClients":[` + row + `]}}`})
	out, _, err = runSecretClient2631(t, srv2.URL, "ls", "--json", "--limit", "5", "--offset", "2")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 1 || len(rows[0]) != 7 || rows[0]["endReason"] != nil {
		t.Fatalf("seven fields, endReason null: %v\n%s", e, out)
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
				rows = append(rows, fmt.Sprintf(`{"clientId":"c%d","grantId":"g","secretId":"s","secretName":"n","selfIssued":true,"endReason":null,"createdAt":"t"}`, i))
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
	row := `{"clientId":"c1","grantId":"g1","secretId":"s1","secretName":"to\u001b[31mk\nx","selfIssued":true,"endReason":"PROTECTED","createdAt":"t"}`
	srv, _ := attnServer(t, map[string]string{"MySecretReadClients": `{"data":{"mySecretReadClients":[` + row + `]}}`})
	out, _, err := runSecretClient2631(t, srv.URL, "ls")
	if err != nil || strings.ContainsAny(out, "\x1b") || strings.Count(out, "\n") != 2 {
		t.Fatalf("header plus one control-free row expected: err=%v out=%q", err, out)
	}
}
