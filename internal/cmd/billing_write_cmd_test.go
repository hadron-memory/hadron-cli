package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#215 write cut — `billing prices set` (setAiModelPrice) and `billing
// grant` (grantCredits). Both are platform-ADMIN writes against the money
// ledger, so every test runs on synthetic transport only. The pins: money goes
// to the wire as the opaque string the user typed (no parse, no positivity rule
// of the CLI's own — the server owns the domain), confirmation precedes any
// request, a refusal passes through, an omitted optional is not sent, and a
// grant never claims whether it was a first grant or a replay (the response has
// no discriminator).

const setPriceResponse = `{"data":{"setAiModelPrice":` + aiModelPriceJSON + `}}`

const grantResponse = `{"data":{"grantCredits":` + ledgerGrantJSON + `}}`

func billingInputOf(t *testing.T, captured map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		t.Fatalf("%s was never sent", op)
	}
	in, _ := billingVars(t, raw)["input"].(map[string]any)
	if in == nil {
		t.Fatalf("%s carried no input object: %s", op, raw)
	}
	return in
}

func TestBillingPricesSetSendsOpaqueMoneyAndOmitsUnsetOptionals(t *testing.T) {
	_, captured, err := runBilling(t, map[string]string{"SetAiModelPrice": setPriceResponse},
		"billing", "prices", "set", "--provider", "anthropic", "--model", "claude-opus-5-5",
		"--input", bigMicroCents, "--output", "0007", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := billingInputOf(t, captured, "SetAiModelPrice")
	if in["inputMicroCentsPerMTok"] != bigMicroCents || in["outputMicroCentsPerMTok"] != "0007" {
		t.Errorf("money must reach the wire as the typed string (>2^53 and leading zero intact): %v", in)
	}
	if in["provider"] != "anthropic" || in["model"] != "claude-opus-5-5" {
		t.Errorf("provider/model: %v", in)
	}
	for _, k := range []string{"tier", "markupBasisPoints", "effectiveAt"} {
		if _, present := in[k]; present {
			t.Errorf("unset %s must be omitted so the server default applies, got %v", k, in[k])
		}
	}
}

func TestBillingPricesSetForwardsOptionals(t *testing.T) {
	_, captured, err := runBilling(t, map[string]string{"SetAiModelPrice": setPriceResponse},
		"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "1", "--output", "2",
		"--tier", "3", "--markup-bp", "0", "--effective-at", "2026-12-01T00:00:00Z", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := billingInputOf(t, captured, "SetAiModelPrice")
	if in["tier"] != float64(3) || in["markupBasisPoints"] != float64(0) || in["effectiveAt"] != "2026-12-01T00:00:00Z" {
		t.Errorf("an explicit 0 markup is a value, not unset; got %v", in)
	}
}

func TestBillingWritesRefuseWithoutYesAndSendNothing(t *testing.T) {
	for _, args := range [][]string{
		{"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "1", "--output", "2"},
		{"billing", "grant", "hrn:org:acme.com", "--amount", "5"},
	} {
		_, captured, err := runBilling(t, map[string]string{"SetAiModelPrice": setPriceResponse, "GrantCredits": grantResponse}, args...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v: non-interactive without --yes must exit %d, got %d (%v)", args[:3], exitcode.Usage, got, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: nothing may be sent before confirmation, saw %v", args[:3], captured)
		}
	}
}

func TestBillingPricesSetJSONKeepsMoneyAString(t *testing.T) {
	out, _, err := runBilling(t, map[string]string{"SetAiModelPrice": setPriceResponse},
		"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "1", "--output", "2", "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if row["inputMicroCentsPerMTok"] != bigMicroCents {
		t.Errorf("money must stay the server's string: %#v", row["inputMicroCentsPerMTok"])
	}
	if v, present := row["markupBasisPoints"]; !present || v != nil {
		t.Errorf("an unknown markup stays null: %#v", v)
	}
}

func TestBillingPricesSetRequiresItsFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "m", "--input", "1", "--output", "2"},
		{"--provider", "p", "--input", "1", "--output", "2"},
		{"--provider", "p", "--model", "m", "--output", "2"},
		{"--provider", "p", "--model", "m", "--input", "1"},
		{"--provider", "", "--model", "m", "--input", "1", "--output", "2"},
		{"--provider", "p", "--model", "m", "--input", "1", "--output", "2", "--effective-at", ""},
	} {
		_, captured, err := runBilling(t, map[string]string{"SetAiModelPrice": setPriceResponse},
			append([]string{"billing", "prices", "set", "--yes"}, args...)...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v: want usage exit %d, got %d (%v)", args, exitcode.Usage, got, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: a malformed request must not be sent", args)
		}
	}
}

func TestBillingGrantSendsOpaqueAmountAndOmitsUnsetOptionals(t *testing.T) {
	for _, amount := range []string{bigMicroCents, "0000042", "0", "-5", "1.5"} {
		_, captured, err := runBilling(t, map[string]string{"GrantCredits": grantResponse},
			"billing", "grant", "hrn:org:acme.com", "--amount", amount, "--yes")
		if err != nil {
			t.Fatalf("%q: execute: %v", amount, err)
		}
		in := billingInputOf(t, captured, "GrantCredits")
		if in["amountMicroCents"] != amount {
			t.Errorf("%q must reach the server untouched (it owns the positivity rule), got %v", amount, in["amountMicroCents"])
		}
		if in["orgRef"] != "hrn:org:acme.com" {
			t.Errorf("orgRef: %v", in["orgRef"])
		}
		for _, k := range []string{"sourceType", "sourceId", "expiresAt", "note"} {
			if _, present := in[k]; present {
				t.Errorf("unset %s must be omitted: %v", k, in)
			}
		}
	}
}

