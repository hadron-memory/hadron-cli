package spec

import (
	"sort"
	"strings"
	"testing"
)

func mustCit(t *testing.T, s string) Citation {
	t.Helper()
	c, err := ParseCitation(s)
	if err != nil {
		t.Fatalf("ParseCitation(%q): %v", s, err)
	}
	return c
}

func equalIntsUnordered(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]int(nil), a...)
	bc := append([]int(nil), b...)
	sort.Ints(ac)
	sort.Ints(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseCitationValid(t *testing.T) {
	cases := []struct {
		in  string
		lvl int
	}{
		{"msg", 1},
		{"msg:010", 2},
		{"msg:010:02", 3},
		{"msg:010:02:03", 4},
	}
	for _, c := range cases {
		got, err := ParseCitation(c.in)
		if err != nil {
			t.Fatalf("ParseCitation(%q): %v", c.in, err)
		}
		if got.Level() != c.lvl {
			t.Errorf("%q Level()=%d, want %d", c.in, got.Level(), c.lvl)
		}
		if got.Format() != c.in {
			t.Errorf("%q Format()=%q", c.in, got.Format())
		}
	}
}

func TestParseCitationInvalid(t *testing.T) {
	for _, in := range []string{"", "ms", "msgg", "MSG", "msg:10", "msg:0100", "msg:010:2", "msg:010:02:3", "msg:010:02:03:04", "m1g:010"} {
		if _, err := ParseCitation(in); err == nil {
			t.Errorf("ParseCitation(%q) should fail", in)
		}
	}
}

func TestCitationParentContract(t *testing.T) {
	c := mustCit(t, "msg:010:02:03")
	if p, ok := c.Parent(); !ok || p.Format() != "msg:010:02" {
		t.Errorf("Parent()=%q,%v", p.Format(), ok)
	}
	if _, ok := mustCit(t, "msg").Parent(); ok {
		t.Errorf("module must have no parent")
	}
	rule := mustCit(t, "msg:010:02")
	if cl, ok := rule.ContractLoc(); !ok || cl.Format() != "msg:010:00" {
		t.Errorf("ContractLoc()=%q,%v", cl.Format(), ok)
	}
	if !mustCit(t, "msg:010:00").IsContract() {
		t.Error("msg:010:00 should be a contract")
	}
	if rule.IsContract() {
		t.Error("msg:010:02 is not a contract")
	}
}

func TestDefaultPLevel(t *testing.T) {
	cases := map[string]int{"msg": 0, "msg:010": 1, "msg:010:00": 1, "msg:010:02": 1, "msg:010:02:03": 2}
	for loc, want := range cases {
		if got := defaultPLevel(mustCit(t, loc)); got != want {
			t.Errorf("defaultPLevel(%q)=%d, want %d", loc, got, want)
		}
	}
}

func TestMemoryURNFromFlag(t *testing.T) {
	for _, in := range []string{"micromentor.org::platform-specs", "hrn:memory:micromentor.org::platform-specs", "urn:memory:micromentor.org::platform-specs"} {
		got, err := memoryURNFromFlag(in)
		if err != nil {
			t.Fatalf("memoryURNFromFlag(%q): %v", in, err)
		}
		if got != "micromentor.org::platform-specs" {
			t.Errorf("memoryURNFromFlag(%q)=%q", in, got)
		}
	}
	if _, err := memoryURNFromFlag("  "); err == nil {
		t.Error("empty memory should error")
	}
}

func TestSpecNodeRef(t *testing.T) {
	if got := specNodeRef("micromentor.org::platform-specs", "msg:010:02"); got != "micromentor.org::platform-specs::msg:010:02" {
		t.Errorf("specNodeRef=%q", got)
	}
}

func TestRubricBody(t *testing.T) {
	body := rubricBody(mustCit(t, "msg:010:02"), "W2 — 48h Activation Nudge")
	for _, h := range []string{headingDefinition, headingRule, headingDurable, headingInvalidates} {
		if !strings.Contains(body, "## "+h) {
			t.Errorf("body missing %q heading", h)
		}
	}
	if !strings.Contains(body, "# msg:010:02 — W2 — 48h Activation Nudge") {
		t.Errorf("body missing title H1:\n%s", body)
	}
}

func TestSpecTagsDedup(t *testing.T) {
	got := specTags(1, []string{"messaging", "spec", "messaging", "nudge", "p1"})
	if !equalStrings(got, []string{"spec", "p1", "messaging", "nudge"}) {
		t.Errorf("specTags=%v", got)
	}
}

func TestParseLedgerModulesNoRetired(t *testing.T) {
	body := "## Service codes\n\n| Code | Service | Status |\n|---|---|---|\n| `mat` | matching | allocated |\n| `msg` | messaging | allocated |\n\n### `msg` — messaging\n- **010** — W-series\n- next free feature: `020` · retired: none\n"
	l := parseLedger(body)
	if !l.modules["mat"] || !l.modules["msg"] {
		t.Errorf("modules=%v", l.modules)
	}
	if len(l.retired) != 0 {
		t.Errorf("retired should be empty for 'none', got %v", l.retired)
	}
}

func TestParseLedgerRetired(t *testing.T) {
	body := "### `msg` — messaging\n- **010** — W-series\n  - retired: 03, 07\n"
	l := parseLedger(body)
	if !equalIntsUnordered(l.retired["msg:010"], []int{3, 7}) {
		t.Errorf("retired[msg:010]=%v", l.retired["msg:010"])
	}
}
