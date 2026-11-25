package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2789: `agent subscription revoke`. Fake HTTP only; no real user or provider data.

const subRevoked2789 = `{"data":{"revokeAgentSubscription":{"agentId":"ag-1","userId":"u-7","isActive":false,"createdAt":"2026-11-01T00:00:00Z","activatedAt":"2026-11-01T01:00:00Z","expiresAt":null,"revokedAt":"2026-11-21T09:00:00Z","revokedBy":"u-admin"}}}`

func subAnswer2789(patch map[string]any) string {
	var base map[string]any
	_ = json.Unmarshal([]byte(subRevoked2789), &base)
	s := base["data"].(map[string]any)["revokeAgentSubscription"].(map[string]any)
	for k, v := range patch {
		s[k] = v
	}
	b, _ := json.Marshal(base)
	return string(b)
}

func runSubRevoke2789(t *testing.T, srvURL string, args ...string) (stdout string, err error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"agent", "subscription", "revoke"}, args...), "--server", srvURL))
	err = root.Execute()
	return out.String(), err
}

func subRevokes2789(calls *[]attnCall) int {
	n := 0
	for _, c := range *calls {
		if c.Op == "RevokeAgentSubscription" {
			n++
		}
	}
	return n
}

// Acceptance 1: exactly the two refs, as given (trimmed), on one mutation. The JSON
// shape is the eight fields of the subscription, stable; the text states the revoke
// and names the memory side effect without claiming an outcome for the memory.
func TestAgentSubscriptionRevokeSendsOneMutationWithTheRefs2789(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"RevokeAgentSubscription": subRevoked2789})
	out, err := runSubRevoke2789(t, srv.URL, "  hrn:agent:acme.com:helper ", "--user", " alice@acme.com ", "--yes", "--json")
	if err != nil || len(*calls) != 1 || subRevokes2789(calls) != 1 {
		t.Fatalf("err=%v ops=%v", err, opsOf2631(calls))
	}
	if v := (*calls)[0].Vars; v["agentRef"] != "hrn:agent:acme.com:helper" || v["userRef"] != "alice@acme.com" || len(v) != 2 {
		t.Fatalf("variables = %v, want exactly the two trimmed refs", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"agentId", "userId", "isActive", "createdAt", "activatedAt", "expiresAt", "revokedAt", "revokedBy"} {
		if _, ok := got[k]; !ok {
			t.Errorf("--json lacks %q: %v", k, got)
		}
	}
	if len(got) != 8 || got["isActive"] != false || got["expiresAt"] != nil || got["revokedAt"] != "2026-11-21T09:00:00Z" {
		t.Errorf("--json = %v", got)
	}
	text, err := runSubRevoke2789(t, srv.URL, "ag-1", "--user", "u-7", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"is revoked", "u-7", "ag-1", "empty personal memory", "hadron memory list"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "was deleted") || strings.Contains(text, "memory deleted") || strings.Contains(text, "memory kept") {
		t.Errorf("the text must not claim an outcome for the memory:\n%s", text)
	}
}

// Acceptance 2: a refusal, a cancel, and off-terminal-without-yes each send no write.
// On a terminal the prompt names the user, the Agent and the memory deletion.
func TestAgentSubscriptionRevokeConfirmationSendsNothingUnlessConfirmed2789(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"RevokeAgentSubscription": subRevoked2789})
	_, err := runSubRevoke2789(t, srv.URL, "ag-1", "--user", "u-7")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Fatalf("off a terminal without --yes: exit=%d requests=%v err=%v", exitCodeFor(err), opsOf2631(calls), err)
	}
	for _, no := range []string{"n\n", "\n", "maybe\n"} {
		f, _, errOut := testFactoryTTY(t, no)
		root := NewRootCmd(f)
		root.SetArgs([]string{"agent", "subscription", "revoke", "ag-1", "--user", "u-7", "--server", srv.URL})
		if err := root.Execute(); err == nil || len(*calls) != 0 {
			t.Fatalf("answer %q must send nothing: err=%v requests=%v", no, err, opsOf2631(calls))
		}
		if p := errOut.String(); !strings.Contains(p, "u-7") || !strings.Contains(p, "ag-1") || !strings.Contains(p, "DELETES") || !strings.Contains(p, "personal memory") || !strings.Contains(p, "no nodes") {
			t.Errorf("the prompt names the user, the Agent and the memory deletion: %q", p)
		}
	}
	f, _, _ := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "subscription", "revoke", "ag-1", "--user", "u-7", "--server", srv.URL})
	if err := root.Execute(); err != nil || subRevokes2789(calls) != 1 {
		t.Fatalf("yes: err=%v requests=%v", err, opsOf2631(calls))
	}
}

