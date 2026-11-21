package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2788: org subscribe/revoke and the paged Agent subscription reads.
// Fake transport only.

type grants2788Reply struct {
	body string
	drop bool // abort the connection: the answer is lost
}

type grants2788Fake struct {
	mu    sync.Mutex
	calls map[string]int
	vars  map[string][]map[string]any
}

func newGrants2788(t *testing.T, replies map[string][]grants2788Reply) (*httptest.Server, *grants2788Fake) {
	t.Helper()
	fk := &grants2788Fake{calls: map[string]int{}, vars: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fk.mu.Lock()
		n := fk.calls[body.OperationName]
		fk.calls[body.OperationName]++
		fk.vars[body.OperationName] = append(fk.vars[body.OperationName], body.Variables)
		seq := replies[body.OperationName]
		fk.mu.Unlock()
		if n >= len(seq) {
			t.Errorf("unexpected call %d of %q", n+1, body.OperationName)
			seq = []grants2788Reply{{body: `{"errors":[{"message":"unexpected","extensions":{"code":"BAD_USER_INPUT"}}]}`}}
			n = 0
		}
		if seq[n].drop {
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(seq[n].body))
	}))
	t.Cleanup(srv.Close)
	return srv, fk
}

func (fk *grants2788Fake) count(op string) int {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	return fk.calls[op]
}

func grants2788Run(t *testing.T, replies map[string][]grants2788Reply, args ...string) (*grants2788Fake, string, error) {
	t.Helper()
	srv, fk := newGrants2788(t, replies)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"agent", "subscription"}, args...), "--server", srv.URL))
	err := root.Execute()
	return fk, out.String(), err
}

const grant2788 = `{"agentId":"ag1","orgId":"org1","isActive":%t,"createdAt":"2026-01-01T00:00:00Z","activatedAt":"2026-01-02T00:00:00Z","expiresAt":null,"revokedAt":%s}`

func sub2788(n int) string {
	return fmt.Sprintf(`{"agentId":"ag1","userId":"u%d","isActive":true,"createdAt":"c","activatedAt":null,"expiresAt":null,"revokedAt":null}`, n)
}

func page2788(op string, total int, more bool, items ...string) grants2788Reply {
	return grants2788Reply{body: fmt.Sprintf(`{"data":{"%s":{"total":%d,"hasMore":%t,"items":[%s]}}}`, op, total, more, strings.Join(items, ","))}
}

func refusal2788(code string) grants2788Reply {
	return grants2788Reply{body: `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}],"data":null}`}
}

