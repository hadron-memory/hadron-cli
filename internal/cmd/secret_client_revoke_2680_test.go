package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2680: `secret client revoke <client-id>`. A read client is backed by one
// grant (cor:sec:060), so revoking it is revokeGrant(ref: <grantId>), once. Fake
// HTTP only.

func readClients2680(rows ...string) string {
	return `{"data":{"mySecretReadClients":[` + strings.Join(rows, ",") + `]}}`
}

func clientRow2680(client, grant string) string {
	return `{"clientId":"` + client + `","grantId":"` + grant + `","secretId":"s1","secretName":"deploy-token","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z"}`
}

func revoked2680(state string) string { return revokedFor2680("gr-c1", state) }

func revokedFor2680(grant, state string) string { return revokedAllows2680(grant, state, false) }

func revokedAllows2680(grant, state string, allows bool) string {
	return `{"data":{"revokeGrant":{"id":"` + grant + `","state":"` + state + `","revokedAt":"2026-11-21T02:00:00Z","allows":` + fmt.Sprint(allows) + `}}}`
}

func runRevoke2680(t *testing.T, srvURL string, args ...string) (string, error) {
	t.Helper()
	out, _, err := runSecretClient2631(t, srvURL, append([]string{"revoke"}, args...)...)
	return out, err
}

func revokes2680(calls *[]attnCall) int {
	n := 0
	for _, c := range *calls {
		if c.Op == "RevokeSecretReadClientGrant" {
			n++
		}
	}
	return n
}

// Acceptance 1: read the clients, send revokeGrant with THAT client's grant id
// exactly once, print the revoked state. No worker session, even when bound.
// Red on main: the command does not exist.
func TestSecretClientRevokeSendsOnceWithTheGrantID2680(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{
		"MySecretReadClients":         readClients2680(clientRow2680("c0", "gr-c0"), clientRow2680("c1", "gr-c1")),
		"RevokeSecretReadClientGrant": revoked2680("REVOKED"),
		"TeamAppIdentity":             teamAppIdentityJSON,
	})
	setTeamBindingServer(t, srv.URL)
	out, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := revokes2680(calls); n != 1 {
		t.Fatalf("revokeGrant must be sent exactly once, got %d (%v)", n, opsOf2631(calls))
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Errorf("%s: revoke acts as you and must not send a worker session, got %q", c.Op, c.Session)
		}
		if c.Op == "RevokeSecretReadClientGrant" {
			if c.Vars["ref"] != "gr-c1" {
				t.Errorf("revokeGrant must name the client's grant id (gr-c1), not the client id: %v", c.Vars)
			}
			if _, has := c.Vars["instruction"]; has {
				t.Errorf("no instruction is sent: %v", c.Vars)
			}
		}
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if d["clientId"] != "c1" || d["grantId"] != "gr-c1" || d["state"] != "REVOKED" || d["revokedAt"] != "2026-11-21T02:00:00Z" || d["allows"] != false {
		t.Errorf("DTO: %v", d)
	}
	text, _, err := runSecretClient2631(t, srv.URL, "revoke", "c1", "--yes")
	if err != nil || !strings.Contains(text, "is REVOKED") || !strings.Contains(text, "c1") {
		t.Errorf("text must name the client and the state: %v %q", err, text)
	}
}

// Acceptance 2: an unknown client (or someone else's, which never lists) is a
// not-found (exit 4) and sends no revokeGrant. A client on a later page is found.
func TestSecretClientRevokeUnknownClientSendsNothing2680(t *testing.T) {
	var mu sync.Mutex
	var ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		ops = append(ops, b.OperationName)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if off, _ := b.Variables["offset"].(float64); off == 0 {
			_, _ = w.Write([]byte(readClients2680(clientRow2680("c0", "gr-c0"))))
			return
		}
		_, _ = w.Write([]byte(readClients2680()))
	}))
	defer srv.Close()
	_, err := runRevoke2680(t, srv.URL, "nope", "--yes")
	mu.Lock()
	defer mu.Unlock()
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("exit %d, want 4: %v", exitCodeFor(err), err)
	}
	for _, op := range ops {
		if op == "RevokeSecretReadClientGrant" {
			t.Fatalf("no revokeGrant for an unknown client: %v", ops)
		}
	}
	if !strings.Contains(err.Error(), "secret client list") {
		t.Errorf("the error says where clients are listed: %v", err)
	}
}

