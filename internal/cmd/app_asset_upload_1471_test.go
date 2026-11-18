package cmd

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #1471: an exit-1 INTERNAL_SERVER_ERROR from the asset-upload toggle is an
// unknown outcome, said once, bounded, without a retry or "nothing changed"
// promise; and the 1338 plan doc's labels are spaced.

const unknownOutcome1471 = "INTERNAL_SERVER_ERROR (exit 1) is an unknown outcome: it can be a concealed refusal or a real server fault, so do not assume nothing changed, and this command does not retry"

func flat1471(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestAssetUploadHelpStatesTheUnknownOutcome1471(t *testing.T) {
	for _, verb := range []string{"enable", "disable"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "asset-upload", verb, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(flat1471(out.String()), unknownOutcome1471) {
			t.Errorf("%s help lacks the unknown-outcome sentence", verb)
		}
	}
	guide, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flat1471(string(guide)), unknownOutcome1471) {
		t.Error("agentic-usage lacks the unknown-outcome sentence")
	}
}

// A label glued to its number ("to2", "exit8", "HTTP200", "availability7")
// reads as one word; the plan doc spaces them.
func TestPlan1338LabelsAreSpaced1471(t *testing.T) {
	b, err := os.ReadFile("../../docs/plans/1338-app-run-surfaces.md")
	if err != nil {
		t.Fatal(err)
	}
	glued := regexp.MustCompile(`\b(to|exit|HTTP|availability)[0-9]`)
	for i, line := range strings.Split(string(b), "\n") {
		if loc := glued.FindString(line); loc != "" {
			t.Errorf("plan doc line %d has a glued label %q: %s", i+1, loc, line)
		}
	}
}