func TestSubscriptionAddSendsExactRefsOnce2788(t *testing.T) {
	fk, out, err := grants2788Run(t, map[string][]grants2788Reply{
		"SubscribeOrgToAgent": {{body: `{"data":{"createAgentOrgGrant":` + fmt.Sprintf(grant2788, true, "null") + `}}`}},
	}, "add", "--org", "hrn:org:acme.com", "--agent", "agent-ref", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if fk.count("SubscribeOrgToAgent") != 1 {
		t.Fatalf("want exactly one write, got %d", fk.count("SubscribeOrgToAgent"))
	}
	v := fk.vars["SubscribeOrgToAgent"][0]
	if v["agentRef"] != "agent-ref" || v["orgRef"] != "hrn:org:acme.com" {
		t.Errorf("refs must pass through unchanged, got %v", v)
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil || dto["agentId"] != "ag1" || dto["isActive"] != true || dto["revokedAt"] != nil {
		t.Errorf("--json = %s (%v)", out, jerr)
	}
}

func TestSubscriptionRevokeNeedsYesAndSendsOnce2788(t *testing.T) {
	// Non-interactive without --yes: refused before any request.
	fk, _, err := grants2788Run(t, map[string][]grants2788Reply{}, "revoke", "--org", "o", "--agent", "a")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("without --yes: exit %d expected, got %v", exitcode.Usage, err)
	}
	if fk.count("UnsubscribeOrgFromAgent") != 0 {
		t.Error("a request was sent without confirmation")
	}
	fk, out, err := grants2788Run(t, map[string][]grants2788Reply{
		"UnsubscribeOrgFromAgent": {{body: `{"data":{"revokeAgentOrgGrant":` + fmt.Sprintf(grant2788, false, `"2026-02-01T00:00:00Z"`) + `}}`}},
	}, "unsubscribe", "--org", "o", "--agent", "a", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if fk.count("UnsubscribeOrgFromAgent") != 1 || !strings.Contains(out, `"isActive": false`) && !strings.Contains(out, `"isActive":false`) {
		t.Errorf("one revoke expected, got %d: %s", fk.count("UnsubscribeOrgFromAgent"), out)
	}
}

// A lost answer, an internal error and a null payload are an UNKNOWN outcome:
// exit unavailable, one request, no retry, and a check-first hint.
func TestSubscriptionWriteUnknownOutcomeNoReplay2788(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd   []string
		op    string
		reply grants2788Reply
	}{
		"add dropped":     {[]string{"add"}, "SubscribeOrgToAgent", grants2788Reply{drop: true}},
		"add null":        {[]string{"add"}, "SubscribeOrgToAgent", grants2788Reply{body: `{"data":{"createAgentOrgGrant":null}}`}},
		"add internal":    {[]string{"add"}, "SubscribeOrgToAgent", refusal2788("INTERNAL_SERVER_ERROR")},
		"revoke dropped":  {[]string{"revoke", "--yes"}, "UnsubscribeOrgFromAgent", grants2788Reply{drop: true}},
		"revoke null":     {[]string{"revoke", "--yes"}, "UnsubscribeOrgFromAgent", grants2788Reply{body: `{"data":{"revokeAgentOrgGrant":null}}`}},
		"revoke internal": {[]string{"revoke", "--yes"}, "UnsubscribeOrgFromAgent", refusal2788("INTERNAL_SERVER_ERROR")},
	} {
		t.Run(name, func(t *testing.T) {
			fk, _, err := grants2788Run(t, map[string][]grants2788Reply{tc.op: {tc.reply}}, append(tc.cmd, "--org", "o", "--agent", "a")...)
			if err == nil || exitCodeFor(err) != exitcode.Unavailable {
				t.Fatalf("exit %d expected, got %v", exitcode.Unavailable, err)
			}
			if fk.count(tc.op) != 1 {
				t.Errorf("the write must be sent once, got %d", fk.count(tc.op))
			}
			if !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), "subscription list --org") {
				t.Errorf("missing check-first guidance: %v", err)
			}
		})
	}
}

// A coded refusal is a definite "did not take effect" and maps to its normal
// exit, never to the unknown-outcome message.
func TestSubscriptionWriteRefusalIsDefinite2788(t *testing.T) {
	_, _, err := grants2788Run(t, map[string][]grants2788Reply{
		"SubscribeOrgToAgent": {refusal2788("FORBIDDEN")},
	}, "add", "--org", "o", "--agent", "a")
	if err == nil || strings.Contains(err.Error(), "outcome unknown") {
		t.Fatalf("a coded refusal must not read as unknown: %v", err)
	}
}

func TestSubscriptionListSelectorsAndFlagRules2788(t *testing.T) {
	for name, args := range map[string][]string{
		"none":              {"list"},
		"two selectors":     {"list", "--org", "o", "--mine"},
		"org with limit":    {"list", "--org", "o", "--limit", "5"},
		"all with limit":    {"list", "--mine", "--all", "--limit", "5"},
		"zero limit":        {"list", "--mine", "--limit", "0"},
		"negative offset":   {"list", "--mine", "--offset", "-1"},
		"agent and agent 2": {"list", "--agent", "a", "--mine"},
	} {
		t.Run(name, func(t *testing.T) {
			fk, _, err := grants2788Run(t, map[string][]grants2788Reply{}, args...)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit %d expected, got %v", exitcode.Usage, err)
			}
			if len(fk.calls) != 0 {
				t.Errorf("a request was sent for a usage error: %v", fk.calls)
			}
		})
	}
}

