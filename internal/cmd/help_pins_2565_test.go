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
		// question ask: every input field shaped, where the full input is
		// documented, the tiers defined, the code printed and how to read it back,
		// and a copyable example (the old F8-F11 pins, now on the question group)
		{[]string{"question", "ask"}, []string{
			`options the choices, each {"key": "pg", "label": "Postgres",`,
			`"whyChoose": "...", "whyNot": "...", "risks": "..."`,
			`noRecommendation {"reason": "..."} when none is given`,
			"narrows the log's default deciders, never widens past its",
			"hadron user search",
			"the authorization a grant question carries, as JSON",
			`{"ref": "..."}, {"node": "..."} or {"spec": "..."}`,
			"is AskQuestionInput in the server's schema",
			"APPROVED (it counts once a decider has approved the",
			"SIGNED (a decider's passkey signature",
			"Passkey signing is not available yet, so a SIGNED question stays",
			"question get <code> --log <log> reads it back",
			`prefixed "✓ asked"`,
			"cat > ask.json <<'EOF'",
		}},
		// question amend: the reference lists shaped, the clearing rules, the stale
		// revision refusal and the success output
		{[]string{"question", "amend"}, []string{
			`{"ref": "hadron-server#1640"}, {"node": "<id or`,
			"An empty list ([]) clears that list",
			`"background", "whyNow", "assignee",`,
			"the tiers mean: hadron question ask --help",
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

// TestQuestionHelpNamesQuestionInputs pins cli#2465: the server's input types are
// AskQuestionInput/AmendQuestionInput, so help that points at the old names sends
// a reader to a type the schema no longer has.
func TestQuestionHelpNamesQuestionInputs(t *testing.T) {
	for _, page := range [][]string{{"question", "ask"}, {"question", "amend"}} {
		help := flatHelp2565(t, page...)
		if strings.Contains(help, "DecisionInput") {
			t.Errorf("%v help still names a *DecisionInput type", page)
		}
		if !strings.Contains(help, "QuestionInput") {
			t.Errorf("%v help does not name its *QuestionInput type", page)
		}
	}
}
