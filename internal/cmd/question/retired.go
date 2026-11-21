package question

// hadron decision was renamed to hadron question (hadron-server#1641 slice S1,
// hadron-cli#2465): a clean break by ruling, no alias. This one stub keeps an old
// script or habit from meeting "unknown command": every spelling of it, with any
// old flag, prints the retirement sentence, exits 2 and does nothing else: no
// config, credential or binding read, no request. Flag parsing is switched off so
// an old flag (--urgency, --context, --log, anything) reaches the message instead
// of an "unknown flag" error, and no argument is echoed.

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// RetiredDecisionMessage is the whole answer; plain text with no format verbs.
const RetiredDecisionMessage = "hadron decision was renamed to hadron question. Nothing was sent."

// The Factory is deliberately unused: a stub resolves nothing through it.
func NewCmdRetiredDecision(_ *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "decision",
		Short: "Renamed to hadron question: exits 2 with a notice",
		Long: `hadron decision was renamed to hadron question, with the same
verbs plus answer, retract and log list. This stub is kept so that an old script
or habit gets this answer rather than "unknown command". Any arguments and old
flags are ignored (--json too: the notice is always plain text); it reads no config
or credentials and makes no request.`,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Flag parsing is off, so --help would otherwise reach the sentence; a lone
			// --help or -h still shows this page (the help inventory reads it).
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			return exitcode.Newf(exitcode.Usage, "%s", RetiredDecisionMessage)
		},
	}
}
