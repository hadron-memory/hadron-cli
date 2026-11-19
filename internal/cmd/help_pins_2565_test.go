package cmd

import (
	"strings"
	"testing"
)

// cli#2565 (group-1051 residual round 1, Gil): facts the evaluator's round 1
// found missing on the three failing pages — decision amend, decision ask and
// memory sync pull — pinned so a later edit cannot drop them. The P2s: input
// fields named but not shaped (amend, ask), tier undefined (amend, ask), and
// no way described to configure a sync repository (sync pull).

func flatHelp2565(t *testing.T, path ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func Test2565PagesSayWhatRound1FoundMissing(t *testing.T) {
	for _, tc := range []struct {
		page []string
		want []string
	}{
		// decision ask — F8: every input field shaped, and where the full
		// input is documented
		{[]string{"decision", "ask"}, []string{
			`{"question": "...", "options": [{"key": "pg", "label": "Postgres"}`,
			`"noRecommendation": {"reason": "..."}`,
			"narrows the log's default deciders, never widens past its eligible set",
			"hadron user search",
			"the authorization a grant decision carries, as JSON (authorizes: true only)",
			`{"ref": "hadron-server#1640"}, {"node": "<id or URN>"}`,
			"is AskDecisionInput in the server's schema",
		}},
		// decision ask — F9: the tiers defined; F10: the code is printed and
		// how to read it back; F11: a copyable example file
		{[]string{"decision", "ask"}, []string{
			"APPROVED: the ruling counts once a decider has approved the answered decision",
			"SIGNED: a decider's passkey signature rather than an approval",
			"SIGNED is forced when authorizes is true",
			"passkey signing is not available yet, so a SIGNED decision stays ANSWERED_UNCONFIRMED",
			"decision get <code> --log <log> reads it back",
			`prefixed "✓ asked"`,
			"cat > ask.json <<'EOF'",
		}},
		// decision amend — F5: parts/addParts shaped, the reference lists
		// shaped, and an example that uses a file — one whose added part the
		// server would accept (exactly one recommendation arm, Bob #2584 P2-1)
		{[]string{"decision", "amend"}, []string{
			`{"n": <part number>}`,
			"DECISION_PART_RENUMBER",
			"new parts appended at the end, each shaped like ask's parts",
			`exactly one of "recommendation" or "noRecommendation"`,
			"DECISION_RECOMMENDATION_MISSING",
			`{"ref": "hadron-server#1640"}, {"node": "<id or URN>"}`,
			"cat > amend.json <<'EOF'",
			`"noRecommendation":{"reason":"outside the asker's remit"}`,
			"--input-file amend.json",
		}},
		// decision amend — F6: the tiers one pointer away; F7: the stale
		// revision refusal and the success output
		{[]string{"decision", "amend"}, []string{
			"what the tiers mean: hadron decision ask --help",
			"refused (NODE_WRITE_CONFLICT, exit 5)",
			"read it again and retry with the new revision",
			`prefixed "✓ amended"`,
		}},
		// memory sync pull — F39: the prerequisite and how to see it
		{[]string{"memory", "sync", "pull"}, []string{
			"hadron memory set <memory> --git-source https://github.com/<owner>/<repo>",
			`(--git-source "" clears it.)`,
			"SYNC_NOT_ATTEMPTED",
			"NO_SOURCE or NO_INSTALLATION, exit",
			"hadron memory get <memory> --json shows the recorded source",
			"as having no source exits 2 with no sync request sent",
		}},
		// memory sync pull — F41: what to do about a skipped or held file;
		// F40: the exit facts stay while the request mechanics go (a failed
		// request is never retried, Bob #2584 P3-14)
		{[]string{"memory", "sync", "pull"}, []string{
			"Fix what the code names",
			"a later pull takes it by itself (a README",
			"name collision recurs until the pair is renamed)",
			"Exit 7 means the sync may have happened",
			"the failed request is not retried",
			"clean:false",
		}},
	} {
		help := flatHelp2565(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
	}
}