// One page is labelled as one page; --all follows to the end; the JSON says
// whether it is complete.
func TestSubscriptionPagedReadsSemantics2788(t *testing.T) {
	type pg struct {
		Items    []map[string]any `json:"items"`
		Total    int              `json:"total"`
		HasMore  bool             `json:"hasMore"`
		Complete bool             `json:"complete"`
	}
	for _, mode := range []struct{ name, op, field string }{
		{"agent", "AgentSubscriptionsPage", "agentSubscriptionsPage"},
		{"mine", "MyAgentSubscriptionsPage", "myAgentSubscriptionsPage"},
	} {
		sel := []string{"--agent", "ag1"}
		if mode.name == "mine" {
			sel = []string{"--mine"}
		}
		t.Run(mode.name+" one page", func(t *testing.T) {
			fk, out, err := grants2788Run(t, map[string][]grants2788Reply{mode.op: {page2788(mode.field, 3, true, sub2788(1), sub2788(2))}},
				append(append([]string{"list"}, sel...), "--limit", "2", "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var got pg
			if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
				t.Fatal(jerr, out)
			}
			if len(got.Items) != 2 || got.Total != 3 || !got.HasMore || got.Complete {
				t.Errorf("a truncated page must say it is incomplete: %+v", got)
			}
			if fk.count(mode.op) != 1 || fk.vars[mode.op][0]["limit"] != float64(2) {
				t.Errorf("one request with limit 2 expected, got %v", fk.vars[mode.op])
			}
			if _, has := fk.vars[mode.op][0]["offset"]; has {
				t.Errorf("an unset --offset must be omitted, got %v", fk.vars[mode.op][0])
			}
		})
		t.Run(mode.name+" human page note", func(t *testing.T) {
			_, out, err := grants2788Run(t, map[string][]grants2788Reply{mode.op: {page2788(mode.field, 3, true, sub2788(1))}},
				append([]string{"list"}, sel...)...)
			if err != nil || !strings.Contains(out, "NOT the whole set") || !strings.Contains(out, "--all") {
				t.Errorf("a partial page must say so: %v\n%s", err, out)
			}
		})
		t.Run(mode.name+" all", func(t *testing.T) {
			fk, out, err := grants2788Run(t, map[string][]grants2788Reply{mode.op: {
				page2788(mode.field, 3, true, sub2788(1), sub2788(2)),
				page2788(mode.field, 3, false, sub2788(3)),
			}}, append(append([]string{"list"}, sel...), "--all", "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var got pg
			if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
				t.Fatal(jerr, out)
			}
			if len(got.Items) != 3 || !got.Complete || got.HasMore {
				t.Errorf("--all must return the whole set: %+v", got)
			}
			if fk.count(mode.op) != 2 || fk.vars[mode.op][1]["offset"] != float64(2) {
				t.Errorf("the second page must start at offset 2: %v", fk.vars[mode.op])
			}
		})
		t.Run(mode.name+" all with an empty page that claims more", func(t *testing.T) {
			_, _, err := grants2788Run(t, map[string][]grants2788Reply{mode.op: {
				page2788(mode.field, 5, true, sub2788(1)),
				page2788(mode.field, 5, true),
			}}, append(append([]string{"list"}, sel...), "--all")...)
			if err == nil || !strings.Contains(err.Error(), "incomplete") {
				t.Errorf("a stuck server must not pass as a complete list: %v", err)
			}
		})
	}
}

// The personal read never claims emptiness proves absence.
func TestSubscriptionMineEmptyIsNotProof2788(t *testing.T) {
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{"MyAgentSubscriptionsPage": {page2788("myAgentSubscriptionsPage", 0, false)}}, "list", "--mine")
	if err != nil || !strings.Contains(out, "does not prove") {
		t.Errorf("an empty personal list must carry the caveat: %v\n%s", err, out)
	}
}

// The existing --org read keeps its exact array shape.
func TestSubscriptionListOrgUnchanged2788(t *testing.T) {
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{"OrgAgentGrants": {{body: `{"data":{"organization":{"agentOrgGrants":[]}}}`}}}, "ls", "--org", "o", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("--org --json must stay an array: %v %q", err, out)
	}
}

// BLIND P2 #1 (cli#2788 round 1): --all must not trust a server that ignores
// the offset. Page 1 repeated would grow the list past the server's own total.
func TestSubscriptionAllRefusesAServerThatIgnoresTheOffset2788(t *testing.T) {
	for _, mode := range []struct{ name, op, field string }{
		{"agent", "AgentSubscriptionsPage", "agentSubscriptionsPage"},
		{"mine", "MyAgentSubscriptionsPage", "myAgentSubscriptionsPage"},
	} {
		sel := []string{"--agent", "ag1"}
		if mode.name == "mine" {
			sel = []string{"--mine"}
		}
		t.Run(mode.name, func(t *testing.T) {
			same := page2788(mode.field, 3, true, sub2788(1), sub2788(2))
			fk, out, err := grants2788Run(t, map[string][]grants2788Reply{mode.op: {same, same, same, same, same}},
				append(append([]string{"list"}, sel...), "--all", "--json")...)
			if err == nil || !strings.Contains(err.Error(), "more rows than") || exitCodeFor(err) != exitcode.Error {
				t.Fatalf("a server repeating page 1 must fail with exit %d and say so: %v", exitcode.Error, err)
			}
			if fk.count(mode.op) > 3 {
				t.Errorf("the walk must stop as soon as it passes the total, made %d requests", fk.count(mode.op))
			}
			if strings.Contains(out, `"complete": true`) || strings.Contains(out, `"complete":true`) {
				t.Errorf("never complete:true on this path: %s", out)
			}
		})
	}
}

