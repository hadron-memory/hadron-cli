package automation

import "testing"

func TestApprovalAppFlagRejectsChangedTerminalText(t *testing.T) {
	for _, app := range []string{"hrn:app:acme.com:op\x1b[31ms", "hrn:app:acme.com:op\u202es"} {
		if terminalSafe(app) {
			t.Fatalf("fixture must be changed by sanitizing: %q", app)
		}
		if got := approvalAppFlag(app); got != "" {
			t.Errorf("unsafe App must not enter a copied command: %q", got)
		}
	}
	if got := approvalAppFlag("hrn:app:acme.com:ops"); got != " --app hrn:app:acme.com:ops" {
		t.Errorf("safe App flag changed: %q", got)
	}
}