func TestBillingGrantForwardsOptionalsAndPair(t *testing.T) {
	_, captured, err := runBilling(t, map[string]string{"GrantCredits": grantResponse},
		"billing", "grant", "acme", "--amount", "5", "--source-type", "stripe-checkout", "--source-id", "cs_1",
		"--expires-at", "2027-01-01T00:00:00Z", "--note", "trial", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := billingInputOf(t, captured, "GrantCredits")
	if in["sourceType"] != "stripe-checkout" || in["sourceId"] != "cs_1" || in["expiresAt"] != "2027-01-01T00:00:00Z" || in["note"] != "trial" {
		t.Errorf("optionals: %v", in)
	}
}

func TestBillingGrantRefusesHalfAnIdempotencyPairBeforeAnyRequest(t *testing.T) {
	for _, extra := range [][]string{{"--source-type", "t"}, {"--source-id", "i"}, {"--source-type", "", "--source-id", "i"}} {
		_, captured, err := runBilling(t, map[string]string{"GrantCredits": grantResponse},
			append([]string{"billing", "grant", "acme", "--amount", "5", "--yes"}, extra...)...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v: want usage exit, got %d (%v)", extra, got, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: nothing may be sent", extra)
		}
	}
}

func TestBillingGrantRequiresAmountAndOrg(t *testing.T) {
	for _, args := range [][]string{{"billing", "grant", "acme", "--yes"}, {"billing", "grant", "--amount", "5", "--yes"}, {"billing", "grant", "acme", "--amount", "", "--yes"}} {
		_, captured, err := runBilling(t, map[string]string{"GrantCredits": grantResponse}, args...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v: want usage exit, got %d (%v)", args, got, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: nothing may be sent", args)
		}
	}
}

// The response carries no created/reused flag, so the wording is the same for a
// first grant and a replay and never claims either (Xylon GAPS 6539950898).
func TestBillingGrantWordingIsNeutral(t *testing.T) {
	out, _, err := runBilling(t, map[string]string{"GrantCredits": grantResponse},
		"billing", "grant", "acme", "--amount", bigMicroCents, "--source-type", "t", "--source-id", "i", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "l2") || !strings.Contains(out, bigMicroCents) || !strings.Contains(out, "org1") {
		t.Errorf("must show the returned entry id, amount and org: %q", out)
	}
	for _, banned := range []string{"already", "existing", "again", "replay", "duplicate", "new "} {
		if strings.Contains(strings.ToLower(out), banned) {
			t.Errorf("the CLI cannot tell a first grant from a replay; output must not say %q: %q", banned, out)
		}
	}
}

func TestBillingGrantJSONKeepsMoneyAString(t *testing.T) {
	out, _, err := runBilling(t, map[string]string{"GrantCredits": grantResponse},
		"billing", "grant", "acme", "--amount", "5", "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if e["amountMicroCents"] != bigMicroCents || e["kind"] != "GRANT" {
		t.Errorf("stable entry DTO with string money: %v", e)
	}
}

func TestBillingWritesPlatformAdminRefusalPassesThroughWithoutRetry(t *testing.T) {
	forbidden := `{"errors":[{"message":"Forbidden: platform admin only","extensions":{"code":"FORBIDDEN"}}]}`
	for _, tc := range []struct {
		op   string
		args []string
	}{
		{"SetAiModelPrice", []string{"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "1", "--output", "2", "--yes"}},
		{"GrantCredits", []string{"billing", "grant", "acme", "--amount", "5", "--yes"}},
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
			t.Errorf("%s: want exit %d, got %d (%v)", tc.op, exitcode.Forbidden, got, err)
		}
		if err == nil || !strings.Contains(err.Error(), "platform admin only") {
			t.Errorf("%s: the server's message must survive: %v", tc.op, err)
		}
		if calls != 1 {
			t.Errorf("%s: a money write is never retried; sent %d times", tc.op, calls)
		}
	}
}

// cli#1560: a transport failure or 5xx on a money write is surfaced after
// exactly one request, and a server refusal of a zero amount (the CLI applies no
// positivity rule of its own) passes through as a usage error, also once.
func TestBillingWritesSendOnceOnServerFailureAndZeroAmountRefusal(t *testing.T) {
	badInput := `{"errors":[{"message":"amount must be positive","extensions":{"code":"BAD_USER_INPUT"}}]}`
	for _, tc := range []struct {
		name string
		op   string
		args []string
		resp string
		code int
	}{
		{"grant zero amount refused", "GrantCredits", []string{"billing", "grant", "acme", "--amount", "0", "--yes"}, badInput, exitcode.Usage},
		{"prices zero input refused", "SetAiModelPrice", []string{"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "0", "--output", "0", "--yes"}, badInput, exitcode.Usage},
		{"grant 5xx", "GrantCredits", []string{"billing", "grant", "acme", "--amount", "5", "--yes"}, "5xx", -1},
		{"prices 5xx", "SetAiModelPrice", []string{"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "1", "--output", "2", "--yes"}, "5xx", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"capabilities":[]}`))
					return
				}
				var body struct {
					OperationName string `json:"operationName"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.OperationName == tc.op {
					calls++
				}
				if tc.resp == "5xx" {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				_, _ = w.Write([]byte(tc.resp))
			}))
			t.Cleanup(srv.Close)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", srv.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("want an error")
			}
			if tc.code >= 0 && exitCodeFor(err) != tc.code {
				t.Errorf("want exit %d, got %d (%v)", tc.code, exitCodeFor(err), err)
			}
			if calls != 1 {
				t.Errorf("a money write is never retried; sent %d times", calls)
			}
		})
	}
}
