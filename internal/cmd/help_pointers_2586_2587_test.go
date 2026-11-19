package cmd

import (
	"strings"
	"testing"
)

// cli#2586 + cli#2587: the pages that take or list grants and action names
// point at where the answer lives. A reader of one listing cannot otherwise
// tell which of the three answers "what can I do?", and nothing said where
// valid action names are listed.
func TestGrantAndPolicyHelpPointToTheirSources2586(t *testing.T) {
	const docs = "docs/reference/authorization.md"
	cases := []struct {
		page string
		want []string
	}{
		{"grant", []string{"hadron team grant actions", "hadron team grant list", "hadron auth grants"}},
		{"grant create", []string{"hadron team grant actions"}},
		{"grant list", []string{"hadron team grant list", "hadron auth grants"}},
		{"team grant", []string{"hadron grant list", "hadron auth grants"}},
		{"team grant list", []string{"hadron grant list", "hadron auth grants"}},
		{"policy", []string{docs}},
		{"policy explain", []string{docs}},
		{"policy set", []string{docs}},
	}
	for _, c := range cases {
		pages := helpTree2508(t, strings.Fields(c.page)[0])
		help, ok := pages[c.page]
		if !ok {
			t.Fatalf("no help page %q", c.page)
		}
		flat := strings.Join(strings.Fields(help), " ")
		for _, w := range c.want {
			if !strings.Contains(flat, w) {
				t.Errorf("%q help does not mention %q", c.page, w)
			}
		}
	}
}