func TestSecretClientRevokeFindsAClientOnALaterPage2680(t *testing.T) {
	var mu sync.Mutex
	var offsets []float64
	var revokes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch b.OperationName {
		case "MySecretReadClients":
			off, _ := b.Variables["offset"].(float64)
			offsets = append(offsets, off)
			switch off {
			case 0:
				rows := make([]string, 200)
				for i := range rows {
					rows[i] = clientRow2680(fmt.Sprintf("x%d", i), fmt.Sprintf("gx%d", i))
				}
				_, _ = w.Write([]byte(readClients2680(rows...)))
			case 200:
				_, _ = w.Write([]byte(readClients2680(clientRow2680("c-late", "gr-late"))))
			default:
				_, _ = w.Write([]byte(readClients2680()))
			}
		case "RevokeSecretReadClientGrant":
			revokes++
			if b.Variables["ref"] != "gr-late" {
				t.Errorf("ref %v", b.Variables["ref"])
			}
			_, _ = w.Write([]byte(revokedFor2680("gr-late", "REVOKED")))
		}
	}))
	defer srv.Close()
	if _, err := runRevoke2680(t, srv.URL, "c-late", "--yes"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if revokes != 1 || len(offsets) < 2 {
		t.Fatalf("revokes=%d offsets=%v", revokes, offsets)
	}
}

// Acceptance 3: a lost revokeGrant answer is not resent, exits 7, and says to
// check `secret client list` (and names the grant). Never called "failed".
func TestSecretClientRevokeLostAnswerIsNotResent2680(t *testing.T) {
	var mu sync.Mutex
	revokes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b struct {
			OperationName string `json:"operationName"`
		}
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		defer mu.Unlock()
		if b.OperationName == "RevokeSecretReadClientGrant" {
			revokes++
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(readClients2680(clientRow2680("c1", "gr-c1"))))
	}))
	defer srv.Close()
	_, err := runRevoke2680(t, srv.URL, "c1", "--yes")
	mu.Lock()
	defer mu.Unlock()
	if exitCodeFor(err) != exitcode.Unavailable || revokes != 1 {
		t.Fatalf("exit=%d revokes=%d err=%v", exitCodeFor(err), revokes, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "secret client list") || !strings.Contains(msg, "gr-c1") || !strings.Contains(strings.ToLower(msg), "unknown") {
		t.Errorf("a lost answer is an unknown outcome: name `secret client list` and the grant: %v", err)
	}
	if strings.Contains(strings.ToLower(msg), "failed") {
		t.Errorf("an unknown outcome is not 'failed': %v", err)
	}
	// cli#2847 (Jonas D5): this revoke ran as the person, so the recovery is the
	// person list, exactly as the instructed fixtures pin their `--as-worker`
	// form (2754). A mutant that hard-codes the instructed hint on the person
	// branch must go red here, not only survive the focused 2754 set.
	if !strings.Contains(msg, "`hadron secret client list --all`") || strings.Contains(msg, "--as-worker") {
		t.Errorf("a person's lost answer recommends the person recovery, never the worker flag: %v", err)
	}
}

