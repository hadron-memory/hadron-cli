package cmd

import (
	"errors"
	"regexp"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3029 (#2898 policy): the framework's own refusals quote what the operator typed. pflag says
// `unknown flag: --<typed>` and `invalid argument "<typed>" for "--limit" flag: strconv…: parsing
// "<typed>"`, cobra says `unknown command "<typed>" for "hadron"`, and a pasted secret lands in any of
// them. These are rewritten to name what is wrong without the value; exit 2 is kept.

// flagErrorWithoutValue is the root's FlagErrorFunc (inherited by every command). pflag reports its
// failures as typed errors, so each is matched by type, never by text.
func flagErrorWithoutValue(cmd *cobra.Command, err error) error {
	var notExist *pflag.NotExistError
	var invalid *pflag.InvalidValueError
	var syntax *pflag.InvalidSyntaxError
	switch {
	case errors.As(err, &notExist):
		return exitcode.Newf(exitcode.Usage,
			"unknown flag (the flag given is not shown) — `%s --help` lists the flags it takes", cmd.CommandPath())
	case errors.As(err, &invalid):
		// The parse error underneath (strconv's `parsing "<typed>"`) is never appended: it embeds the value,
		// %q-escaped, so a value with a quote or backslash would not even match a substring check. The CLI
		// has no flag type of its own whose reason is worth keeping; the expected type says what is wrong.
		if fl := invalid.GetFlag(); fl != nil {
			return exitcode.Newf(exitcode.Usage, "invalid value for --%s: expected %s (the value given is not shown)", fl.Name, fl.Value.Type())
		}
		return exitcode.Newf(exitcode.Usage, "invalid value for the flag (the value given is not shown)")
	case errors.As(err, &syntax):
		return exitcode.Newf(exitcode.Usage,
			"bad flag syntax (the argument given is not shown) — a flag is --name or -n; `%s --help` lists them", cmd.CommandPath())
	}
	// ValueRequiredError ("flag needs an argument: --limit") names only a flag that exists.
	return exitcode.New(exitcode.Usage, err)
}

// cobra builds these with %q in legacyArgs and OnlyValidArgs (`… "<typed>" for "<command path>"`,
// then its suggestions, which are command names). The quoted value is %q-escaped, so it is matched as a
// Go quoted string.
var cobraQuotedArgError = regexp.MustCompile(`^(unknown command|invalid argument) "(?:[^"\\]|\\.)*" for "([^"]*)"`)

// withoutEchoedCommand rewrites cobra's positional-argument refusals at the one place every error leaves
// the CLI (renderError): cobra raises them inside Find, before any command code runs.
//
// Only cobra's own uncoded errors are rewritten: an error the CLI already coded (exitcode.CodedError)
// is left alone whatever its text, and the rewrite keeps the exit code the original would have had.
func withoutEchoedCommand(err error) error {
	var coded *exitcode.CodedError
	if errors.As(err, &coded) {
		return err
	}
	msg := err.Error()
	m := cobraQuotedArgError.FindStringSubmatch(msg)
	if m == nil {
		return err
	}
	return exitcode.Newf(exitCodeFor(err), "%s for %q (the name given is not shown)%s", m[1], m[2], msg[len(m[0]):])
}
