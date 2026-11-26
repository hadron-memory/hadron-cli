package cmd

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3071 (Billing B1): `billing spend` and `adjust`, the required source on
// `grant`, the expiring parts of `balance`, exit 5 for a replay conflict, and
// `user set-roles --role billing`.

const spendRowsJSON = `{"key":"HADRON_SERVER","events":3,"tokensIn":"` + bigMicroCents + `","tokensOut":"20",
	"costMicroCents":"` + bigMicroCents + `","unpricedEvents":1,"chargedMicroCents":"1250"},
	{"key":"","events":2,"tokensIn":"5","tokensOut":"6","costMicroCents":"0","unpricedEvents":2,"chargedMicroCents":"0"}`

func spendResponse(rows string, truncated string, total string) map[string]string {
	return map[string]string{"AiSpend": `{"data":{"aiSpend":{"from":"2026-11-01T00:00:00.000Z","to":"2026-12-01T00:00:00.000Z",
		"truncated":` + truncated + `,"totalGroups":` + total + `,"rows":[` + rows + `]}}}`}
}

func spendArgs(extra ...string) []string {
	return append([]string{"billing", "spend", "hrn:org:acme.com", "--from", "2026-11-01T00:00:00Z",
		"--to", "2026-12-01T00:00:00+00:00", "--group-by", "key-owner"}, extra...)
}

func TestBillingSpendJSONGolden(t *testing.T) {
	out, captured, err := runBilling(t, spendResponse(spendRowsJSON, "false", "2"), spendArgs("--json")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["AiSpend"])
	for k, v := range map[string]any{"orgRef": "hrn:org:acme.com", "from": "2026-11-01T00:00:00Z", "to": "2026-12-01T00:00:00+00:00", "groupBy": "KEY_OWNER"} {
		if vars[k] != v {
			t.Errorf("%s: want %v, got %v", k, v, vars[k])
		}
	}
	want := `{"from":"2026-11-01T00:00:00.000Z","to":"2026-12-01T00:00:00.000Z","groupBy":"key-owner","truncated":false,"totalGroups":2,"rows":[` +
		`{"key":"HADRON_SERVER","events":3,"tokensIn":"9007199254740993","tokensOut":"20","costMicroCents":"9007199254740993","unpricedEvents":1,"chargedMicroCents":"1250"},` +
		`{"key":"","events":2,"tokensIn":"5","tokensOut":"6","costMicroCents":"0","unpricedEvents":2,"chargedMicroCents":"0"}]}`
	if got := compactJSON3071(t, out); got != want {
		t.Errorf("golden --json\nwant %s\ngot  %s", want, got)
	}
}

// compactJSON3071 strips whitespace only: key order and every byte of a value
// stay, so a re-encoded number would show.
func compactJSON3071(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatalf("--json: %v\n%s", err, s)
	}
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			// A backslash escapes the next byte only, so an escaped backslash then a quote closes the string.
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
		}
		if !inString && strings.ContainsRune(" \n\t\r", rune(c)) {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// A cost is never described as owed or due: whole words only, so a key such as
// "residue" does not trip the check.
var owedOrDue3207 = regexp.MustCompile(`(?i)\b(?:owed|due)\b`)

func TestBillingSpendEmptyRowsIsAnEmptyArray(t *testing.T) {
	out, _, err := runBilling(t, spendResponse("", "false", "0"), spendArgs("--json")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(compactJSON3071(t, out), `"rows":[]`) {
		t.Errorf("rows must be [], got %s", out)
	}
}

func TestBillingSpendGroupByMapsEachSpelling(t *testing.T) {
	for flag, enum := range map[string]string{"day": "DAY", "app": "APP", "model": "MODEL", "key-owner": "KEY_OWNER"} {
		_, captured, err := runBilling(t, spendResponse("", "false", "0"), "billing", "spend", "o",
			"--from", "2026-11-01", "--to", "2026-12-01", "--group-by", flag)
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if got := billingVars(t, captured["AiSpend"])["groupBy"]; got != enum {
			t.Errorf("%s: want %s, got %v", flag, enum, got)
		}
	}
}

func TestBillingSpendTableKeepsCostAndChargedApart(t *testing.T) {
	out, _, err := runBilling(t, spendResponse(spendRowsJSON, "false", "2"), spendArgs()...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var header, priced, unpriced []string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "KEY"):
			header = strings.Fields(line)
		case strings.HasPrefix(line, "HADRON_SERVER"):
			priced = strings.Fields(line)
		case strings.HasPrefix(line, "(none)"):
			unpriced = strings.Fields(line)
		}
	}
	if !slices.Contains(header, "COST") || !slices.Contains(header, "CHARGED") || !slices.Contains(header, "UNPRICED") {
		t.Errorf("header must carry COST, UNPRICED and CHARGED: %v\n%s", header, out)
	}
	if want := []string{"HADRON_SERVER", "3", bigMicroCents, "20", bigMicroCents, "1", "1250"}; !slices.Equal(priced, want) {
		t.Errorf("priced row: want %v, got %v", want, priced)
	}
	// An all-unpriced row is never a cost of 0 (cor:dmo:170:04).
	if want := []string{"(none)", "2", "5", "6", "unpriced", "2", "0"}; !slices.Equal(unpriced, want) {
		t.Errorf("unpriced row: want %v, got %v", want, unpriced)
	}
	if m := owedOrDue3207.FindString(out); m != "" {
		t.Errorf("a cost is never what is %q:\n%s", m, out)
	}
}

