package cmd

import (
	"strings"
	"testing"
)

// cli#2591 (cor:dmo:100:00): help must not imply a tier 2 or 3 row is applied
// to any charge, must not suggest a fallback to another tier, and must say a
// row at a tier nothing resolves is stored but unused.
func TestBillingTierHelpStatesTheTierSemantics2591(t *testing.T) {
	pages := helpTree2508(t, "billing")
	for _, c := range []struct {
		page string
		want []string
	}{
		{"billing prices set", []string{"stored but unused", "never applied to a charge", "never billed at another tier's rate", "leave --tier off"}},
		{"billing prices", []string{"stored but unused"}},
	} {
		help, ok := pages[c.page]
		if !ok {
			t.Fatalf("no help page %q", c.page)
		}
		flat := strings.Join(strings.Fields(help), " ")
		for _, w := range c.want {
			if !strings.Contains(flat, w) {
				t.Errorf("%q help does not say %q", c.page, w)
			}
		}
		if strings.Contains(flat, "--tier 2") {
			t.Errorf("%q help still offers a tier-2 example", c.page)
		}
	}
}
