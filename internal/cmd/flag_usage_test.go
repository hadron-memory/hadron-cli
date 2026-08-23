package cmd

import (
	"regexp"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Cobra reads a BACK-QUOTED word in a flag's usage string as the flag's
// argument PLACEHOLDER, not as markup. That is a real feature — "read from
// `path`" renders `--out path` instead of `--out string` — but it means a
// backtick used for emphasis silently rewrites the flag's advertised shape.
//
// Found in my own new flag (hadron-cli#505): "(`-` reads stdin)" rendered as
// `--handoff -`, inviting the reader to pass a literal dash. Sweeping then
// turned up fourteen live ones in the spec group, including a placeholder on a
// BOOL — a flag that takes no argument at all (hadron-cli#506).
//
// Nothing compiles against a usage string, and backticks read as markdown to
// anyone who has not hit this, so the mistake looks correct where it is made.
// The only check that works is the rendered shape.

// backquoted finds the placeholder cobra would take from a usage string: the
// first back-quoted run, per pflag's own scan.
//
// It exists instead of comparing pflag.UnquoteUsage's result to the flag's
// type, which was the first version and had a hole: UnquoteUsage returns the
// TYPE NAME when no backticks are present, so a usage that back-quotes
// `stringArray` on a stringArray flag produced a name equal to the type and
// skipped every check (PR #509 review, @codex). The question is whether the
// author WROTE a placeholder, not whether the result happens to look like one.
var backquoted = regexp.MustCompile("`([^`]*)`")

// A metavariable: a lowercase word naming the argument. Permits the hyphen in
// `data-key`; rejects `-`, `Spec:`, and anything capitalised or punctuated.
var metavar = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func TestFlagUsagePlaceholdersAreDeliberate(t *testing.T) {
	f, _ := testFactory(t)
	checked, seen := 0, map[string]bool{}

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		check := func(fl *pflag.Flag) {
			// A persistent flag is visited once per command that inherits it;
			// dedupe so one mistake is not reported thirty times.
			key := c.CommandPath() + " --" + fl.Name
			if seen[key] {
				return
			}
			seen[key] = true
			checked++

			m := backquoted.FindStringSubmatch(fl.Usage)
			if m == nil {
				return // no placeholder written, nothing to judge
			}
			name := m[1]

			// A BOOL takes no argument, so any placeholder on one is a backtick
			// meant as emphasis. `--loose Spec:` told the reader to pass
			// something the flag cannot accept.
			if fl.Value.Type() == "bool" {
				t.Errorf("`%s --%s` is a bool but advertises the argument %q — a backtick in its usage "+
					"became the placeholder:\n  %s", c.CommandPath(), fl.Name, name, fl.Usage)
				return
			}
			// Otherwise it must read as a METAVARIABLE — a lowercase noun
			// naming the argument, like path or n.
			//
			// "one word" is NOT sufficient, and I shipped that first:
			// mutation-checking against the bug that motivated this guard —
			// "(`-` reads stdin)" — showed it passing, because - is one word on
			// a string flag. It has to reject punctuation and capitals too.
			if !metavar.MatchString(name) {
				t.Errorf("`%s --%s` advertises the argument %q, which does not read as a metavariable — a "+
					"backtick in its usage became the placeholder. Use a lowercase noun, or drop the "+
					"backticks:\n  %s", c.CommandPath(), fl.Name, name, fl.Usage)
			}
		}
		// Flags() is local + inherited persistent, so a persistent flag declared
		// on a group is checked rather than skipped (PR #509 review, @copilot).
		// LocalFlags() alone left that gap.
		c.Flags().VisitAll(check)
		c.PersistentFlags().VisitAll(check)
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
