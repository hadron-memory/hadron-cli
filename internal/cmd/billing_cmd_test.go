package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#215 — the spec-046 metering READS (aiModelPrices, creditBalance,
// creditLedgerEntries). Money is a base-10 integer string of micro-cents on
// the wire and must stay one end to end: these fixtures use values past 2^53
// (9007199254740993 is the first integer a float64 cannot hold) and signed
// debits, so a float or int64 parse anywhere would show up as a changed byte.

const bigMicroCents = "9007199254740993"

const aiModelPriceJSON = `{"id":"p1","provider":"anthropic","model":"claude-opus-5-5","tier":2,
	"inputMicroCentsPerMTok":"` + bigMicroCents + `","outputMicroCentsPerMTok":"1500000000",
	"markupBasisPoints":null,"effectiveAt":"2026-07-11T00:00:00.000Z",
	"createdAt":"2026-07-10T00:00:00.000Z","createdBy":null}`

const ledgerDebitJSON = `{"id":"l1","organizationId":"org1","kind":"DEBIT",
	"amountMicroCents":"-` + bigMicroCents + `","expiresAt":null,"sourceType":"usage_event",
	"sourceId":"ue1","note":null,"createdAt":"2026-07-12T00:00:00.000Z","createdBy":null}`

const ledgerGrantJSON = `{"id":"l2","organizationId":"org1","kind":"GRANT",
	"amountMicroCents":"` + bigMicroCents + `","expiresAt":"2027-01-01T00:00:00.000Z","sourceType":null,
	"sourceId":null,"note":"trial","createdAt":"2026-07-11T00:00:00.000Z","createdBy":"u1"}`

func runBilling(t *testing.T, responses map[string]string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

func billingVars(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	vars := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &vars); err != nil {
			t.Fatalf("variables: %v", err)
		}
	}
	return vars
}

func TestBillingPricesForwardsFiltersAndOnePage(t *testing.T) {
	out, captured, err := runBilling(t, map[string]string{
		"AiModelPrices": `{"data":{"aiModelPrices":{"total":40,"items":[` + aiModelPriceJSON + `]}}}`,
	}, "billing", "prices", "--provider", "anthropic", "--model", "claude-opus-5-5", "--tier", "2",
		"--limit", "5", "--offset", "10", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["AiModelPrices"])
	want := map[string]any{"provider": "anthropic", "model": "claude-opus-5-5", "tier": float64(2), "limit": float64(5), "offset": float64(10)}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s: want %v, got %v (%v)", k, v, vars[k], vars)
		}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("--json not an array: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("an explicit --limit is one page, got %d rows", len(rows))
	}
	if got, ok := rows[0]["inputMicroCentsPerMTok"].(string); !ok || got != bigMicroCents {
		t.Errorf("money must stay a byte-identical string, got %#v", rows[0]["inputMicroCentsPerMTok"])
	}
	if v, present := rows[0]["markupBasisPoints"]; !present || v != nil {
		t.Errorf("an unknown markup stays null (not 0, not absent), got %#v present=%v", v, present)
	}
	if rows[0]["tier"] != float64(2) {
		t.Errorf("tier: %v", rows[0]["tier"])
	}
}

func TestBillingPricesUnfilteredOmitsFilters(t *testing.T) {
	out, captured, err := runBilling(t, map[string]string{
		"AiModelPrices": `{"data":{"aiModelPrices":{"total":1,"items":[` + aiModelPriceJSON + `]}}}`,
	}, "billing", "prices")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["AiModelPrices"])
	for _, k := range []string{"provider", "model", "tier"} {
		if v, present := vars[k]; present {
			t.Errorf("unset --%s must be omitted, got %v", k, v)
		}
	}
	if !strings.Contains(out, bigMicroCents) || !strings.Contains(out, "1500000000") {
		t.Errorf("table must print the micro-cents strings verbatim:\n%s", out)
	}
}

func TestBillingPricesRefusesEmptyFilters(t *testing.T) {
	for _, flag := range []string{"--provider=", "--model="} {
		_, captured, err := runBilling(t, map[string]string{}, "billing", "prices", flag)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%s must exit %d (an empty filter is not an absent one), got %d (%v)", flag, exitcode.Usage, got, err)
		}
		if _, sent := captured["AiModelPrices"]; sent {
			t.Errorf("%s must refuse before any request", flag)
		}
	}
}