// A state that is not an end (ACTIVE, PENDING) is NOT a confirmed revoke (exit 7);
// a grant that had already ended is returned as it is and is a success that says
// nothing changed (the first end stands); a null answer is not a success.
func TestSecretClientRevokeStatesAndMalformedAnswers2680(t *testing.T) {
	for _, tc := range []struct {
		name, resp string
		exit       int
		want       string
	}{
		{"active is not revoked", revoked2680("ACTIVE"), exitcode.Unavailable, "still ACTIVE"},
		{"pending is not revoked", revoked2680("PENDING"), exitcode.Unavailable, "still PENDING"},
		{"already expired stands", revoked2680("EXPIRED"), 0, "already ended"},
		{"already withdrawn stands", revoked2680("WITHDRAWN"), 0, "already ended"},
		{"null answer", `{"data":{"revokeGrant":null}}`, exitcode.Unavailable, "not confirmed"},
		{"missing answer", `{"data":{}}`, exitcode.Unavailable, "not confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")), "RevokeSecretReadClientGrant": tc.resp})
			out, err := runRevoke2680(t, srv.URL, "c1", "--yes")
			if exitCodeFor(err) != tc.exit || revokes2680(calls) != 1 {
				t.Fatalf("exit=%d want=%d sends=%d err=%v", exitCodeFor(err), tc.exit, revokes2680(calls), err)
			}
			got := out
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("want %q in %q", tc.want, got)
			}
		})
	}
}

// Refusals keep their exit class and send revokeGrant once. The server answers a
// grant that is not the caller's to revoke with GRANT_NOT_MANAGER (not allowed
// and not visible are one answer), which is a permission refusal (8).
func TestSecretClientRevokeRefusals2680(t *testing.T) {
	for code, want := range map[string]int{"GRANT_NOT_MANAGER": 8, "FORBIDDEN": 8, "UNAUTHENTICATED": 3} {
		t.Run(code, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")), "RevokeSecretReadClientGrant": gqlErrorJSON(code)})
			_, err := runRevoke2680(t, srv.URL, "c1", "--yes")
			if exitCodeFor(err) != want || revokes2680(calls) != 1 {
				t.Fatalf("exit=%d want=%d sends=%d err=%v", exitCodeFor(err), want, revokes2680(calls), err)
			}
		})
	}
}

// Revoke ends access and cannot be undone (a new client needs a new signature):
// off a terminal it needs --yes and sends nothing; on one it asks, naming the
// client and the secret, and "no" sends nothing.
func TestSecretClientRevokeConfirmation2680(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")), "RevokeSecretReadClientGrant": revoked2680("REVOKED")})
	_, err := runRevoke2680(t, srv.URL, "c1")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Fatalf("without --yes off a terminal: exit=%d requests=%v err=%v", exitCodeFor(err), opsOf2631(calls), err)
	}
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "client", "revoke", "c1", "--server", srv.URL})
	err = root.Execute()
	if err == nil || revokes2680(calls) != 0 {
		t.Fatalf("an answer of no must send no revokeGrant: %v %v", err, opsOf2631(calls))
	}
	if p := errOut.String(); !strings.Contains(p, "c1") || !strings.Contains(p, "deploy-token") {
		t.Errorf("the prompt names the client and the secret: %q", p)
	}
	f, _, _ = testFactoryTTY(t, "y\n")
	root = NewRootCmd(f)
	root.SetArgs([]string{"secret", "client", "revoke", "c1", "--server", srv.URL})
	if err := root.Execute(); err != nil || revokes2680(calls) != 1 {
		t.Fatalf("yes: %v %v", err, opsOf2631(calls))
	}
}

// The client id is sent as given in the read and never composed into anything
// else; a blank id is a usage error with no request.
func TestSecretClientRevokeBlankIDSendsNothing2680(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(), "RevokeSecretReadClientGrant": revoked2680("REVOKED")})
	for _, id := range []string{"", "  "} {
		_, err := runRevoke2680(t, srv.URL, id, "--yes")
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Errorf("id %q: exit=%d requests=%v err=%v", id, exitCodeFor(err), opsOf2631(calls), err)
		}
	}
}

