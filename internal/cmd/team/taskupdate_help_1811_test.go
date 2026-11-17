package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// #1811: the assignee-edit paragraph must name references (ruling 0233) and
// must not keep the stale "properties alone" claim.
func TestTaskUpdateHelpAssigneeMayEditReferences(t *testing.T) {
	long := newCmdTeamTaskUpdate(cmdutil.NewFactory()).Long
	for _, want := range []string{"properties (--properties, --clear-properties) and references (--ref,", "--clear-refs), alone or together", "TASK_NOT_OWNER", "hadron-server#3718"} {
		if !strings.Contains(strings.Join(strings.Fields(long), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, stale := range []string{"properties alone", "--properties and nothing else"} {
		if strings.Contains(long, stale) {
			t.Errorf("help still says %q", stale)
		}
	}
}
