package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// #1374: install, uninstall and status are verbs of the `team health` group
// that #1181 registers, next to sweep.
func TestTeamHealthGroupRegistersInstallVerbs(t *testing.T) {
	group := newCmdTeamHealth(&cmdutil.Factory{})
	for _, verb := range []string{"sweep", "install", "uninstall", "status"} {
		c, _, err := group.Find([]string{verb})
		if err != nil || c == group || c.Name() != verb {
			t.Fatalf("team health %s is not registered (got %v, err %v)", verb, c, err)
		}
	}
}

// #1181 gate sentence the install entry depends on: the sweep resolves Orca
// from delivery.orca_bin when set, otherwise PATH.
func TestTeamHealthSweepHelpStatesOrcaResolution(t *testing.T) {
	c, _, err := newCmdTeamHealth(&cmdutil.Factory{}).Find([]string{"sweep"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(c.Long), " "), "delivery.orca_bin when set, otherwise PATH") {
		t.Fatalf("sweep help lost the orca resolution sentence:\n%s", c.Long)
	}
}
