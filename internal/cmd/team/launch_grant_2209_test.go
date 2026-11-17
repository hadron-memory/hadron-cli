package team

import "testing"

// Without a launch grant the variable is ignored: the server refuses false
// without a grant (hadron-server#4306), so sending it would fail an otherwise
// valid bind. This helper is the one place that rule lives; the --as bind
// path passes no grant. Only the exact value "false" sends anything.
func TestStartSettingsNotAppliedNeedsAGrant2209(t *testing.T) {
	for _, grant := range []string{"", "  "} {
		if startSettingsNotApplied(grant, "false") != nil {
			t.Errorf("grant %q: false must not be sent without a launch grant", grant)
		}
	}
	if got := startSettingsNotApplied("hlg_abc", "false"); got == nil || *got {
		t.Fatalf("a grant with false must send false: %v", got)
	}
	for _, v := range []string{"", "true", "False", "0", "garbage"} {
		if startSettingsNotApplied("hlg_abc", v) != nil {
			t.Errorf("env %q must send nothing", v)
		}
	}
}
