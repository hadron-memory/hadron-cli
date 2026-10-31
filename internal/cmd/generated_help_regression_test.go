package cmd

import (
	"strings"
	"testing"
)

func TestGeneratedHelpSurvivesExecute(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "--help"}, "For the full reference an AI assistant uses"},
		{[]string{"completion", "--help"}, "It only prints the script"},
		{[]string{"completion", "bash"}, "__start_hadron"},
		{[]string{"__complete", "vers"}, "version"},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("missing %q in output: %s", tc.want, out.String())
			}
			counts := map[string]int{}
			for _, child := range root.Commands() {
				counts[child.Name()]++
			}
			for _, name := range []string{"help", "completion"} {
				if counts[name] != 1 {
					t.Fatalf("%s child count = %d, want 1", name, counts[name])
				}
			}
		})
	}
}

func TestAutomationHelpDistinguishesStoredStateAndManualApp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		want   []string
		absent []string
	}{
		{
			name: "ls_names_only_the_two_stored_states",
			args: []string{"automation", "ls", "--help"},
			want: []string{"stored approval state", `"not approved" or "approved (approval may be stale)"`},
			// The disallowed third state, in every spelling help could use: the
			// detail projection's CHANGED word and the claim an approved row is
			// current.
			absent: []string{`changed since it was approved`, "CHANGED", "up to date", "unchanged"},
		},
		{
			name:   "approve_says_manual_app_is_not_verified",
			args:   []string{"automation", "approve", "--help"},
			want:   []string{"--app does not verify which App owns it"},
			absent: []string{"it affects everyone who relies on that App"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			// Help is wrapped by hand in the source: match on its words, not on
			// where a line happens to break, so a rewrap can neither fail a
			// present claim nor hide an absent one.
			text := strings.Join(strings.Fields(out.String()), " ")
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("help missing %q:\n%s", w, out.String())
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(text, bad) {
					t.Errorf("help must not say %q:\n%s", bad, out.String())
				}
			}
		})
	}
}