func TestBillingSpendTruncatedSaysSoOnStderr(t *testing.T) {
	for _, extra := range [][]string{nil, {"--json"}} {
		gql, _ := captureGraphQL(t, spendResponse(spendRowsJSON, "true", "812"))
		f, out, errOut := testFactoryTTY(t, "")
		root := NewRootCmd(f)
		root.SetArgs(append(spendArgs(extra...), "--server", gql.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "812") || !strings.Contains(errOut.String(), "largest charge") {
			t.Errorf("%v: stderr must say the list is partial: %q", extra, errOut.String())
		}
		if extra != nil && !strings.Contains(compactJSON3071(t, out.String()), `"truncated":true`) {
			t.Errorf("truncated must pass through: %s", out.String())
		}
	}
}

func TestBillingSpendRefusesBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"--to", "2026-12-01", "--group-by", "day"},
		{"--from", "2026-11-01", "--group-by", "day"},
		{"--from", "2026-11-01", "--to", "2026-12-01"},
		{"--from", "2026-11-01", "--to", "2026-12-01", "--group-by", "week"},
		{"--from", "2026-11-01", "--to", "2026-12-01", "--group-by", "user"},
		{"--from", "2026-11-01", "--to", "2026-12-01", "--group-by", ""},
		{"--from", "2026-13-01", "--to", "2026-12-01", "--group-by", "day"},
		{"--from", "2026-02-30", "--to", "2026-12-01", "--group-by", "day"},
		{"--from", "yesterday", "--to", "2026-12-01", "--group-by", "day"},
		{"--from", "2026-11-01", "--to", "tomorrow", "--group-by", "day"},
		{"--from", "2026-11-01T00:00:00", "--to", "2026-12-01", "--group-by", "day"},
		{"--from", "", "--to", "2026-12-01", "--group-by", "day"},
	} {
		_, captured, err := runBilling(t, spendResponse("", "false", "0"), append([]string{"billing", "spend", "o"}, args...)...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v: want exit %d, got %d (%v)", args, exitcode.Usage, got, err)
		}
		if err != nil && strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%v: the refusal must be spend's own: %v", args, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: nothing may be sent", args)
		}
	}
	_, captured, err := runBilling(t, spendResponse("", "false", "0"), "billing", "spend", "", "--from", "2026-11-01", "--to", "2026-12-01", "--group-by", "day")
	if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
		t.Errorf("an empty organization must exit 2 and send nothing: %v %v", err, captured)
	}
}

func TestBillingSpendBareDateIsMidnightUTC(t *testing.T) {
	_, captured, err := runBilling(t, spendResponse("", "false", "0"), "billing", "spend", "o",
		"--from", "2026-11-01", "--to", "2026-12-01", "--group-by", "day")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["AiSpend"])
	if vars["from"] != "2026-11-01T00:00:00Z" || vars["to"] != "2026-12-01T00:00:00Z" {
		t.Errorf("a bare date is midnight UTC: %v", vars)
	}
}

const adjustEntryJSON = `{"id":"l3","organizationId":"org1","kind":"adjustment",
	"amountMicroCents":"-300","expiresAt":null,"sourceType":"refund",
	"sourceId":"r1","note":"n","createdAt":"2026-11-25T00:00:00.000Z","createdBy":"u1"}`

const adjustResponse = `{"data":{"adjustCredits":` + adjustEntryJSON + `}}`

func adjustArgs(extra ...string) []string {
	return append([]string{"billing", "adjust", "acme", "--amount", "-300", "--source-type", "refund", "--source-id", "r1", "--note", "n"}, extra...)
}

