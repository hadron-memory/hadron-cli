package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2320 (follow-up to #2271's node move --dry-run, cor:api:190:03 and
// cor:api:240:01).

// Item 1: the --json fields agree. `truncated` is the CLI's verdict (the
// server flagged the plan OR it lists fewer nodes than it counts), so it is
// always !complete and never reads complete beside status TRUNCATED. A plan is
// complete only when the server does not flag it and the list matches the
// count, so a list longer than totalCount reads TRUNCATED too (cli#2358).
func TestNodeMovePlan2320JSONFieldsCoherent(t *testing.T) {
	for name, tc := range map[string]struct {
		plan     map[string]any
		complete bool
	}{
		"count short, flag false": {plan741(0, 3, false, []any{}, false), false},
		"count short by one":      {plan741(2, 3, false, []any{}, false), false},
		"flag only":               {plan741(3, 3, true, []any{}, false), false},
		"both signals":            {plan741(1, 3, true, []any{}, false), false},
		"list longer than count":  {plan741(3, 2, false, []any{}, false), false},
		"complete":                {plan741(3, 3, false, []any{}, false), true},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := run741(t, &planFake741{plan: tc.plan}, "--to-urn", "hrn:node:acme.com:archive:x", "--json")
			want := 0
			if !tc.complete {
				want = exitcode.Error
			}
			if got := exitCodeFor(err); got != want {
				t.Fatalf("exit = %d, want %d (%v)", got, want, err)
			}
			var doc struct {
				Status    string `json:"status"`
				Complete  *bool  `json:"complete"`
				Truncated *bool  `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("not one JSON document: %v\n%s", err, out)
			}
			if doc.Complete == nil || doc.Truncated == nil {
				t.Fatalf("complete and truncated must both be present and non-null:\n%s", out)
			}
			wantStatus := "TRUNCATED"
			if tc.complete {
				wantStatus = "COMPLETE"
			}
			if *doc.Complete != tc.complete || *doc.Truncated != !tc.complete || doc.Status != wantStatus {
				t.Fatalf("incoherent plan fields: status=%s complete=%v truncated=%v (want complete=%v)\n%s",
					doc.Status, *doc.Complete, *doc.Truncated, tc.complete, out)
			}
		})
	}
}

// Item 2: --limit above the GraphQL Int range is refused locally, exit 2, with
// no request; the refusal names the type range, never a server limit.
func TestNodeMovePlan2320LimitAboveIntRefusedLocally(t *testing.T) {
	for _, l := range []string{"2147483648", "9223372036854775807"} {
		t.Run(l, func(t *testing.T) {
			p := &planFake741{plan: plan741(1, 1, false, []any{}, false)}
			_, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x", "--limit="+l)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want Usage (%v)", got, err)
			}
			if len(p.ops) != 0 {
				t.Fatalf("an out-of-range --limit must be refused before any request, sent %v", p.ops)
			}
			msg := err.Error()
			if !strings.Contains(msg, "2147483647") || !strings.Contains(msg, "GraphQL Int") || strings.Contains(msg, "server") {
				t.Fatalf("the refusal names the GraphQL Int range and no server limit: %q", msg)
			}
		})
	}
}

// Item 2: the largest GraphQL Int is still sent unchanged for the server to bound.
func TestNodeMovePlan2320LimitAtIntMaxIsSent(t *testing.T) {
	p := &planFake741{plan: plan741(1, 1, false, []any{}, false)}
	if _, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x", "--limit=2147483647"); err != nil {
		t.Fatal(err)
	}
	v := onlyPlan741(t, p)
	if got, ok := v["limit"].(float64); !ok || got != 2147483647 {
		t.Fatalf("limit sent = %v, want 2147483647", v["limit"])
	}
}