// A blank ref is a usage error with no request; --user is required.
func TestAgentSubscriptionRevokeBlankOrMissingRefsSendNothing2789(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"RevokeAgentSubscription": subRevoked2789})
	for name, args := range map[string][]string{
		"blank agent": {"  ", "--user", "u-7", "--yes"},
		"blank user":  {"ag-1", "--user", "  ", "--yes"},
		"no --user":   {"ag-1", "--yes"},
		"no agent":    {"--user", "u-7", "--yes"},
	} {
		_, err := runSubRevoke2789(t, srv.URL, args...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Errorf("%s: exit=%d requests=%v err=%v", name, exitCodeFor(err), opsOf2631(calls), err)
		}
	}
}

// Acceptance 3: the server's refusals before the write keep their typed exit and are
// not an unknown outcome: not signed in 3, not permitted 8, unknown or unreadable
// Agent (or user) 4, an unusable request 2. Each is exactly one send.
func TestAgentSubscriptionRevokeRefusalsKeepTheirExit2789(t *testing.T) {
	for code, want := range map[string]int{"UNAUTHENTICATED": exitcode.AuthRequired, "FORBIDDEN": exitcode.Forbidden, "NOT_FOUND": exitcode.NotFound, "AGENT_NOT_FOUND": exitcode.NotFound, "BAD_USER_INPUT": exitcode.Usage} {
		t.Run(code, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"RevokeAgentSubscription": gqlErrorJSON(code)})
			_, err := runSubRevoke2789(t, srv.URL, "ag-1", "--user", "u-7", "--yes")
			if exitCodeFor(err) != want || subRevokes2789(calls) != 1 {
				t.Fatalf("exit=%d want=%d sends=%d err=%v", exitCodeFor(err), want, subRevokes2789(calls), err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "unknown") {
				t.Errorf("a refusal before the write is not an unknown outcome: %v", err)
			}
		})
	}
}

// Acceptance 4: everything else after the request left may follow the write (and the
// empty-memory deletion after it), so it says the outcome is unknown, names where to
// check, and is never resent.
func TestAgentSubscriptionRevokeUnknownOutcomeIsNeverResent2789(t *testing.T) {
	at := func(code, path string) string {
		return `{"data":{"revokeAgentSubscription":null},"errors":[{"message":"x","path":` + path + `,"extensions":{"code":"` + code + `"}}]}`
	}
	for name, tc := range map[string]struct {
		resp string
		exit int
	}{
		"internal error at the mutation":       {at("INTERNAL_SERVER_ERROR", `["revokeAgentSubscription"]`), exitcode.Unavailable},
		"a result field fails after the write": {at("INTERNAL_SERVER_ERROR", `["revokeAgentSubscription","user"]`), exitcode.Unavailable},
		"a definitive code below the mutation": {at("FORBIDDEN", `["revokeAgentSubscription","user"]`), exitcode.Unavailable},
		"an unlisted code, no path":            {`{"errors":[{"message":"x","extensions":{"code":"SOMETHING_ELSE"}}]}`, exitcode.Unavailable},
		"mixed definitive and unlisted":        {`{"errors":[{"message":"x","path":["revokeAgentSubscription"],"extensions":{"code":"FORBIDDEN"}},{"message":"y","path":["revokeAgentSubscription","user"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, exitcode.Unavailable},
		"a bare 5xx body":                      {`{"errors":[{"message":"upstream","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, exitcode.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"RevokeAgentSubscription": tc.resp})
			_, err := runSubRevoke2789(t, srv.URL, "ag-1", "--user", "u-7", "--yes")
			if exitCodeFor(err) != tc.exit || subRevokes2789(calls) != 1 {
				t.Fatalf("exit=%d want=%d sends=%d err=%v", exitCodeFor(err), tc.exit, subRevokes2789(calls), err)
			}
			m := err.Error()
			if !strings.Contains(strings.ToLower(m), "unknown") || !strings.Contains(m, "hadron memory list") || !strings.Contains(m, "not resent") || strings.Contains(m, "u-7") || strings.Contains(m, "ag-1") {
				t.Errorf("an unknown outcome says so, names `memory ls`, shows neither ref (cli#3114) and that it is not resent: %v", err)
			}
		})
	}
}

// A connection that closes after the request left is an unknown outcome too, and the
// request is not repeated (the CLI client must not retry this write).
func TestAgentSubscriptionRevokeConnectionClosedAfterTheSend2789(t *testing.T) {
	var mu sync.Mutex
	sends := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "RevokeAgentSubscription") {
			mu.Lock()
			sends++
			mu.Unlock()
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()
	_, err := runSubRevoke2789(t, srv.URL, "ag-1", "--user", "u-7", "--yes")
	mu.Lock()
	defer mu.Unlock()
	if exitCodeFor(err) != exitcode.Unavailable || sends != 1 {
		t.Fatalf("exit=%d sends=%d err=%v", exitCodeFor(err), sends, err)
	}
	if m := err.Error(); !strings.Contains(strings.ToLower(m), "unknown") || !strings.Contains(m, "hadron memory list") {
		t.Errorf("an unknown outcome names the recovery: %v", err)
	}
}

// A missing or malformed answer is not a confirmation: no subscription, an active one,
// no revokedAt, or empty ids each exit 7 (one send, no success output).
func TestAgentSubscriptionRevokeMalformedAnswersAreNotConfirmations2789(t *testing.T) {
	for name, resp := range map[string]string{
		"null subscription": `{"data":{"revokeAgentSubscription":null}}`,
		"still active":      subAnswer2789(map[string]any{"isActive": true}),
		"no revokedAt":      subAnswer2789(map[string]any{"revokedAt": nil}),
		"empty agent id":    subAnswer2789(map[string]any{"agentId": " "}),
		"empty user id":     subAnswer2789(map[string]any{"userId": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"RevokeAgentSubscription": resp})
			out, err := runSubRevoke2789(t, srv.URL, "ag-1", "--user", "u-7", "--yes")
			if exitCodeFor(err) != exitcode.Unavailable || subRevokes2789(calls) != 1 || strings.Contains(out, "is revoked") {
				t.Fatalf("exit=%d sends=%d out=%q err=%v", exitCodeFor(err), subRevokes2789(calls), out, err)
			}
		})
	}
}