// Joost's F1: success needs the SAME grant back in an explicit ended state. A
// missing, empty or unknown state, or an answer for another grant, is "not
// confirmed" (exit 7, the grant named, `secret client list`), sent once, and the
// DTO never claims the requested grant ended on the strength of another's answer.
func TestSecretClientRevokeNeedsTheSameGrantInAnEndedState2680(t *testing.T) {
	for name, resp := range map[string]string{
		"another grant":            revokedFor2680("gr-other", "REVOKED"),
		"empty id":                 revokedFor2680("", "REVOKED"),
		"empty state":              revokedFor2680("gr-c1", ""),
		"unknown state":            revokedFor2680("gr-c1", "SURPRISE"),
		"missing state":            `{"data":{"revokeGrant":{"id":"gr-c1","revokedAt":null,"allows":false}}}`,
		"missing fields":           `{"data":{"revokeGrant":{}}}`,
		"missing allows":           `{"data":{"revokeGrant":{"id":"gr-c1","state":"REVOKED","revokedAt":"2026-11-21T02:00:00Z"}}}`,
		"null allows":              `{"data":{"revokeGrant":{"id":"gr-c1","state":"REVOKED","revokedAt":"2026-11-21T02:00:00Z","allows":null}}}`,
		"revoked but still allows": revokedAllows2680("gr-c1", "REVOKED", true),
		"expired but still allows": revokedAllows2680("gr-c1", "EXPIRED", true),
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")), "RevokeSecretReadClientGrant": resp})
			out, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--json")
			if exitCodeFor(err) != exitcode.Unavailable || revokes2680(calls) != 1 || out != "" {
				t.Fatalf("exit=%d sends=%d out=%q err=%v", exitCodeFor(err), revokes2680(calls), out, err)
			}
			if m := err.Error(); !strings.Contains(m, "not confirmed") || !strings.Contains(m, "gr-c1") || !strings.Contains(m, "secret client list") {
				t.Errorf("name the grant and where to check: %v", err)
			}
		})
	}
}

