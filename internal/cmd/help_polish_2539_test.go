package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#2539 (Refs #1056): wording pins for the utility-page help polish.
// Each phrase is a claim a reader acts on, so a revert must fail here.
func TestHelpPolish2539(t *testing.T) {
	cases := []struct {
		argv    []string
		want    []string
		notWant []string
	}{
		{[]string{"server-info", "--help"}, []string{"(queried anonymously — server-info is public)"}, []string{"(queried anonymously ...)"}},
		{[]string{"--help"}, []string{"check that you are signed in", "the saved org and search scope", "never ran that command"}, []string{"ask whether this request's credential"}},
		{[]string{"config", "get", "--help"}, []string{`{"server": "https://srv.hadronmemory.com"}`}, nil},
		{[]string{"config", "set", "--help"}, []string{"✓ <key> = <value>"}, nil},
		{[]string{"config", "list", "--help"}, []string{"Every value is a\nstring"}, nil},
		{[]string{"version", "--help"}, []string{"release archive", "/releases"}, nil},
	}
	for _, c := range cases {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(c.argv)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", c.argv, err)
		}
		got := out.String()
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v help lacks %q", c.argv, w)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(got, w) {
				t.Errorf("%v help still has %q", c.argv, w)
			}
		}
	}
}