// Acceptance 5: help says what the command does to a memory and does not claim it only
// removes a license; the group's help points the same way.
func TestAgentSubscriptionRevokeHelpNamesTheMemorySideEffect2789(t *testing.T) {
	f, _ := testFactory(t)
	c, _, err := NewRootCmd(f).Find([]string{"agent", "subscription", "revoke"})
	if err != nil || c.Name() != "revoke" {
		t.Fatalf("no revoke command: %v", err)
	}
	long := strings.Join(strings.Fields(c.Long), " ")
	for _, want := range []string{"DELETE a memory", "personal memory", "has no nodes", "keeps it if it has any", "decides which", "hadron memory list", "never resent", "not an organization license", "subscription list --agent"} {
		if !strings.Contains(long, want) {
			t.Errorf("the help lacks %q", want)
		}
	}
	if !strings.Contains(c.Short, "delete") {
		t.Errorf("the Short must name the deletion: %q", c.Short)
	}
	for _, bad := range []string{"only removes", "just removes", "only a license", "does not delete"} {
		if strings.Contains(strings.ToLower(long), bad) {
			t.Errorf("the help must not claim %q", bad)
		}
	}
	grp, _, _ := NewRootCmd(f).Find([]string{"agent", "subscription"})
	if grp == nil || !strings.Contains(grp.Short, "revoke") {
		t.Errorf("the group's Short must mention revoke: %v", grp)
	}
}

// The operation is exactly one root mutation with exactly the two ref arguments: no
// read of the memory first, no second call.
func TestAgentSubscriptionRevokeDocumentIsOneMutation2789(t *testing.T) {
	doc, err := parser.ParseQuery(&ast.Source{Input: gen.RevokeAgentSubscription_Operation})
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Operations.ForName("RevokeAgentSubscription")
	if op == nil || op.Operation != ast.Mutation || len(op.SelectionSet) != 1 {
		t.Fatalf("want one root mutation field: %+v", op)
	}
	fld := op.SelectionSet[0].(*ast.Field)
	if fld.Name != "revokeAgentSubscription" || len(fld.Arguments) != 2 || fld.Arguments.ForName("agentRef") == nil || fld.Arguments.ForName("userRef") == nil {
		t.Fatalf("root field = %s with %d args", fld.Name, len(fld.Arguments))
	}
	var names []string
	for _, s := range fld.SelectionSet {
		names = append(names, s.(*ast.Field).Name)
	}
	if got := strings.Join(names, ","); got != "agentId,userId,isActive,createdAt,activatedAt,expiresAt,revokedAt,revokedBy" {
		t.Errorf("selection = %s", got)
	}
}
