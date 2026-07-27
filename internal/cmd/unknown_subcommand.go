package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// checkUnknownSubcommand reports a usage error when args name a
// subcommand that does not exist under a group command.
//
// Cobra cannot do this for us. A group command — one that only
// dispatches to children and has no Run of its own — is not Runnable,
// and inside Command.execute both the --help branch and the !Runnable
// branch return before ValidateArgs is reached. An Args validator on
// the group therefore never runs, so `hadron spec update` prints the
// `hadron spec` help and exits 0 as if it had worked (#232). Only the
// root command is safe today, because cobra's legacyArgs special-cases
// it.
//
// It builds its own command tree rather than taking one: telling
// positional args apart from flag values means parsing flags, and
// doing that on the tree that then executes would pre-populate its
// factory from the probe's parse.
//
// jsonOut reports whether --json was passed. The probe runs before
// cobra binds that flag, so it has to hand the answer back for the
// error to be rendered in the shape the --json contract promises.
func checkUnknownSubcommand(args []string) (jsonOut bool, err error) {
	target, rest, err := NewRootCmd(cmdutil.NewFactory()).Find(args)
	if err != nil || target == nil {
		// Cobra's own dispatch already reports this, and the root
		// handler maps its "unknown command" error to exit 2.
		return false, nil
	}
	if !target.HasSubCommands() || target.Runnable() {
		return false, nil
	}

	// Let pflag split flags from positionals — it knows which flags
	// take a value. --help is registered lazily during execute, so
	// register it now or `spec update --help` fails to parse here and
	// slips through as the bare-help case.
	target.InitDefaultHelpFlag()
	flags := target.Flags()
	if err := flags.Parse(rest); err != nil {
		// A malformed flag is cobra's error to report, not ours.
		return false, nil
	}
	positional := flags.Args()
	if len(positional) == 0 {
		// Bare `hadron spec` or `hadron spec --help`: printing the
		// group's help and exiting 0 is the correct behaviour.
		return false, nil
	}
	jsonOut, _ = flags.GetBool("json")
	return jsonOut, unknownSubcommandErr(target, positional[0])
}

// unknownSubcommandErr renders the "unknown command" usage error,
// appending cobra's suggestions when it has any. Distance-based
// matching only catches typos, so intuitive-but-wrong verbs are wired
// up explicitly with a command's SuggestFor (e.g. spec edit answers
// for "update").
func unknownSubcommandErr(cmd *cobra.Command, typed string) error {
	msg := fmt.Sprintf("unknown command %q for %q", typed, cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(typed); len(suggestions) > 0 {
		msg += fmt.Sprintf("; did you mean %s?", orList(suggestions))
	}
	return exitcode.Newf(exitcode.Usage, "%s", msg)
}

// orList renders names as a quoted, human-readable alternation:
// `"edit"`, `"edit" or "extract"`, `"a", "b" or "c"`.
func orList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	switch len(quoted) {
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}
