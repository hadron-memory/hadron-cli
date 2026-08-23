package cmd

import (
	"regexp"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Cobra reads a BACK-QUOTED word in a flag's usage string as the flag's
// argument PLACEHOLDER, not as markup. That is a real feature — `"read from
// `+"`path`"+`" renders `+"`--out path`"+` instead of `+"`--out string`"+` —
// but it means a backtick used for emphasis silently rewrites the flag's
// advertised shape.
//
// Found in my own new flag (hadron-cli#505): "(`-` reads stdin)" rendered as
// `--handoff -`, inviting the reader to pass a literal dash. Sweeping then
// turned up live ones in the spec group, including a placeholder on a BOOL —
// a flag that takes no argument at all (hadron-cli#506).
//
// Nothing compiles against a usage string, and backticks read as markdown to
// anyone who has not hit this, so the mistake looks correct in the source. The
// only check that works is the rendered help — which is what this does, via
// the same pflag call cobra uses to render it.
// A metavariable: a lowercase word naming the argument. Permits the hyphen in
// `data-key`; rejects `-`, `Spec:`, and anything capitalised or punctuated.
var metavar = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func TestFlagUsagePlaceholdersAreDeliberate(t *testing.T) {
	f, _ := testFactory(t)
	checked := 0

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		check := func(fl *pflag.Flag) {
			checked++
			// The exact call cobra's help renderer uses: name is the
			// back-quoted token when one is present, else the type name.
			name, _ := pflag.UnquoteUsage(fl)

			// A BOOL takes no argument, so any placeholder on one is a
			// backtick that was meant as emphasis. `--loose Spec:` told the
			// reader to pass something the flag cannot accept.
			if fl.Value.Type() == "bool" && name != "" {
				t.Errorf("`%s --%s` is a bool but advertises the argument %q — a backtick in its usage "+
					"became the placeholder:\n  %s", c.CommandPath(), fl.Name, name, fl.Usage)
			}
			// A real placeholder is a METAVARIABLE — a lowercase noun naming
			// the argument, like `path` or `n`. Anything else is emphasis that
			// got promoted.
			//
			// "one word" is NOT a sufficient rule, and I shipped it first:
			// mutation-checking against the bug that motivated this guard —
			// `"(`+"`-`"+` reads stdin)"` — showed it passing, because `-` is
			// one word on a string flag. It has to reject punctuation and
			// capitals too (hadron-cli#506).
			if name != "" && name != fl.Value.Type() && !metavar.MatchString(name) {
				t.Errorf("`%s --%s` advertises the argument %q, which does not read as a metavariable — a "+
					"backtick in its usage became the placeholder. Use a lowercase noun, or drop the "+
					"backticks:\n  %s", c.CommandPath(), fl.Name, name, fl.Usage)
			}
		}
		c.LocalFlags().VisitAll(check)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCmd(f))

	// The walk silently checks nothing if the command tree stops being
	// reachable this way, so floor it — the same reason the agentic-usage
	// synopsis guard counts what it matched.
	if checked < 100 {
		t.Errorf("only %d flags checked — the walk has stopped reaching the command tree", checked)
	}
}