func TestBillingAdjustJSONGoldenAndAmountByteForByte(t *testing.T) {
	for _, amount := range [][]string{{"--amount", "-300"}, {"--amount=-300"}} {
		args := append([]string{"billing", "adjust", "acme", "--source-type", "refund", "--source-id", "r1", "--note", "n", "--yes", "--json"}, amount...)
		out, captured, err := runBilling(t, map[string]string{"AdjustCredits": adjustResponse}, args...)
		if err != nil {
			t.Fatalf("%v: %v", amount, err)
		}
		in := billingInputOf(t, captured, "AdjustCredits")
		for k, v := range map[string]any{"orgRef": "acme", "amountMicroCents": "-300", "sourceType": "refund", "sourceId": "r1", "note": "n"} {
			if in[k] != v {
				t.Errorf("%s: want %v, got %v", k, v, in[k])
			}
		}
		if len(in) != 5 {
			t.Errorf("exactly the five inputs are sent: %v", in)
		}
		want := `{"id":"l3","organizationId":"org1","kind":"adjustment","amountMicroCents":"-300","expiresAt":null,"sourceType":"refund","sourceId":"r1","note":"n","createdAt":"2026-11-25T00:00:00.000Z","createdBy":"u1"}`
		if got := compactJSON3071(t, out); got != want {
			t.Errorf("golden\nwant %s\ngot  %s", want, got)
		}
	}
}

func TestBillingAdjustRequiresEveryFlagNonEmpty(t *testing.T) {
	flags := map[string]string{"--amount": "-300", "--source-type": "refund", "--source-id": "r1", "--note": "n"}
	for missing := range flags {
		for _, empty := range []bool{false, true} {
			args := []string{"billing", "adjust", "acme", "--yes"}
			for k, v := range flags {
				if k != missing {
					args = append(args, k, v)
				} else if empty {
					args = append(args, k, "")
				}
			}
			_, captured, err := runBilling(t, map[string]string{"AdjustCredits": adjustResponse}, args...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("%s empty=%v: want exit %d, got %d (%v)", missing, empty, exitcode.Usage, got, err)
			}
			if err != nil && !strings.Contains(err.Error(), missing) {
				t.Errorf("the refusal must name %s: %v", missing, err)
			}
			if len(captured) != 0 {
				t.Errorf("%s empty=%v: nothing may be sent", missing, empty)
			}
		}
	}
}

func TestBillingAdjustNeedsConfirmation(t *testing.T) {
	_, captured, err := runBilling(t, map[string]string{"AdjustCredits": adjustResponse}, adjustArgs()...)
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("no terminal and no --yes: want exit %d, got %d (%v)", exitcode.Usage, got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the refusal must name --yes: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("nothing may be sent: %v", captured)
	}
	// Declined at the prompt: values whole (cli#1560), nothing sent.
	long := "-" + strings.Repeat("9", 1000)
	gql, sent := captureGraphQL(t, map[string]string{})
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"billing", "adjust", "acme", "--amount", long, "--source-type", "refund", "--source-id", "r1", "--note", "n", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Cancelled {
		t.Errorf("declined: want exit %d, got %d", exitcode.Cancelled, got)
	}
	if !strings.Contains(errOut.String(), long) || len(sent) != 0 {
		t.Errorf("the prompt shows the amount whole and a decline sends nothing: %v", sent)
	}
}

func TestBillingGrantRequiresTheSourcePair(t *testing.T) {
	_, captured, err := runBilling(t, map[string]string{"GrantCredits": grantResponse}, "billing", "grant", "acme", "--amount", "5", "--yes")
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("want exit %d, got %d (%v)", exitcode.Usage, got, err)
	}
	if len(captured) != 0 {
		t.Errorf("nothing may be sent: %v", captured)
	}
}

func TestBillingLedgerWriteRefusalsAreSentOnce(t *testing.T) {
	const conflict = "This source is already recorded on the ledger with a different account, amount or kind."
	grant := []string{"billing", "grant", "acme", "--amount", "5", "--source-type", "t", "--source-id", "i", "--yes"}
	for _, tc := range []struct {
		name, op, code, message string
		args                    []string
		exit                    int
	}{
		{"grant conflict", "GrantCredits", "IDEMPOTENCY_CONFLICT", conflict, grant, exitcode.Conflict},
		{"adjust conflict", "AdjustCredits", "IDEMPOTENCY_CONFLICT", conflict, adjustArgs("--yes"), exitcode.Conflict},
		{"adjust forbidden", "AdjustCredits", "FORBIDDEN", "Forbidden", adjustArgs("--yes"), exitcode.Forbidden},
		{"adjust zero", "AdjustCredits", "BAD_USER_INPUT", "amount must not be zero", adjustArgs("--yes"), exitcode.Usage},
		{"grant past expiry", "GrantCredits", "BAD_USER_INPUT", "expiresAt must be later than now.", append(slices.Clone(grant), "--expires-at", "2020-01-01T00:00:00Z"), exitcode.Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				if op == tc.op {
					calls++
					return `{"errors":[{"message":"` + tc.message + `","extensions":{"code":"` + tc.code + `"}}]}`
				}
				return ""
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(slices.Clone(tc.args), "--server", gql.URL))
			err := root.Execute()
			if got := exitCodeFor(err); got != tc.exit {
				t.Errorf("want exit %d, got %d (%v)", tc.exit, got, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Errorf("the server's sentence must survive: %v", err)
			}
			if calls != 1 {
				t.Errorf("a money write is sent once, got %d", calls)
			}
		})
	}
}

