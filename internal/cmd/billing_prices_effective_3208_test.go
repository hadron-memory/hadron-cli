package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3208 (follow-up to #3071): since hadron-server#5021 (merged at 2559aef1,
// cor:dmo:170:06) a price row never takes effect in the past. An omitted --effective-at
// is the earliest allowed instant (a short way ahead, never "now"), and an earlier time
// is refused BAD_USER_INPUT with the server's own text. The help must say so, and the
// CLI must neither pre-check the time nor reword the server's refusal.

const pastRefusal3208 = "effectiveAt must be at least 60 seconds from now (not before 2026-11-27T20:01:00.000Z): " +
	"a price takes effect only once every server can apply it, and never in the past. Leave it out for the earliest allowed time."

func flatHelp3208(t *testing.T, args ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(f.IOStreams.Out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// Neither the Long text nor the flag usage says the default is "now" any more, and
// both say what is true: never in the past, an earlier time is refused, left off takes
// the earliest allowed time.
func TestBillingPricesSetHelpMatchesTheServersEffectiveAtRule3208(t *testing.T) {
	h := flatHelp3208(t, "billing", "prices", "set", "--help")
	for _, stale := range []string{"effective now", "unset means now", "takes effect now"} {
		if strings.Contains(h, stale) {
			t.Errorf("help still says %q, which hadron-server#5021 made false:\n%s", stale, h)
		}
	}
	for _, want := range []string{"never in the past", "earliest", "refuses"} {
		if !strings.Contains(h, want) {
			t.Errorf("help must state the rule; missing %q:\n%s", want, h)
		}
	}
}

// The agentic-usage entry for `billing prices set` states the same rule.
func TestAgenticUsageStatesThePriceEffectiveAtRule3208(t *testing.T) {
	u := flatHelp3208(t, "agentic-usage")
	if !strings.Contains(u, "never dated in the past") {
		t.Error("agentic-usage must say a price row is never dated in the past (hadron-server#5021)")
	}
	if strings.Contains(u, "effective now") {
		t.Error("agentic-usage still claims a row is effective now")
	}
}

// A past --effective-at goes to the server unchanged (no client-side check, so the server
// stays the one judge) and the server's refusal reaches the caller with its own text, exit 2.
func TestBillingPricesSetPastEffectiveAtPassesTheServersRefusalThrough3208(t *testing.T) {
	reply := `{"data":null,"errors":[{"message":` + jsonString3208(pastRefusal3208) + `,"extensions":{"code":"BAD_USER_INPUT"}}]}`
	out, captured, err := runBilling(t, map[string]string{"SetAiModelPrice": reply},
		"billing", "prices", "set", "--provider", "p", "--model", "m", "--input", "1", "--output", "2",
		"--effective-at", "2020-01-01T00:00:00Z", "--yes", "--json")
	if err == nil {
		t.Fatalf("the server's refusal must fail the command; out: %s", out)
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("a BAD_USER_INPUT refusal exits %d, got %d (%v)", exitcode.Usage, got, err)
	}
	if !strings.Contains(err.Error(), pastRefusal3208) {
		t.Errorf("the server's refusal must reach the caller unchanged:\n got: %v\nwant: %s", err, pastRefusal3208)
	}
	if len(captured) != 1 {
		t.Errorf("exactly one request, the mutation: %v", captured)
	}
	if in := billingInputOf(t, captured, "SetAiModelPrice"); in["effectiveAt"] != "2020-01-01T00:00:00Z" {
		t.Errorf("a past --effective-at must be sent as typed (the CLI does not pre-check it): %v", in)
	}
}

func jsonString3208(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
