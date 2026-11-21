package cmd

import (
	"strings"
	"testing"
)

// cli#2785: `team pr seat` and `team pr verdict` help state the review order,
// what a negative verdict does, and the remedy when a seat cannot bind or record:
// only a native verdict counts, so the remedy is a fresh seat bound --supersedes at
// the same head, with the signed PR receipt as evidence (never the record).
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
				"binds only while its own task is live (STARTED)",
				"a finished seat cannot take a verdict",
				"A negative verdict at a head blocks every slot there until the head moves or its own reviewer re-rates it",
				"TASK_ENDED, REVIEW_SEAT_UNASSIGNED or REVIEW_HEAD_MISMATCH",
				"only a verdict recorded with pr verdict counts",
				"create a fresh seat task and bind it with --supersedes at the same head",
				"A signed PR receipt at the exact head is evidence, not the record",
			} {
				if !strings.Contains(help, s) {
					t.Errorf("%s help missing %q:\n%s", leaf, s, help)
				}
			}
			for _, bad := range []string{"is the record", "names the refusal code", "team process", "team's practice"} {
				if strings.Contains(help, bad) {
					t.Errorf("%s help must not say %q:\n%s", leaf, bad, help)
				}
			}
		})
	}
}
