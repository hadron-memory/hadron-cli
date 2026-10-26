package cmd

import (
	"strings"
	"testing"
)

func TestAutomationManualContextIsSeparateFromQuestion(t *testing.T) {
	const note = "Found by run id only; which App it belongs to is not checked here."
	for _, verb := range []string{"approve", "decline"} {
		t.Run(verb, func(t *testing.T) {
			s := oneRowServer(t, "UNADOPTED", nil)
			r := runAuto(t, s, true, "n\n", verb, "--run", "run-1")
			if !strings.Contains(r.errOut, "\n"+note+"\n") {
				t.Fatalf("manual context must occupy its own line:\n%s", r.errOut)
			}
			if s.count("ApproveAutomation")+s.count("DeclineAutomation") != 0 {
				t.Fatal("declining confirmation must not mutate")
			}
		})
	}
}

func TestAutomationDeclineWarningUsesPlainWords(t *testing.T) {
	s := oneRowServer(t, "UNADOPTED", nil)
	r := runAuto(t, s, true, "n\n", "decline", "nightly")
	if strings.Contains(r.errOut, "carries no hash") || !strings.Contains(r.errOut, "revoke that newer approval too") {
		t.Fatalf("explain the concurrent approval in plain words:\n%s", r.errOut)
	}
}

func TestAutomationRunHelpExplainsIgnoredApp(t *testing.T) {
	s := oneRowServer(t, "UNADOPTED", nil)
	for _, verb := range []string{"approve", "decline", "approval"} {
		help := runAuto(t, s, false, "", verb, "--help")
		text := strings.Join(strings.Fields(help.out), " ")
		if !strings.Contains(text, "--app is ignored for this lookup") || strings.Contains(text, "carries no hash") {
			t.Errorf("%s help must explain run lookup without hash jargon:\n%s", verb, help.out)
		}
	}
}

func TestAutomationListMarksOnlyTheApprovalRowAsPossiblyStale(t *testing.T) {
	s := oneRowServer(t, "ADOPTED", nil)
	r := runAuto(t, s, false, "", "ls")
	var row string
	for _, line := range strings.Split(r.out, "\n") {
		if strings.Contains(line, "schedule/nightly") {
			row = line
		}
	}
	if !strings.Contains(row, "approved (approval may be stale)") {
		t.Fatalf("the row itself needs the suffix, not only the footer: %q", row)
	}
	detail := runAuto(t, s, false, "", "approval", "nightly")
	if strings.Contains(detail.out, "approval may be stale") {
		t.Fatalf("the fresh detail state must not inherit the list suffix:\n%s", detail.out)
	}
}