// --all ends with complete:true only when it holds exactly the rows the server
// says there are.
func TestSubscriptionAllNeedsLenEqualToTotal2788(t *testing.T) {
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{"MyAgentSubscriptionsPage": {
		page2788("myAgentSubscriptionsPage", 5, true, sub2788(1), sub2788(2)),
		page2788("myAgentSubscriptionsPage", 5, false, sub2788(3)),
	}}, "list", "--mine", "--all", "--json")
	if err == nil || !strings.Contains(err.Error(), "inconsistent") || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("3 rows against a total of 5 must not pass as complete: %v", err)
	}
	if strings.Contains(out, `"complete": true`) || strings.Contains(out, `"complete":true`) {
		t.Errorf("never complete:true here: %s", out)
	}
}

// BLIND P2 #2: a page past the end is not "none visible", and a last page has
// nothing to page on to.
func TestSubscriptionEmptyPastTheEndAndLastPageWording2788(t *testing.T) {
	op, field := "MyAgentSubscriptionsPage", "myAgentSubscriptionsPage"
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{op: {page2788(field, 10, false)}}, "list", "--mine", "--offset", "50")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No subscriptions are visible") || strings.Contains(out, "Page on") || !strings.Contains(out, "offset 50") || !strings.Contains(out, "10") {
		t.Errorf("past the end: say the page is empty at that offset, no 'none visible', no page-on hint:\n%s", out)
	}
	_, out, err = grants2788Run(t, map[string][]grants2788Reply{op: {page2788(field, 3, false, sub2788(3))}}, "list", "--mine", "--offset", "2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Page on") || !strings.Contains(out, "NOT the whole set") || !strings.Contains(out, "--offset 0") {
		t.Errorf("a last page at offset>0 is not the whole set but has nothing to page on to; point at --offset 0/--all:\n%s", out)
	}
	_, out, err = grants2788Run(t, map[string][]grants2788Reply{op: {page2788(field, 0, false)}}, "list", "--mine")
	if err != nil || !strings.Contains(out, "No subscriptions are visible") {
		t.Errorf("a truly empty first page still says none visible: %v\n%s", err, out)
	}
}