// Joost's F2: a GraphQL error BENEATH revokeGrant (a result field failing after
// the write committed), or any code that is not a definitive pre-write refusal, is
// an unknown outcome: exit 7 with the recovery, sent once. The definitive refusals
// keep their typed exits only at the mutation's own level.
func TestSecretClientRevokeErrorsBelowTheWriteAreUnknown2680(t *testing.T) {
	at := func(code string, path string) string {
		return `{"data":{"revokeGrant":null},"errors":[{"message":"x","path":` + path + `,"extensions":{"code":"` + code + `"}}]}`
	}
	for name, tc := range map[string]struct {
		resp string
		exit int
	}{
		"result field fails after the write":   {at("INTERNAL_SERVER_ERROR", `["revokeGrant","state"]`), exitcode.Unavailable},
		"internal error at the mutation":       {at("INTERNAL_SERVER_ERROR", `["revokeGrant"]`), exitcode.Unavailable},
		"unlisted code with no path":           {`{"errors":[{"message":"x","extensions":{"code":"SOMETHING_ELSE"}}]}`, exitcode.Unavailable},
		"definitive code below the write":      {at("FORBIDDEN", `["revokeGrant","state"]`), exitcode.Unavailable},
		"not manager at the mutation":          {at("GRANT_NOT_MANAGER", `["revokeGrant"]`), exitcode.Forbidden},
		"forbidden at the mutation":            {at("FORBIDDEN", `["revokeGrant"]`), exitcode.Forbidden},
		"validation failure has no path":       {`{"errors":[{"message":"x","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, exitcode.Usage},
		"unauthenticated at the mutation":      {at("UNAUTHENTICATED", `["revokeGrant"]`), exitcode.AuthRequired},
		"mixed definitive and unlisted errors": {`{"errors":[{"message":"x","path":["revokeGrant"],"extensions":{"code":"FORBIDDEN"}},{"message":"y","path":["revokeGrant","state"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, exitcode.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")), "RevokeSecretReadClientGrant": tc.resp})
			_, err := runRevoke2680(t, srv.URL, "c1", "--yes")
			if exitCodeFor(err) != tc.exit || revokes2680(calls) != 1 {
				t.Fatalf("exit=%d want=%d sends=%d err=%v", exitCodeFor(err), tc.exit, revokes2680(calls), err)
			}
			if tc.exit == exitcode.Unavailable {
				if m := err.Error(); !strings.Contains(m, "gr-c1") || !strings.Contains(m, "secret client list") || !strings.Contains(strings.ToLower(m), "unknown") {
					t.Errorf("an unknown outcome names the grant and `secret client list`: %v", err)
				}
			}
		})
	}
}

// pagedClients2680 serves rows on the first page and an empty list after it, and
// answers revokeGrant with the given state, recording the refs it was sent.
func pagedClients2680(t *testing.T, rows []string, refs *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &b)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch b.OperationName {
		case "MySecretReadClients":
			if off, _ := b.Variables["offset"].(float64); off == 0 {
				_, _ = w.Write([]byte(readClients2680(rows...)))
				return
			}
			_, _ = w.Write([]byte(readClients2680()))
		case "RevokeSecretReadClientGrant":
			ref, _ := b.Variables["ref"].(string)
			*refs = append(*refs, ref)
			_, _ = w.Write([]byte(revokedFor2680(ref, "REVOKED")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Rook's #2757 P3: the client id is matched EXACTLY. With two clients sharing a
// prefix, a partial id (or another case) is not found: exit 4, no revoke, so it
// can never pick and irreversibly revoke a different client.
func TestSecretClientRevokeMatchesTheClientIDExactly2680(t *testing.T) {
	rows := []string{clientRow2680("c1-long", "gr-long"), clientRow2680("c1-other", "gr-other")}
	for _, id := range []string{"c", "c1", "c-", "C1-LONG"} {
		t.Run(id, func(t *testing.T) {
			var refs []string
			srv := pagedClients2680(t, rows, &refs)
			_, err := runRevoke2680(t, srv.URL, id, "--yes")
			if exitCodeFor(err) != exitcode.NotFound || len(refs) != 0 {
				t.Fatalf("id %q: exit=%d refs=%v err=%v", id, exitCodeFor(err), refs, err)
			}
		})
	}
	var refs []string
	srv := pagedClients2680(t, []string{clientRow2680("c1-long", "gr-long"), clientRow2680("c1", "gr-c1")}, &refs)
	if _, err := runRevoke2680(t, srv.URL, "c1", "--yes"); err != nil || len(refs) != 1 || refs[0] != "gr-c1" {
		t.Fatalf("the exact id must revoke its own grant: %v refs=%v", err, refs)
	}
}

// Dorian's #2757 P2: a client row with no grant id never reaches the prompt or the
// mutation (an empty ref would be sent otherwise): refused, zero revoke calls.
func TestSecretClientRevokeRefusesAClientWithoutAGrantID2680(t *testing.T) {
	for _, grant := range []string{"", "   "} {
		srv, calls := attnServer(t, map[string]string{
			"MySecretReadClients":         readClients2680(clientRow2680("c1", grant)),
			"RevokeSecretReadClientGrant": revoked2680("REVOKED"),
		})
		_, err := runRevoke2680(t, srv.URL, "c1", "--yes")
		if exitCodeFor(err) != exitcode.Error || revokes2680(calls) != 0 {
			t.Fatalf("grant %q: exit=%d revokes=%d err=%v", grant, exitCodeFor(err), revokes2680(calls), err)
		}
		if !strings.Contains(err.Error(), "no grant id") {
			t.Errorf("say why nothing was sent: %v", err)
		}
	}
	// The prompt is not shown for a row that cannot be revoked.
	f, _, errOut := testFactoryTTY(t, "y\n")
	srv, calls := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", ""))})
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "client", "revoke", "c1", "--server", srv.URL})
	if err := root.Execute(); err == nil || revokes2680(calls) != 0 || strings.Contains(errOut.String(), "Revoke read client") {
		t.Errorf("no prompt and no revoke for a row without a grant id: %v %q", err, errOut.String())
	}
}

// Dorian's #2757 P3: the server's code rides to the --json error envelope on both
// kinds of failure (a definitive refusal keeps its exit, an unknown outcome is
// exit 7), so a script can tell them apart without parsing the message.
func TestSecretClientRevokeKeepsTheServerCode2680(t *testing.T) {
	for name, tc := range map[string]struct {
		resp, code string
		exit       int
	}{
		"refusal": {`{"errors":[{"message":"x","path":["revokeGrant"],"extensions":{"code":"GRANT_NOT_MANAGER"}}]}`, "GRANT_NOT_MANAGER", exitcode.Forbidden},
		"unknown": {`{"data":{"revokeGrant":null},"errors":[{"message":"x","path":["revokeGrant","state"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, "INTERNAL_SERVER_ERROR", exitcode.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := attnServer(t, map[string]string{"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")), "RevokeSecretReadClientGrant": tc.resp})
			f, out := testFactory(t)
			var errOut strings.Builder
			f.IOStreams.ErrOut = &errOut
			f.JSON = true
			root := NewRootCmd(f)
			root.SetArgs([]string{"secret", "client", "revoke", "c1", "--yes", "--json", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit=%d want=%d err=%v", exitCodeFor(err), tc.exit, err)
			}
			renderError(f, err)
			if env := out.String() + errOut.String(); !strings.Contains(env, `"serverCode": "`+tc.code+`"`) {
				t.Errorf("the envelope must carry serverCode %s: %q", tc.code, env)
			}
		})
	}
}

// Dara's #2757 P3: the not-found message and the recovery text follow the real
// rule (clients you issued OR hold), and recovery points at `list --all`, which
// reaches a client the revoke command found past the first page.
func TestSecretClientRevokeMessagesNameTheRealRule2680(t *testing.T) {
	var refs []string
	srv := pagedClients2680(t, []string{clientRow2680("c0", "gr-c0")}, &refs)
	_, err := runRevoke2680(t, srv.URL, "nope", "--yes")
	if err == nil || !strings.Contains(err.Error(), "issued or hold") || !strings.Contains(err.Error(), "secret client list --all") {
		t.Errorf("not-found names the issued-or-held rule and list --all: %v", err)
	}
	lost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "RevokeSecretReadClientGrant") {
			w.WriteHeader(502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(readClients2680(clientRow2680("c1", "gr-c1"))))
	}))
	defer lost.Close()
	_, err = runRevoke2680(t, lost.URL, "c1", "--yes")
	if err == nil || !strings.Contains(err.Error(), "secret client list --all") {
		t.Errorf("recovery points at list --all: %v", err)
	}
}

// Dara's #2757 P3: the mutation reaches the server and the connection closes before
// any answer (a transport error, not an HTTP status): exit 7, one send, the unknown
// outcome and the recovery.
func TestSecretClientRevokeConnectionClosedAfterTheSend2680(t *testing.T) {
	var mu sync.Mutex
	sends := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "RevokeSecretReadClientGrant") {
			mu.Lock()
			sends++
			mu.Unlock()
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(readClients2680(clientRow2680("c1", "gr-c1"))))
	}))
	defer srv.Close()
	_, err := runRevoke2680(t, srv.URL, "c1", "--yes")
	mu.Lock()
	defer mu.Unlock()
	if exitCodeFor(err) != exitcode.Unavailable || sends != 1 {
		t.Fatalf("exit=%d sends=%d err=%v", exitCodeFor(err), sends, err)
	}
	if m := err.Error(); !strings.Contains(m, "gr-c1") || !strings.Contains(m, "secret client list --all") || !strings.Contains(strings.ToLower(m), "unknown") {
		t.Errorf("an unknown outcome names the grant and the recovery: %v", err)
	}
}
