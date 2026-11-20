package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// cli#2594 (automation pages): help text carries no commit hash and no
// maintainer citation (a reader cannot map either to anything they can ask
// for), and the --instruction paragraph sits only on the pages that take the
// flag, leading with who may ignore it.
func TestAutomationHelpNamesNoHashOrMaintainerCitation2594(t *testing.T) {
	hash := regexp.MustCompile(`\b[0-9a-f]{8}\b`)
	cites := []string{"cor:agt:", "cor:acl:", "rulings 00"}
	pages := helpTree2508(t, "automation")
	for _, name := range []string{"automation", "automation grant", "automation grant set", "automation grant list", "automation grant revoke",
		"automation team-snapshot-grant", "automation team-snapshot-grant set", "automation team-snapshot-grant list", "automation team-snapshot-grant revoke"} {
		help, ok := pages[name]
		if !ok {
			t.Fatalf("no help page %q", name)
		}
		if m := hash.FindString(help); m != "" && regexp.MustCompile(`[a-f]`).MatchString(m) && regexp.MustCompile(`\d`).MatchString(m) {
			t.Errorf("%q help names a commit hash %q", name, m)
		}
		for _, c := range cites {
			if strings.Contains(help, c) {
				t.Errorf("%q help carries the maintainer citation %q", name, c)
			}
		}
	}
	set := strings.Join(strings.Fields(pages["automation team-snapshot-grant set"]), " ")
	if strings.Contains(set, "--instruction is for a worker") {
		t.Error("team-snapshot-grant set has no --instruction flag and must not carry its paragraph")
	}
	for _, name := range []string{"automation team-snapshot-grant list", "automation team-snapshot-grant revoke"} {
		if !strings.Contains(strings.Join(strings.Fields(pages[name]), " "), "--instruction is for a worker acting on its holder's instruction; anyone else leaves it out") {
			t.Errorf("%q must lead its --instruction paragraph with who may ignore it", name)
		}
	}
}

// cli#2594 (Vera, #124562): what re-adopting a MANUAL run means, worded as the
// server's current behavior.
func TestReAdoptHelpSaysWhatAManualReAdoptMeans2594(t *testing.T) {
	h := strings.Join(strings.Fields(helpTree2508(t, "automation")["automation re-adopt"]), " ")
	for _, want := range []string{"For a MANUAL run, give the id of the run you started by hand", "As the server behaves today", "the approval applies to that run only"} {
		if !strings.Contains(h, want) {
			t.Errorf("automation re-adopt help missing %q", want)
		}
	}
}