func TestBillingRefusesNegativePaging(t *testing.T) {
	for _, args := range [][]string{
		{"billing", "prices", "--limit", "-1"},
		{"billing", "ledger", "org1", "--offset", "-1"},
	} {
		_, _, err := runBilling(t, map[string]string{}, args...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v must exit %d, got %d (%v)", args, exitcode.Usage, got, err)
		}
	}
}

func TestBillingBalance(t *testing.T) {
	resp := map[string]string{
		"CreditBalance": `{"data":{"creditBalance":{"organizationId":"org1","balanceMicroCents":"-` + bigMicroCents + `"}}}`,
	}
	out, captured, err := runBilling(t, resp, "billing", "balance", "hrn:org:acme.com", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := billingVars(t, captured["CreditBalance"])["orgRef"]; got != "hrn:org:acme.com" {
		t.Errorf("orgRef must be forwarded verbatim, got %v", got)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if got, ok := dto["balanceMicroCents"].(string); !ok || got != "-"+bigMicroCents {
		t.Errorf("a negative balance past 2^53 must survive as a string, got %#v", dto["balanceMicroCents"])
	}
	if dto["organizationId"] != "org1" {
		t.Errorf("organizationId: %v", dto)
	}

	out, _, err = runBilling(t, resp, "billing", "balance", "org1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "-"+bigMicroCents) || !strings.Contains(out, "micro-cents") {
		t.Errorf("human output must print the exact signed micro-cents, labelled:\n%s", out)
	}
}

func TestBillingLedger(t *testing.T) {
	resp := map[string]string{
		"CreditLedgerEntries": `{"data":{"creditLedgerEntries":{"total":2,"items":[` + ledgerDebitJSON + `,` + ledgerGrantJSON + `]}}}`,
	}
	out, captured, err := runBilling(t, resp, "billing", "ledger", "hrn:org:acme.com", "--limit", "2", "--offset", "4", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["CreditLedgerEntries"])
	if vars["orgRef"] != "hrn:org:acme.com" || vars["limit"] != float64(2) || vars["offset"] != float64(4) {
		t.Errorf("org/page args must be forwarded, got %v", vars)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("--json not an array: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if got, ok := rows[0]["amountMicroCents"].(string); !ok || got != "-"+bigMicroCents {
		t.Errorf("a signed debit must stay a byte-identical string, got %#v", rows[0]["amountMicroCents"])
	}
	for _, k := range []string{"expiresAt", "note", "createdBy"} {
		if v, present := rows[0][k]; !present || v != nil {
			t.Errorf("%s: unknown stays null, got %#v present=%v", k, v, present)
		}
	}
	if rows[1]["note"] != "trial" || rows[1]["expiresAt"] != "2027-01-01T00:00:00.000Z" {
		t.Errorf("grant row: %v", rows[1])
	}

	out, _, err = runBilling(t, resp, "billing", "ledger", "org1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "-"+bigMicroCents) || !strings.Contains(out, bigMicroCents) {
		t.Errorf("table must print the signed amounts verbatim:\n%s", out)
	}
}

func TestBillingOrgAdminRefusalPassesThrough(t *testing.T) {
	forbidden := `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`
	for _, tc := range []struct {
		op   string
		args []string
	}{
		{"CreditBalance", []string{"billing", "balance", "hrn:org:acme.com"}},
		{"CreditLedgerEntries", []string{"billing", "ledger", "hrn:org:acme.com"}},
	} {
		calls := 0
		gql, _ := captureGraphQLFunc(t, func(op string) string {
			if op == tc.op {
				calls++
				return forbidden
			}
			return ""
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(tc.args, "--server", gql.URL))
		err := root.Execute()
		if got := exitCodeFor(err); got != exitcode.Forbidden {
			t.Errorf("%s: the org-ADMIN refusal must exit %d, got %d (%v)", tc.op, exitcode.Forbidden, got, err)
		}
		if err == nil || !strings.Contains(err.Error(), "Forbidden") {
			t.Errorf("%s: the server's message must survive: %v", tc.op, err)
		}
		if calls != 1 {
			t.Errorf("%s: sent %d times, want exactly once", tc.op, calls)
		}
	}
}
