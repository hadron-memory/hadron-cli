package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// #536: a flag marked required must SAY SO in its usage string.
//
// Cobra's help template does not distinguish a required flag from an optional
// one — `MarkFlagRequired` changes the parser and nothing about `--help`.
// Measured on #533, where `--trigger` had been marked since its command shipped
// and rendered identically to the optional flags beside it. So the usage string
// is the ONLY signal a reader gets, and an unannotated required flag is
// discoverable exclusively by failing.
//
// That matters more here than in most CLIs because `--help` is documentation of
// record for the agent half of this tool's audience: it cannot infer
// requiredness from anywhere else, and each unmarked one costs a round trip.
//
// THE ASSERTION IS "mentions required", not a fixed spelling. Two forms are in
// use — a trailing `(required)` and an inline `…; required)` — and both tell the
// reader the thing that matters. Normalising the inline ones would turn
// "grantee App (ID or URN; required)" into a double-parenthetical that reads
// worse, so the rule is the property rather than the punctuation. Prefer the
// trailing `(required)` for NEW flags; it is what the majority use.
func TestRequiredFlagsSaySoInUsage(t *testing.T) {
	checked := 0
	walkCommands(NewRootCmd(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(fl *pflag.Flag) {
			ann := fl.Annotations[cobra.BashCompOneRequiredFlag]
			if len(ann) == 0 || ann[0] != "true" {
				return
			}
			checked++
			if !strings.Contains(strings.ToLower(fl.Usage), "required") {
				t.Errorf("%s --%s is marked required but its usage never says so: %q",
					cmd.CommandPath(), fl.Name, fl.Usage)
			}
		})
	})
	// FLOOR the count, and mutation-check the floor itself. Without it the walk
	// degrades to green-and-empty when the tree is restructured or cobra renames
	// the annotation key — a check that passes by reaching nothing, which is the
	// failure mode this repo keeps catching in its own guards.
	//
	// 80 flags were marked required when this landed; 70 leaves room for a
	// command to be retired without a false alarm, while still catching a walk
	// that silently stops covering the tree.
	if checked < 70 {
		t.Errorf("only %d required flags reached; the walk is not covering the command tree", checked)
	}
}
