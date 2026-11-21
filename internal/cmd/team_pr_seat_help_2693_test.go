package cmd

import (
	"strings"
	"testing"
)

// cli#2693: `team pr seat` is authorized on the SEAT task's owner chain
// (cor:agt:130:04 item 3; hadron-server assignReviewSeat runs ownerChain on the
// seat row), never the build task's. The help must say so.
func TestTeamPRSeatHelpNamesSeatTaskOwnerChain2693(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "pr", "seat", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(help, "Only the seat task's owner chain may") {
		t.Errorf("seat help must name the seat task's owner chain:\n%s", help)
	}
	for _, s := range []string{"the build task's assignee, who created it", "TASK_NOT_OWNER"} {
		if !strings.Contains(help, s) {
			t.Errorf("seat help missing %q:\n%s", s, help)
		}
	}
	if strings.Contains(help, "build task's owner chain") {
		t.Errorf("seat help still names the build task's owner chain:\n%s", help)
	}
}
