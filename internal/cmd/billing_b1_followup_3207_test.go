package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3207: the follow-up to cli#3071's review. Pins for the two behaviour
// changes (a null list element fails the command; the adjust prompt shows the
// note) and the test gaps the review found.

// The schema types list elements non-null, so a null element is a response the
// server cannot send. Skipping it would print a short list as if it were the
// whole one; the command fails the way the null-response branch does.
func TestBillingNullListElementFailsTheCommand3207(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp map[string]string
		args []string
	}{
		{"spend", spendResponse("null", "false", "1"), spendArgs()},
		{"spend --json", spendResponse(spendRowsJSON+",null", "false", "3"), spendArgs("--json")},
		{"balance", map[string]string{"CreditBalance": `{"data":{"creditBalance":{"organizationId":"org1","balanceMicroCents":"7","expiring":[null]}}}`}, []string{"billing", "balance", "acme"}},
		{"balance --json", map[string]string{"CreditBalance": `{"data":{"creditBalance":{"organizationId":"org1","balanceMicroCents":"7","expiring":[{"amountMicroCents":"2","expiresAt":"2027-01-01T00:00:00.000Z"},null]}}}`}, []string{"billing", "balance", "acme", "--json"}},
	} {
		out, _, err := runBilling(t, tc.resp, tc.args...)
		if got := exitCodeFor(err); got != exitcode.Error {
			t.Errorf("%s: want exit %d, got %d (%v)", tc.name, exitcode.Error, got, err)
		}
		if out != "" {
			t.Errorf("%s: nothing may be printed for a response the schema forbids: %q", tc.name, out)
		}
		if err != nil && strings.Contains(err.Error(), "acme") {
			t.Errorf("%s: the org given is not shown: %v", tc.name, err)
		}
	}
}

func TestBillingAdjustPromptShowsTheNote3207(t *testing.T) {
	note := "refund r-1\x1b[31m for ticket 42"
	gql, sent := captureGraphQL(t, map[string]string{})
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"billing", "adjust", "acme", "--amount", "-300", "--source-type", "refund", "--source-id", "r1", "--note", note, "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Cancelled {
		t.Errorf("declined: want exit %d, got %d", exitcode.Cancelled, got)
	}
	if !strings.Contains(errOut.String(), "refund r-1") || !strings.Contains(errOut.String(), "for ticket 42") {
		t.Errorf("the prompt must show the permanent note: %q", errOut.String())
	}
	if strings.Contains(errOut.String(), "\x1b") {
		t.Errorf("the note goes through promptValue: no control bytes in the prompt: %q", errOut.String())
	}
	if len(sent) != 0 {
		t.Errorf("a decline sends nothing: %v", sent)
	}
}

func TestBillingSpendOrgAdminRefusalIsExit8Once3207(t *testing.T) {
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if op == "AiSpend" {
			calls++
			return `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(spendArgs(), "--server", gql.URL))
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Forbidden {
		t.Errorf("want exit %d, got %d (%v)", exitcode.Forbidden, got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("the server's message must survive: %v", err)
	}
	if calls != 1 {
		t.Errorf("sent %d times, want exactly once", calls)
	}
}

func TestBillingSpendOffsetBoundIsSentAsGiven3207(t *testing.T) {
	_, captured, err := runBilling(t, spendResponse("", "false", "0"), "billing", "spend", "o",
		"--from", "2026-11-01T02:00:00+02:00", "--to", "2026-12-01T00:00:00-05:00", "--group-by", "day")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := billingVars(t, captured["AiSpend"])
	if vars["from"] != "2026-11-01T02:00:00+02:00" || vars["to"] != "2026-12-01T00:00:00-05:00" {
		t.Errorf("a date-time with an offset is sent unchanged: %v", vars)
	}
}

func TestBillingGrantExpiresAtWithOffsetIsSentAndEntryShowsIt3207(t *testing.T) {
	out, captured, err := runBilling(t, map[string]string{"GrantCredits": grantResponse},
		"billing", "grant", "acme", "--amount", "5", "--source-type", "t", "--source-id", "i",
		"--expires-at", "2027-01-01T01:00:00+01:00", "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := billingInputOf(t, captured, "GrantCredits")["expiresAt"]; got != "2027-01-01T01:00:00+01:00" {
		t.Errorf("expiresAt is sent as given: %v", got)
	}
	if !strings.Contains(out, `"expiresAt": "2027-01-01T00:00:00.000Z"`) {
		t.Errorf("the entry's own expiresAt is printed: %s", out)
	}
}

func TestBillingGrantConflictJSONNamesTheServerCode3207(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"GrantCredits": `{"errors":[{"message":"m","extensions":{"code":"IDEMPOTENCY_CONFLICT"}}]}`})
	f, stdout, errOut := testFactoryTTY(t, "")
	f.JSON = true
	root := NewRootCmd(f)
	err := func() error {
		root.SetArgs([]string{"billing", "grant", "acme", "--amount", "5", "--source-type", "t", "--source-id", "i", "--yes", "--json", "--server", gql.URL})
		return root.Execute()
	}()
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Errorf("want exit %d, got %d", exitcode.Conflict, got)
	}
	renderError(f, err)
	if out := stdout.String() + errOut.String(); !strings.Contains(out, `"IDEMPOTENCY_CONFLICT"`) {
		t.Errorf("the failure envelope must carry serverCode: %q", out)
	}
}

// compactJSON3071 must keep every byte of a string, including one that ends in
// an escaped backslash (the closing quote then is not escaped).
func TestCompactJSON3071StringEndingInEscapedBackslash3207(t *testing.T) {
	in := "{ \"a\": \"x\\\\\", \"b\": \"y z\",\n \"c\": 1 }"
	want := `{"a":"x\\","b":"y z","c":1}`
	if got := compactJSON3071(t, in); got != want {
		t.Errorf("want %s, got %s", want, got)
	}
	if got := compactJSON3071(t, `{"a":"q\"  r\\\\", "b": 2}`); got != `{"a":"q\"  r\\\\","b":2}` {
		t.Errorf("an escaped quote and an even run of backslashes: got %s", got)
	}
}

// A cost is never described as owed or due: whole words only, so a key such as
// "residue" does not trip the check.
var owedOrDue3207 = regexp.MustCompile(`(?i)\b(?:owed|due)\b`)

func TestOwedOrDueCheckMatchesWholeWordsOnly3207(t *testing.T) {
	for in, want := range map[string]bool{"amount due": true, "Owed": true, "residue": false, "overdue": false, "(none)": false} {
		if got := owedOrDue3207.MatchString(in); got != want {
			t.Errorf("%q: want %v, got %v", in, want, got)
		}
	}
}
