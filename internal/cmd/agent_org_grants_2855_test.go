package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agent"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2855 (follow-up to #2788): the P3s deferred from the round-2 reads of PR #2837.

// P3 1: `list --mine` on an empty set must not claim complete:true. A run or impersonated caller has
// no personal subscriptions, so an empty list proves nothing; the whole-set claim is held back
// (cor:dmo:050:07 is untouched: the page still says hasMore:false and total 0).
func TestSubscriptionMineEmptyIsNotMarkedComplete2855(t *testing.T) {
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{"MyAgentSubscriptionsPage": {page2788("myAgentSubscriptionsPage", 0, false)}}, "list", "--mine", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items    []any `json:"items"`
		Total    int   `json:"total"`
		HasMore  bool  `json:"hasMore"`
		Complete bool  `json:"complete"`
	}
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatal(jerr, out)
	}
	if got.Complete || got.HasMore || got.Total != 0 || got.Items == nil || len(got.Items) != 0 {
		t.Errorf("an empty --mine list is not a whole-set claim; hasMore and total stay as the server said: %+v", got)
	}
	// The same empty set read for an Agent (the server answered for that Agent) is still complete.
	_, out, err = grants2788Run(t, map[string][]grants2788Reply{"AgentSubscriptionsPage": {page2788("agentSubscriptionsPage", 0, false)}}, "list", "--agent", "ag1", "--json")
	if err != nil || !strings.Contains(out, `"complete": true`) && !strings.Contains(out, `"complete":true`) {
		t.Errorf("an empty --agent list stays complete: %v\n%s", err, out)
	}
	// The text is the caveat only, not the "answer is inconsistent" footer.
	_, text, err := grants2788Run(t, map[string][]grants2788Reply{"MyAgentSubscriptionsPage": {page2788("myAgentSubscriptionsPage", 0, false)}}, "list", "--mine")
	if err != nil || !strings.Contains(text, "does not prove") || strings.Contains(text, "inconsistent") || strings.Contains(text, "NOT the whole set") {
		t.Errorf("empty --mine text: %v\n%s", err, text)
	}
}

// P3 2: --all stops at a hard row cap and says so loudly: the walk was cut, how far it got, and
// that the list is NOT the whole set (the spirit of cor:api:040:02). The cap is checked after a
// page, so a page that ends the set under the cap is untouched.
func TestSubscriptionAllStopsLoudlyAtTheRowCap2855(t *testing.T) {
	old := agent.MaxSubscriptionRows
	agent.MaxSubscriptionRows = 3
	defer func() { agent.MaxSubscriptionRows = old }()
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{"AgentSubscriptionsPage": {
		page2788("agentSubscriptionsPage", 9, true, sub2788(1), sub2788(2)),
		page2788("agentSubscriptionsPage", 9, true, sub2788(3), sub2788(4)),
		page2788("agentSubscriptionsPage", 9, true, sub2788(5), sub2788(6)),
	}}, "list", "--agent", "ag1", "--all", "--json")
	if err == nil || exitcode.FromError(err) == exitcode.OK {
		t.Fatalf("a cut walk must fail, got %v\n%s", err, out)
	}
	for _, want := range []string{"cut at 4", "total of 9", "NOT the whole set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the cut must say %q: %v", want, err)
		}
	}
	if strings.Contains(out, `"complete": true`) || strings.Contains(out, `"complete":true`) {
		t.Errorf("never complete:true on a cut walk: %s", out)
	}
	// Under the cap, the walk is whole.
	agent.MaxSubscriptionRows = 10
	_, out, err = grants2788Run(t, map[string][]grants2788Reply{"AgentSubscriptionsPage": {
		page2788("agentSubscriptionsPage", 3, true, sub2788(1), sub2788(2)),
		page2788("agentSubscriptionsPage", 3, false, sub2788(3)),
	}}, "list", "--agent", "ag1", "--all", "--json")
	if err != nil || !strings.Contains(out, `"complete": true`) && !strings.Contains(out, `"complete":true`) {
		t.Errorf("a set under the cap is read whole: %v\n%s", err, out)
	}
}

// P3 3: the unknown-outcome error keeps its cause instead of flattening it to text: what the server
// sent (its code and request id) reaches the --json envelope, and the message adds nothing.
func TestSubscriptionUnknownOutcomeKeepsTheServerCause2855(t *testing.T) {
	body := `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req_2855"}}],"data":null}`
	_, _, err := grants2788Run(t, map[string][]grants2788Reply{"SubscribeOrgToAgent": {{body: body}}}, "add", "--org", "o", "--agent", "a")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("unknown outcome is exit 7: %v", err)
	}
	f, out := testFactory(t)
	f.JSON = true
	renderError(f, err)
	var env struct {
		Error map[string]any `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
		t.Fatal(jerr, out.String())
	}
	if env.Error["serverCode"] != "INTERNAL_SERVER_ERROR" || env.Error["requestId"] != "req_2855" {
		t.Errorf("the server's code and request id must survive: %v", env.Error)
	}
	if !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), "subscription list --org") {
		t.Errorf("the check-first guidance stays: %v", err)
	}
}

// P3 4: add and revoke map every JSON field from its own field (each value distinct), so a swapped
// or dropped field fails, not only a missing one.
func TestSubscriptionGrantJSONMapsEachFieldFromItsOwn2855(t *testing.T) {
	row := `{"agentId":"AG-1","orgId":"ORG-2","isActive":%s,"createdAt":"CR-3","activatedAt":"AC-4","expiresAt":"EX-5","revokedAt":%s}`
	for name, tc := range map[string]struct {
		op, field string
		args      []string
		active    string
		revoked   string
	}{
		"add":    {"SubscribeOrgToAgent", "createAgentOrgGrant", []string{"add"}, "true", "null"},
		"revoke": {"UnsubscribeOrgFromAgent", "revokeAgentOrgGrant", []string{"revoke", "--yes"}, "false", `"RV-6"`},
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"data":{"` + tc.field + `":` + sprintf2855(row, tc.active, tc.revoked) + `}}`
			_, out, err := grants2788Run(t, map[string][]grants2788Reply{tc.op: {{body: body}}}, append(orgArgs2788(tc.args), "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
				t.Fatal(jerr, out)
			}
			var wantRevoked any
			if tc.revoked != "null" {
				wantRevoked = "RV-6"
			}
			for k, want := range map[string]any{"agentId": "AG-1", "orgId": "ORG-2", "isActive": tc.active == "true", "createdAt": "CR-3", "activatedAt": "AC-4", "expiresAt": "EX-5", "revokedAt": wantRevoked} {
				if got[k] != want {
					t.Errorf("%s = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

func sprintf2855(format string, a ...string) string {
	out := format
	for _, v := range a {
		out = strings.Replace(out, "%s", v, 1)
	}
	return out
}
