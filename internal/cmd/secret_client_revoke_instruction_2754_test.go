package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	gql "github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const instructedRevoke2754 = "RevokeSecretReadClientGrantInstructed"

func revokeResponses2754() map[string]string {
	return map[string]string{
		"MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1")),
		instructedRevoke2754:  revoked2680("REVOKED"),
		"TeamAppIdentity":     teamAppIdentityJSON,
	}
}

func TestSecretClientRevokeInstructionCarriesBoundSession2754(t *testing.T) {
	for _, kind := range []string{"task", "team-chat-message", "live-chat"} {
		t.Run(kind, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, revokeResponses2754())
			setTeamBindingServer(t, srv.URL)
			const ref = "opaque:address:with:colons"
			out, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--json", "--instruction", kind+":"+ref)
			if err != nil {
				t.Fatal(err)
			}
			reads, writes := 0, 0
			for _, c := range *calls {
				switch c.Op {
				case "MySecretReadClients":
					reads++
					if c.Session != "s-new" {
						t.Errorf("lookup session %q", c.Session)
					}
					if _, ok := c.Vars["instruction"]; ok {
						t.Error("instruction on lookup")
					}
				case instructedRevoke2754:
					writes++
					if c.Session != "s-new" || c.Vars["ref"] != "gr-c1" {
						t.Errorf("mutation: %+v", c)
					}
					doc, parseErr := parser.ParseQuery(&gql.Source{Input: c.Query})
					if parseErr != nil {
						t.Fatal(parseErr)
					}
					op := doc.Operations.ForName(instructedRevoke2754)
					if op == nil || len(op.SelectionSet) != 1 {
						t.Fatal("missing single instructed mutation")
					}
					field, ok := op.SelectionSet[0].(*gql.Field)
					if !ok || field.Name != "revokeGrant" {
						t.Fatal("wrong mutation root")
					}
					arg := field.Arguments.ForName("instruction")
					if arg == nil || arg.Value.Kind != gql.Variable || arg.Value.Raw != "instruction" {
						t.Fatal("claim not wired to revokeGrant")
					}
					claim, ok := c.Vars["instruction"].(map[string]any)
					wantKind := map[string]string{"task": "TASK", "team-chat-message": "TEAM_CHAT_MESSAGE", "live-chat": "LIVE_CHAT"}[kind]
					if !ok || claim["kind"] != wantKind || claim["ref"] != ref {
						t.Errorf("claim: %v", c.Vars)
					}
				case "RevokeSecretReadClientGrant":
					t.Error("person fallback")
				}
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if reads != 1 || writes != 1 || result["state"] != "REVOKED" {
				t.Fatalf("reads %d writes %d out %s", reads, writes, out)
			}
		})
	}
}

func TestSecretClientRevokeInstructionRefusesBeforeRequests2754(t *testing.T) {
	for _, tc := range []struct {
		name, instruction string
		bound             bool
		mutate            func(map[string]any)
	}{
		{name: "unbound", instruction: "task:ref"},
		{name: "empty", instruction: "", bound: true},
		{name: "blank-ref", instruction: "task: ", bound: true},
		{name: "unknown-kind", instruction: "unknown:ref", bound: true},
		{name: "incomplete", instruction: "task:ref", bound: true, mutate: func(b map[string]any) { b["sessionId"] = "" }},
		{name: "wrong-server", instruction: "task:ref", bound: true, mutate: func(b map[string]any) { b["server"] = "https://other.invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bound {
				writeTeamBinding(t)
			} else {
				teamGitDir(t)
			}
			srv, calls := attnServer(t, revokeResponses2754())
			if tc.bound {
				setTeamBindingServer(t, srv.URL)
			}
			if tc.mutate != nil {
				rewriteBinding1592(t, tc.mutate)
			}
			_, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--instruction", tc.instruction)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("exit %d calls %v: %v", exitCodeFor(err), opsOf2631(calls), err)
			}
		})
	}
}

func TestSecretClientRevokeInstructionRechecksBinding2754(t *testing.T) {
	for _, found := range []bool{true, false} {
		name := "before-mutation"
		if !found {
			name = "before-next-page"
		}
		t.Run(name, func(t *testing.T) {
			writeTeamBinding(t)
			responses := revokeResponses2754()
			if !found {
				responses["MySecretReadClients"] = readClients2680(clientRow2680("other", "other-grant"))
			}
			srv, calls := attnServerHook(t, responses, func(op string) {
				if op == "MySecretReadClients" {
					rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "replacement" })
				}
			})
			setTeamBindingServer(t, srv.URL)
			_, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--instruction", "task:ref")
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want usage: %v", err)
			}
			reads := 0
			for _, c := range *calls {
				if c.Op == "MySecretReadClients" {
					reads++
				}
				if strings.HasPrefix(c.Op, "RevokeSecret") {
					t.Fatalf("mutation after rebind: %+v", c)
				}
			}
			if reads != 1 {
				t.Fatalf("lookup replayed after rebind: %v", opsOf2631(calls))
			}
		})
	}
}

func TestSecretClientRevokeInstructionUniformRefusalAndLostAnswer2754(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		code           int
	}{
		{"uniform-grant-refusal", gqlErrorJSON("GRANT_NOT_MANAGER"), exitcode.Forbidden},
		{"old-server", gqlErrorJSON("GRAPHQL_VALIDATION_FAILED"), exitcode.Usage},
		{"lost-answer", `{"data":{"revokeGrant":null}}`, exitcode.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			responses := revokeResponses2754()
			responses[instructedRevoke2754] = tc.response
			srv, calls := attnServer(t, responses)
			setTeamBindingServer(t, srv.URL)
			_, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--instruction", "task:ref")
			if exitCodeFor(err) != tc.code {
				t.Fatalf("exit %d: %v", exitCodeFor(err), err)
			}
			writes := 0
			for _, c := range *calls {
				if c.Op == instructedRevoke2754 {
					writes++
				}
				if c.Op == "RevokeSecretReadClientGrant" {
					t.Error("fallback to person")
				}
			}
			if writes != 1 {
				t.Fatalf("writes %d", writes)
			}
			if tc.name == "uniform-grant-refusal" && (!strings.Contains(err.Error(), "GRANT_NOT_MANAGER") || strings.Contains(err.Error(), "not found")) {
				t.Fatalf("nonuniform refusal: %v", err)
			}
			if tc.name == "lost-answer" && !strings.Contains(err.Error(), "secret client list --all --as-worker") {
				t.Fatalf("missing recovery: %v", err)
			}
		})
	}
}

func TestSecretClientRevokeInstructionWrongAppStopsAtIdentity2754(t *testing.T) {
	writeTeamBinding(t)
	responses := revokeResponses2754()
	responses["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp900000000000000000000")
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	_, err := runRevoke2680(t, srv.URL, "c1", "--yes", "--instruction", "task:ref", "--app", "hrn:app:acme.com:other")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("wrong App: %v", err)
	}
	for _, c := range *calls {
		if c.Op != "TeamAppIdentity" || c.Session != "" {
			t.Fatalf("refused App sent %+v", c)
		}
	}
}

func TestSecretClientRevokeInstructionNeedsYesBeforeAnyRequest2754(t *testing.T) {
	writeTeamBinding(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	setTeamBindingServer(t, srv.URL)
	_, err := runRevoke2680(t, srv.URL, "c1", "--instruction", "task:ref")
	if exitCodeFor(err) != exitcode.Usage || calls.Load() != 0 {
		t.Fatalf("confirmation refusal sent %d requests: %v", calls.Load(), err)
	}
}