func TestBillingConflictJSONNamesTheServerCode(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"AdjustCredits": `{"errors":[{"message":"m","extensions":{"code":"IDEMPOTENCY_CONFLICT"}}]}`})
	f, stdout, errOut := testFactoryTTY(t, "")
	f.JSON = true
	root := NewRootCmd(f)
	root.SetArgs(adjustArgs("--yes", "--json", "--server", gql.URL))
	renderError(f, root.Execute())
	if out := stdout.String() + errOut.String(); !strings.Contains(out, `"IDEMPOTENCY_CONFLICT"`) {
		t.Errorf("the failure envelope must carry serverCode: %q", out)
	}
}

func TestBillingBalanceExpiringParts(t *testing.T) {
	balance := func(parts string) map[string]string {
		return map[string]string{"CreditBalance": `{"data":{"creditBalance":{"organizationId":"org1","balanceMicroCents":"7","expiring":[` + parts + `]}}}`}
	}
	out, _, err := runBilling(t, balance(""), "billing", "balance", "o", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `{"organizationId":"org1","balanceMicroCents":"7","expiring":[]}`; compactJSON3071(t, out) != want {
		t.Errorf("want %s, got %s", want, out)
	}
	two := `{"amountMicroCents":"` + bigMicroCents + `","expiresAt":"2027-02-01T00:00:00.000Z"},{"amountMicroCents":"2","expiresAt":"2027-01-01T00:00:00.000Z"}`
	out, _, err = runBilling(t, balance(two), "billing", "balance", "o", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `"expiring":[` + two + `]`; !strings.Contains(compactJSON3071(t, out), want) {
		t.Errorf("server order and bytes: want %s, got %s", want, out)
	}
	out, _, err = runBilling(t, balance(two), "billing", "balance", "o")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "7 micro-cents") ||
		!strings.Contains(lines[1], bigMicroCents) || !strings.Contains(lines[1], "2027-02-01T00:00:00.000Z") || !strings.Contains(lines[2], "2027-01-01T00:00:00.000Z") {
		t.Errorf("the parts are listed under the balance, in server order:\n%s", out)
	}
}

func TestBillingHelpStatesTheWriteRules(t *testing.T) {
	for _, sub := range []string{"grant", "adjust"} {
		out, _, err := runBilling(t, map[string]string{}, "billing", sub, "--help")
		if err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		flat := strings.Join(strings.Fields(out), " ")
		if strings.Contains(flat, "not enforced") {
			t.Errorf("%s --help must not say the expiry is not enforced", sub)
		}
		if !strings.Contains(flat, "kept for good") || !strings.Contains(flat, "personal data") {
			t.Errorf("%s --help must say the note is kept for good and holds no personal data:\n%s", sub, out)
		}
	}
}

func TestUserSetRolesAcceptsBilling(t *testing.T) {
	const user = `{"id":"usr1","email":"pay@acme.com","name":"Pay","handle":"pay","githubUsername":null,"roles":["READER"]}`
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "GetUser":
			return `{"data":{"user":` + user + `}}`
		case "UpdateUserRoles":
			return `{"data":{"updateUserRoles":` + strings.Replace(user, "READER", "BILLING", 1) + `}}`
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "billing", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := string(captured["UpdateUserRoles"]); !strings.Contains(got, `["BILLING"]`) {
		t.Errorf("BILLING must be sent: %s", got)
	}

	_, _, err := runBilling(t, map[string]string{}, "user", "set-roles", "usr1", "--role", "nonsense", "--yes")
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "owner, admin, contributor, reader, billing") {
		t.Errorf("an unknown role names the five valid ones and exits 2: %v", err)
	}
}

// Guard pin (green on main): BILLING is never an organization role.
func TestOrgMemberSetRoleStillRefusesBilling(t *testing.T) {
	_, captured, err := runBilling(t, map[string]string{}, "org", "member", "set-role", "org1", "--user", "usr1", "--role", "billing")
	if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
		t.Errorf("want exit 2 and no request: %v %v", err, captured)
	}
}
