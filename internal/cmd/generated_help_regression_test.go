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
		args         []string
		want, absent string
	}{
		{[]string{"automation", "ls", "--help"}, "stored approval state", "or \"changed since it was approved\""},
		{[]string{"automation", "approve", "--help"}, "--app\ndoes not verify which App owns it", "it affects everyone who relies on that App"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(tc.args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), tc.want) || strings.Contains(out.String(), tc.absent) {
			t.Fatalf("inaccurate help: %s", out.String())
		}
	}
}