// BLIND P3 #3: every row field is mapped from its own server field.
func TestSubscriptionRowFieldsMapFromTheirOwnFields2788(t *testing.T) {
	row := `{"agentId":"AG","userId":"US","isActive":true,"createdAt":"CR","activatedAt":"AC","expiresAt":"EX","revokedAt":"RV"}`
	for _, mode := range []struct{ name, op, field string }{
		{"agent", "AgentSubscriptionsPage", "agentSubscriptionsPage"},
		{"mine", "MyAgentSubscriptionsPage", "myAgentSubscriptionsPage"},
	} {
		sel := []string{"--agent", "ag1"}
		if mode.name == "mine" {
			sel = []string{"--mine"}
		}
		t.Run(mode.name, func(t *testing.T) {
			_, out, err := grants2788Run(t, map[string][]grants2788Reply{mode.op: {page2788(mode.field, 1, false, row)}},
				append(append([]string{"list"}, sel...), "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Items []map[string]any `json:"items"`
			}
			if jerr := json.Unmarshal([]byte(out), &got); jerr != nil || len(got.Items) != 1 {
				t.Fatal(jerr, out)
			}
			for k, want := range map[string]any{"agentId": "AG", "userId": "US", "isActive": true, "createdAt": "CR", "activatedAt": "AC", "expiresAt": "EX", "revokedAt": "RV"} {
				if got.Items[0][k] != want {
					t.Errorf("%s = %v, want %v", k, got.Items[0][k], want)
				}
			}
		})
	}
}

// BLIND P3 #4: a coded refusal on either write maps to its exit, one request.
func TestSubscriptionWriteRefusalExitAndSingleRequest2788(t *testing.T) {
	for _, c := range []struct {
		verb, op string
		args     []string
	}{
		{"add", "SubscribeOrgToAgent", []string{"add", "--org", "o", "--agent", "a"}},
		{"revoke", "UnsubscribeOrgFromAgent", []string{"revoke", "--org", "o", "--agent", "a", "--yes"}},
	} {
		t.Run(c.verb, func(t *testing.T) {
			fk, _, err := grants2788Run(t, map[string][]grants2788Reply{c.op: {refusal2788("FORBIDDEN")}}, c.args...)
			if err == nil || strings.Contains(err.Error(), "outcome unknown") || exitCodeFor(err) != exitcode.Forbidden {
				t.Fatalf("exit %d and no unknown-outcome text expected: %v", exitcode.Forbidden, err)
			}
			if fk.count(c.op) != 1 {
				t.Errorf("exactly one request expected, got %d", fk.count(c.op))
			}
		})
	}
}

// A single page that ends the set (hasMore:false, offset 0) is complete only if
// it holds the rows the server says there are.
func TestSubscriptionSinglePageCompleteNeedsLenEqualToTotal2788(t *testing.T) {
	_, out, err := grants2788Run(t, map[string][]grants2788Reply{"MyAgentSubscriptionsPage": {
		page2788("myAgentSubscriptionsPage", 5, false, sub2788(1), sub2788(2)),
	}}, "list", "--mine", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"complete": true`) || strings.Contains(out, `"complete":true`) {
		t.Errorf("2 rows against a total of 5 must not be complete: %s", out)
	}
}

func refusalAt2788(code string, path ...string) grants2788Reply {
	ps := make([]string, len(path))
	for i, p := range path {
		ps[i] = `"` + p + `"`
	}
	return grants2788Reply{body: `{"errors":[{"message":"refused","path":[` + strings.Join(ps, ",") + `],"extensions":{"code":"` + code + `"}}],"data":null}`}
}

// LOCAL P2 (Jill, cli#2788 round 1): a coded error BELOW the mutation field came
// from resolving the returned object after the write committed, so the outcome is
// unknown (exit 7), however well coded. Only a refusal with no path or exactly
// [field] is definite.
func TestSubscriptionWriteErrorBelowTheFieldIsUnknownOutcome2788(t *testing.T) {
	for _, c := range []struct {
		verb, op, field string
		args            []string
	}{
		{"add", "SubscribeOrgToAgent", "createAgentOrgGrant", []string{"add", "--org", "o", "--agent", "a"}},
		{"revoke", "UnsubscribeOrgFromAgent", "revokeAgentOrgGrant", []string{"revoke", "--org", "o", "--agent", "a", "--yes"}},
	} {
		below := refusalAt2788("FORBIDDEN", c.field, "agentId")
		mixed := grants2788Reply{body: `{"errors":[{"message":"refused","path":["` + c.field + `"],"extensions":{"code":"FORBIDDEN"}},{"message":"x","path":["` + c.field + `","agentId"],"extensions":{"code":"NOT_FOUND"}}],"data":null}`}
		for name, reply := range map[string]grants2788Reply{"below the field": below, "at and below": mixed} {
			t.Run(c.verb+" "+name, func(t *testing.T) {
				fk, _, err := grants2788Run(t, map[string][]grants2788Reply{c.op: {reply}}, c.args...)
				if err == nil || !strings.Contains(err.Error(), "outcome unknown") || exitCodeFor(err) != exitcode.Unavailable {
					t.Fatalf("exit %d and an unknown-outcome message expected: %v", exitcode.Unavailable, err)
				}
				if fk.count(c.op) != 1 {
					t.Errorf("exactly one request expected, got %d", fk.count(c.op))
				}
			})
		}
		t.Run(c.verb+" at the field is definite", func(t *testing.T) {
			_, _, err := grants2788Run(t, map[string][]grants2788Reply{c.op: {refusalAt2788("FORBIDDEN", c.field)}}, c.args...)
			if err == nil || strings.Contains(err.Error(), "outcome unknown") || exitCodeFor(err) != exitcode.Forbidden {
				t.Fatalf("a refusal at exactly [%s] is definite, exit %d: %v", c.field, exitcode.Forbidden, err)
			}
		})
	}
}

// LOCAL P3 (Jill): an explicit --offset 0 is sent; an unset one is omitted.
func TestSubscriptionExplicitOffsetZeroIsSent2788(t *testing.T) {
	fk, _, err := grants2788Run(t, map[string][]grants2788Reply{"MyAgentSubscriptionsPage": {page2788("myAgentSubscriptionsPage", 0, false)}}, "list", "--mine", "--offset", "0")
	if err != nil {
		t.Fatal(err)
	}
	if v, has := fk.vars["MyAgentSubscriptionsPage"][0]["offset"]; !has || v != float64(0) {
		t.Errorf("an explicit --offset 0 must be sent, got %v", fk.vars["MyAgentSubscriptionsPage"][0])
	}
}
