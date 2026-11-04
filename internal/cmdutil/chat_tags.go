package cmdutil

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// ChatTagFlags is only the client's presence/conflict policy. The server owns
// metadata access, head identity and filtering; values are never resolved here.
type ChatTagFlags struct {
	head     string
	untagged bool
	tagged   bool
}

const ChatTagHelp = `
TEAM TAGS. --team-head <id> sends a stable head ID exactly as given (not a
name or label). --untagged asks for rows with no stored head; --tagged asks for
rows with at least one. Choose only one of these three filters. The boolean
switches take no false form: --tagged=false and --untagged=false are usage
errors. A tag filter cannot be combined with --thread. Other read cursors and
mention filters still compose; filtering happens on the server, never here.

Tags appear only where the deployment stamps them. Text shows nonempty tag
labels; --json adds teamTags: [{headId,label}], [] for known untagged, or null
for historical/withheld metadata. Key on headId, never the display label.
A tag filter does not grant metadata access or select a head when posting.
TEAM_CHAT_TEAM_HEAD_INVALID is uniform exit 4; TEAM_CHAT_TEAM_METADATA_FORBIDDEN
is exit 8, and BAD_USER_INPUT exit 2. A refusal is never an empty transcript.
Tag-filtered reads never mark messages read or update a binding watermark;
nextSince is only a page cursor, not evidence that the whole chat was read.
`

func (f *ChatTagFlags) Bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.head, "team-head", "", "only messages stamped with this stable head ID (sent verbatim; not a name or label)")
	cmd.Flags().BoolVar(&f.untagged, "untagged", false, "only messages with no stored team head; cannot combine with --tagged, --team-head or --thread")
	cmd.Flags().BoolVar(&f.tagged, "tagged", false, "only messages with a stored team head; sends untagged:false, never drops the filter")
	cmd.Long += ChatTagHelp
}

func (f *ChatTagFlags) Validate(cmd *cobra.Command) error {
	n := 0
	for _, name := range []string{"team-head", "untagged", "tagged"} {
		if cmd.Flags().Changed(name) {
			n++
		}
	}
	if n > 1 {
		return exitcode.Newf(exitcode.Usage, "pass only one of --team-head, --untagged or --tagged")
	}
	if n > 0 && cmd.Flags().Changed("thread") {
		return exitcode.Newf(exitcode.Usage, "--thread cannot be combined with a team tag filter")
	}
	// Only blankness is checked here. The cleared #936 wire contract requires
	// every nonblank ID to be forwarded raw; normalization belongs to the server.
	if cmd.Flags().Changed("team-head") && strings.TrimSpace(f.head) == "" {
		return exitcode.Newf(exitcode.Usage, "--team-head requires a stable head ID")
	}
	if (cmd.Flags().Changed("untagged") && !f.untagged) || (cmd.Flags().Changed("tagged") && !f.tagged) {
		return exitcode.Newf(exitcode.Usage, "--untagged=false and --tagged=false are not accepted; omit the flag or choose its opposite")
	}
	return nil
}

func (f *ChatTagFlags) Active() bool { return f.head != "" || f.untagged || f.tagged }
func (f *ChatTagFlags) Head() *string {
	if f.head == "" {
		return nil
	}
	return &f.head
}
func (f *ChatTagFlags) Untagged() *bool {
	if !f.untagged && !f.tagged {
		return nil
	}
	v := f.untagged
	return &v
}
