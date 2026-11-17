package cmd

import (
	"strings"
	"testing"
)

func TestCodingReviewCreateHelpExplainsRootDependentLoc(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"coding", "review", "create", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"the node's loc is <root>:<check-name>",
		"--root defaults to review",
		"With the default root, thin-resolver-field becomes review:thin-resolver-field",
		"With --root checks, the same name becomes checks:thin-resolver-field",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q", want)
		}
	}
	if strings.Contains(help, `the node's loc is "review:" plus`) {
		t.Error("help still claims an unconditional review: prefix")
	}
}
