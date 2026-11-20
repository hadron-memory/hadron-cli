package cmd

import (
	"strings"
	"testing"
)

// cli#2672: the org-level grant says what it is, that it is retiring, and
// where App grants live, so nobody reaches for it to grant a worker.
func TestGrantHelpNamesTheOrgGrantRetiringAndTeamGrant2672(t *testing.T) {
	for _, path := range [][]string{{"grant"}, {"grant", "create"}} {
		f, _ := testFactory(t)
		c, _, err := NewRootCmd(f).Find(path)
		if err != nil {
			t.Fatal(err)
		}
		help := c.Short + "\n" + c.Long
		for _, want := range []string{"organization", "retiring", "hadron team grant"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v help lacks %q", path, want)
			}
		}
	}
}
