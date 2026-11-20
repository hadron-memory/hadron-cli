package cmd

import (
	"strings"
	"testing"
)

func flat2594(s string) string { return strings.Join(strings.Fields(s), " ") }

// cli#2594 slice B: billing, marketplace, quota and usage help. Each check is
// a phrase the audit asked for, so a later rewrap cannot silently lose it.
func TestSliceBHelpPolish2594(t *testing.T) {
	billing := helpTree2508(t, "billing")
	for name, h := range billing {
		if strings.Contains(flat2594(h), "divide by 100000000") || strings.Contains(h, "hrn:org:<slug>") {
			t.Errorf("%q: one micro-cent conversion phrasing, and hrn:org:<root> not <slug>", name)
		}
	}
	if !strings.Contains(flat2594(billing["billing grant"]), `it prints "Ledger entry <id>:`) {
		t.Error("billing grant must say what it prints on success")
	}
	if !strings.Contains(billing["billing prices set"], "# 15 USD in and 75 USD out per million tokens") {
		t.Error("billing prices set example must say what its rates mean")
	}
	if h := helpTree2508(t, "marketplace")["marketplace"]; !strings.Contains(h, "# count only: no rows, just the total\n  hadron marketplace orgs --limit 0") {
		t.Error("marketplace example --limit 0 must say it is count only")
	}
	quota := helpTree2508(t, "quota")
	for _, name := range []string{"quota list", "quota set"} {
		h := flat2594(quota[name])
		if !strings.Contains(h, "root domain (acme.com)") || !strings.Contains(h, "it is not the App context") {
			t.Errorf("%q must define root and say --app is not the App context", name)
		}
	}
	if !strings.Contains(quota["quota set"], "# stops every run launch for that App") {
		t.Error("quota set llm.tokens --limit 0 example needs its caution")
	}
	usage := helpTree2508(t, "usage")
	ev := usage["usage events"]
	cost, what := strings.Index(ev, "COST is in micro-cents"), strings.Index(ev, "What you see depends")
	if cost < 0 || what < 0 || cost > what || strings.Contains(ev, "Details:") {
		t.Error("usage events: the COST note leads, and the Details repeat is gone")
	}
	if !strings.Contains(usage["usage record"], "Every\nexample below records a real event.") {
		t.Error("usage record must say its examples record real events")
	}
	if strings.Contains(usage["usage activity"], "omitted on the wire") {
		t.Error("usage activity: no implementation language")
	}
}
