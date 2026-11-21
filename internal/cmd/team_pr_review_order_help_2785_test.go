package cmd

import (
	"strings"
	"testing"
)

// cli#2785: `team pr seat` and `team pr verdict` help state the review order,
// the negative-verdict rule and the signed-receipt remedy. The remedy is team
// process (Peter's ruling on hadron-server#4494), not a platform contract, and
// the help must say so.
func TestTeamPRSeatVerdictHelpStatesReviewOrder2785(t *testing.T) {
	for _, leaf := range []string{"seat", "verdict"} {
		t.Run(leaf, func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "pr", leaf, "--help"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := strings.Join(strings.Fields(out.String()), " ")
			for _, s := range []string{
				"schedule, freeze, seat, verdict, finish",
				"binds only while its own task is live",
				"native negative verdict at a head blocks every slot there",
				"the head must move",
				"TASK_ENDED, REVIEW_SEAT_UNASSIGNED or REVIEW_HEAD_MISMATCH",
				"signed PR receipt at the exact head",
				"the ready call names the refusal code",
				"team process, not a platform contract",
			} {
				if !strings.Contains(help, s) {
					t.Errorf("%s help missing %q:\n%s", leaf, s, help)
				}
			}
		})
	}
}
