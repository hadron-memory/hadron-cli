// Package question is the `hadron question` group: question logs and the
// questions asked in them (hadron-server #1641; hadron-cli#2465, which replaces
// the `decision` group of hadron-cli#1335).
//
// Every verb is exactly one call. References go to the server exactly as typed,
// and who may ask, amend, answer, retract, withdraw, purge or change deciders is
// the server's rule (cor:api:240:01): the CLI refuses only usage mistakes, before
// any call, and maps every server refusal through api.MapError.
package question

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdQuestion is the group constructor, wired in internal/cmd/root.go.
func NewCmdQuestion(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "question",
		Short: "Ask, read, answer, amend and withdraw questions in a question log",
		Long: `A question log is a node in a Memory that issues question codes (QA, QB, …) and
names who decides. A question asks for one answer from its deciders: a choice
among options (each with why to choose it, why not, and its risks), or free text.
Several answers can follow one another: a decider may take an answer back and
give another, and every entry stays on the question.

The server derives a question's state on every read. Rely on a question as a
ruling only while its state is DECIDED. A priority is LOW, NORMAL, HIGH or
URGENT; a tier is ANSWERED, APPROVED or SIGNED.

Each command makes one server call and passes references exactly as typed (ID
or URN). The server decides who may do what; a refusal exits with its code.
This group replaces hadron decision, which was renamed.`,
	}
	cmd.AddCommand(
		newCmdLog(f),
		newCmdAsk(f),
		newCmdGet(f),
		newCmdList(f),
		newCmdAmend(f),
		newCmdAnswer(f),
		newCmdRetract(f),
		newCmdWithdraw(f),
		newCmdVerify(f),
		newCmdPurge(f),
	)
	return cmd
}

func newCmdLog(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Create a question log, find the logs of an App, read one, or change its deciders",
	}
	cmd.AddCommand(newCmdLogCreate(f), newCmdLogList(f), newCmdLogGet(f), newCmdLogSetDeciders(f))
	return cmd
}

// refArg refuses a blank positional reference before any call. TrimSpace only
// detects blank input: the reference is returned exactly as typed.
func refArg(what, v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", exitcode.Newf(exitcode.Usage, "the %s reference is empty", what)
	}
	return v, nil
}

// requireFlag refuses a missing required flag before any call.
func requireFlag(cmd *cobra.Command, name string) error {
	if !cmd.Flags().Changed(name) {
		return exitcode.Newf(exitcode.Usage, "--%s is required", name)
	}
	return nil
}

// requireReason refuses a missing or blank --reason before any call.
func requireReason(cmd *cobra.Command, reason string) error {
	if err := requireFlag(cmd, "reason"); err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		return exitcode.Newf(exitcode.Usage, "--reason is empty")
	}
	return nil
}

func optString(cmd *cobra.Command, name, val string) *string {
	if cmd.Flags().Changed(name) {
		return &val
	}
	return nil
}

// --- question log create | list | get | set-deciders ---

func newCmdLogCreate(f *cmdutil.Factory) *cobra.Command {
	var loc, name, prefix string
	var deciders, eligible []string
	cmd := &cobra.Command{
		Use:   "create <memory> --loc <loc> --decider <user-id>... [--eligible <user-id>...] [--name <name>] [--prefix <prefix>]",
		Short: "Create a question log in a Memory",
		Long: `Create a question log at --loc in a Memory. --decider names the default
deciders by user ID (hadron user search lists them; repeat it or separate with
commas); each must be a user who can write the Memory. --eligible names who else a question may name as a
decider; omitted, only the default deciders are eligible. --prefix sets the code prefix (Q gives QA, QB, …);
omitted, the server uses Q.

The server decides who may create a log (management rights on the Memory, or
the owner role in its App) and refuses encrypted, git-mirrored, system and
anonymous Memories.`,
		Example: `  hadron question log create hrn:mem:acme.com:eng --loc questions --decider u_123 --prefix R`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memoryRef, err := refArg("memory", args[0])
			if err != nil {
				return err
			}
			if err := requireFlag(cmd, "loc"); err != nil {
				return err
			}
			if len(deciders) == 0 {
				return exitcode.Newf(exitcode.Usage, "--decider is required (at least one default decider)")
			}
			var eligiblePtr []string
			if cmd.Flags().Changed("eligible") {
				eligiblePtr = eligible
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateQuestionLog(cmd.Context(), client, memoryRef, loc,
				optString(cmd, "name", name), optString(cmd, "prefix", prefix), deciders, eligiblePtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateQuestionLog == nil {
				return exitcode.Newf(exitcode.Error, "server returned no question log")
			}
			return writeLog(f, questionLogFrom(&resp.CreateQuestionLog.QuestionLogFields), "✓ created question log")
		},
	}
	cmd.Flags().StringVar(&loc, "loc", "", "the log's loc in the Memory (required)")
	cmd.Flags().StringSliceVar(&deciders, "decider", nil, "a default decider, by user ID (required; repeat or comma-separate)")
	cmd.Flags().StringSliceVar(&eligible, "eligible", nil, "another eligible decider, by user ID (repeat or comma-separate)")
	cmd.Flags().StringVar(&name, "name", "", "the log's name (omitted: the server default)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "the code prefix (omitted: Q)")
	return cmd
}

func newCmdLogList(f *cmdutil.Factory) *cobra.Command {
	var app, memory string
	cmd := &cobra.Command{
		Use:     "list (--app <app> | --memory <memory>)",
		Aliases: []string{"ls"},
		Short:   "Find the question logs of an App or a Memory",
		Long: `List the question logs of one App (--app) or one Memory (--memory): how to find
the log to ask a question in. An unknown App or Memory gives an empty list, not
an error, so an empty list is not proof that none exists. Give exactly one of
--app and --memory.`,
		Example: `  hadron question log list --app hrn:app:acme.com:ops
  hadron question log list --memory hrn:mem:acme.com:eng --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			switch {
			case fl.Changed("app") && fl.Changed("memory"):
				return exitcode.Newf(exitcode.Usage, "give --app or --memory, not both")
			case !fl.Changed("app") && !fl.Changed("memory"):
				return exitcode.Newf(exitcode.Usage, "give --app <app> or --memory <memory>")
			}
			var appPtr, memoryPtr *string
			if fl.Changed("app") {
				if _, err := refArg("app", app); err != nil {
					return err
				}
				appPtr = &app
			} else {
				if _, err := refArg("memory", memory); err != nil {
					return err
				}
				memoryPtr = &memory
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.QuestionLogs(cmd.Context(), client, appPtr, memoryPtr)
			if err != nil {
				return api.MapError(err)
			}
			logs := []questionLogDTO{}
			if resp != nil {
				for _, l := range resp.QuestionLogs {
					if l != nil {
						logs = append(logs, questionLogFrom(&l.QuestionLogFields))
					}
				}
			}
			return output.Write(f.IOStreams, f.JSON, logs, func(w io.Writer) error {
				if len(logs) == 0 {
					_, err := fmt.Fprintln(w, "no question logs")
					return err
				}
				t := output.NewTable(w, "LOG", "PREFIX", "CODES", "REV", "URN")
				for _, l := range logs {
					t.Row(output.SafeCell(l.Name), output.SafeCell(l.Prefix), fmt.Sprint(l.LastCode), fmt.Sprint(l.Revision), output.SafeCell(l.URN))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "the App, by ID or URN")
	cmd.Flags().StringVar(&memory, "memory", "", "the Memory, by ID or URN")
	return cmd
}

func newCmdLogGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "get <log>",
		Short:   "Show a question log's settings",
		Example: `  hadron question log get hrn:node:acme.com:eng:questions --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logRef, err := refArg("log", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.QuestionLog(cmd.Context(), client, logRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.QuestionLog == nil {
				return exitcode.Newf(exitcode.NotFound, "question log %s not found (or not visible to you)", logRef)
			}
			return writeLog(f, questionLogFrom(&resp.QuestionLog.QuestionLogFields), "")
		},
	}
}

func newCmdLogSetDeciders(f *cmdutil.Factory) *cobra.Command {
	var deciders, eligible []string
	var expected int
	var reason string
	cmd := &cobra.Command{
		Use:   "set-deciders <log> --decider <user-id>... [--eligible <user-id>...] --expected-revision <n> --reason <text>",
		Short: "Replace a question log's deciders",
		Long: `Replace a log's default deciders (and, with --eligible, its other eligible
deciders), by user ID (hadron user search lists them), as an audited revision of the log. Questions already asked keep
their deciders. Without --eligible the other eligible deciders are kept;
--eligible "" clears them. --expected-revision is the revision you read
(question log get shows it); a different current revision is refused and
nothing changes.`,
		Example: `  hadron question log set-deciders hrn:node:acme.com:eng:questions --decider u_123 --expected-revision 4 --reason "Ada took over"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logRef, err := refArg("log", args[0])
			if err != nil {
				return err
			}
			if len(deciders) == 0 {
				return exitcode.Newf(exitcode.Usage, "--decider is required (at least one default decider)")
			}
			if err := requireFlag(cmd, "expected-revision"); err != nil {
				return err
			}
			if err := requireReason(cmd, reason); err != nil {
				return err
			}
			var eligiblePtr []string
			if cmd.Flags().Changed("eligible") {
				eligiblePtr = eligible
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetQuestionLogDeciders(cmd.Context(), client, logRef, deciders, eligiblePtr, expected, reason)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetQuestionLogDeciders == nil {
				return exitcode.Newf(exitcode.Error, "server returned no question log")
			}
			return writeLog(f, questionLogFrom(&resp.SetQuestionLogDeciders.QuestionLogFields), "✓ set deciders of")
		},
	}
	cmd.Flags().StringSliceVar(&deciders, "decider", nil, "a default decider, by user ID (required; repeat or comma-separate)")
	cmd.Flags().StringSliceVar(&eligible, "eligible", nil, "another eligible decider, by user ID (omitted: unchanged; --eligible \"\" clears them; repeat or comma-separate)")
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the log revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the deciders change (required)")
	return cmd
}

// --- question ask | amend ---

// inputFlags are the shared ask/amend field flags; each, when given, sets the
// field of the same name and overrides the file's.
type inputFlags struct {
	file, title, background, tier, priority, assignee string
}

func addInputFlags(cmd *cobra.Command, in *inputFlags) {
	cmd.Flags().StringVar(&in.file, "input-file", "", `a JSON object of input fields ("-" reads piped stdin); unknown, mis-cased and duplicate keys are refused`)
	cmd.Flags().StringVar(&in.title, "title", "", "the question's title (overrides the file's)")
	cmd.Flags().StringVar(&in.background, "background", "", "the background for the deciders, in markdown (overrides the file's)")
	cmd.Flags().StringVar(&in.tier, "tier", "", "ANSWERED, APPROVED or SIGNED (overrides the file's; the server validates)")
	cmd.Flags().StringVar(&in.priority, "priority", "", "LOW, NORMAL, HIGH or URGENT (overrides the file's)")
	cmd.Flags().StringVar(&in.assignee, "assignee", "", "the user the question is routed to, by user ID (overrides the file's; must be one of its deciders)")
}

// fileData reads --input-file once, before any call; nil when it was not given.
// A given-but-blank --input-file is refused, not read as absent.
func (in *inputFlags) fileData(cmd *cobra.Command, f *cmdutil.Factory) ([]byte, error) {
	if !cmd.Flags().Changed("input-file") {
		return nil, nil
	}
	if strings.TrimSpace(in.file) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--input-file is empty: give a path, or - for piped stdin")
	}
	return readInputFile(f.IOStreams, in.file)
}

func newCmdAsk(f *cmdutil.Factory) *cobra.Command {
	var in inputFlags
	cmd := &cobra.Command{
		Use:   "ask <log> --input-file <path|-> [--title <t>] [--background <b>] [--priority <p>] [--tier <tier>] [--assignee <user>]",
		Short: "Ask a question in a question log",
		Long: `Ask a question. The server issues its code (the log prefix plus the next
letters) and prints the question; question get <code> --log <log> reads it back
later. --input-file is a JSON object with the fields of the server's
AskQuestionInput, passed through unchanged:

  title (required)  the question, one line
  background        why it matters, in markdown (--background)
  whyNow            why it is being asked now
  priority          LOW, NORMAL (the default), HIGH or URGENT (--priority)
  options           the choices, each {"key": "pg", "label": "Postgres",
                    "whyChoose": "...", "whyNot": "...", "risks": "..."}
                    (only key and label are required) — or "yesno": true
                    for yes and no; neither makes a free-text question
  recommendation    {"key": "pg", "reason": "we run it already"}, or
  noRecommendation  {"reason": "..."} when none is given
  deciders          decider user IDs (hadron user search lists them); narrows
                    the log's default deciders, never widens past its
                    eligible set
  assignee          a decider the question is routed to (--assignee); routing
                    only: any decider may answer
  tier              ANSWERED (the default: a decider's answer alone settles
                    it), APPROVED (it counts once a decider has approved the
                    answered question) or SIGNED (a decider's passkey signature;
                    forced when authorizes is true, and a lower tier is then
                    refused: QUESTION_TIER_TOO_LOW). Passkey signing is not
                    available yet, so a SIGNED question stays
                    ANSWERED_UNCONFIRMED
  authorizes        true when this question grants an authorization
  grant             the authorization a grant question carries, as JSON
                    (authorizes: true only)
  neededBy, defaultAction   when it is needed, and what happens if it is not
  blocks, sources, specsTouched, specsMustChange   lists of references, each
                    {"ref": "..."}, {"node": "..."} or {"spec": "..."}

--title, --background, --tier, --priority and --assignee override the file's
value. Keys are exact and case-sensitive, and a key given twice is refused.
Every field's exact shape is AskQuestionInput in the server's schema. The server
validates the object (QUESTION_INVALID names each bad field by JSON pointer) and
decides who may ask: any writer of the log's Memory.

On success the question prints as question get shows it, prefixed "✓ asked";
--json prints the same question as one object.

Exit ` + strconv.Itoa(exitcode.Unavailable) + ` is an unknown outcome: the reply never arrived, came back without the
question, or reported a server error, so the question may have been created.
Nothing is resent, and it is not retry-safe: the server gives every ask the
next code, so asking again can create a second question.

` + wrapHelp("To recover, "+AskReadBack("<log>")+".", 80),
		Example: `  cat > ask.json <<'EOF'
{"title":"Pick a database","options":[{"key":"pg","label":"Postgres","whyChoose":"we run it"},{"key":"my","label":"MySQL"}],"recommendation":{"key":"pg","reason":"we run it already"}}
EOF
  hadron question ask hrn:node:acme.com:eng:questions --input-file ask.json --priority HIGH`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logRef, err := refArg("log", args[0])
			if err != nil {
				return err
			}
			if err := requireFlag(cmd, "input-file"); err != nil {
				return err
			}
			data, err := in.fileData(cmd, f)
			if err != nil {
				return err
			}
			var input gen.AskQuestionInput
			if data != nil {
				// Decoded here, into the generated input, so the coverage matrix
				// (scripts/unboundops counts a Decode(&input)) sees every field of
				// it as reachable through the file.
				dec, err := inputDecoder(data, &input)
				if err != nil {
					return err
				}
				if err := finishInput(dec, dec.Decode(&input)); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("title") {
				input.Title = in.title
			}
			if cmd.Flags().Changed("background") {
				input.Background = &in.background
			}
			if cmd.Flags().Changed("tier") {
				t := gen.QuestionTier(in.tier)
				input.Tier = &t
			}
			if cmd.Flags().Changed("priority") {
				p := gen.QuestionPriority(in.priority)
				input.Priority = &p
			}
			if cmd.Flags().Changed("assignee") {
				if strings.TrimSpace(in.assignee) == "" {
					return exitcode.Newf(exitcode.Usage, "--assignee is empty: give a user id")
				}
				input.Assignee = &in.assignee
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			readBack := AskReadBack(cmdutil.ShellQuote(logRef))
			resp, err := gen.AskQuestion(cmd.Context(), client, logRef, &input)
			if err != nil {
				return questionWriteError(err, "askQuestion", "ask", readBack, resp != nil && resp.AskQuestion != nil)
			}
			if resp == nil || resp.AskQuestion == nil {
				return questionWriteNotConfirmed("ask", readBack, "the server returned no question", nil)
			}
			return writeQuestion(f, questionFrom(&resp.AskQuestion.QuestionFields), "✓ asked")
		},
	}
	addInputFlags(cmd, &in)
	return cmd
}

func newCmdAmend(f *cmdutil.Factory) *cobra.Command {
	var in inputFlags
	var reason string
	var expected int
	cmd := &cobra.Command{
		Use:   "amend <question> --expected-revision <n> --reason <text> [--input-file <path|->] [--title <t>] [--background <b>] [--tier <tier>] [--priority <p>] [--assignee <user>]",
		Short: "Amend a question",
		Long: `Amend a question. Only the fields you give change; the rest stay as they are.
--input-file is a JSON object with the fields of the server's AmendQuestionInput,
passed through unchanged:

  title, tier, priority, background, whyNow, assignee, neededBy, defaultAction,
  options, yesno, recommendation, noRecommendation   as for question ask (what
             the tiers mean: hadron question ask --help)
  blocks, sources, specsTouched, specsMustChange   replace that whole list; each
             entry a reference: {"ref": "<an issue or PR reference>"}, {"node": "<id or
             URN>"} or {"spec": "<citation>"}

An empty list ([]) clears that list ("options": [] makes the question free text),
and "background", "whyNow", "assignee", "neededBy" or "defaultAction": null clears
that value; leaving a key out keeps it. A null on any other key changes nothing and
is refused (exit 2) naming the key. Answers never change here (question answer and question retract do that).
Keys are exact and case-sensitive, and a key given twice is refused.
--title, --background, --tier, --priority and --assignee set that field
(overriding the file).

--expected-revision is the revision you read (question get shows it). If the
question changed since, the amend is refused (NODE_WRITE_CONFLICT, exit 5): read
it again and retry with the new revision. The server decides who may amend what:
before any answer any writer, after one a decider only, and priority and tier
are a decider's to change.

On success the question prints as question get shows it, prefixed "✓ amended".

Exit 7 is an unknown outcome: the reply never arrived, was unusable, or came
with an error beneath the mutation, so the amend may have been recorded.
Nothing is resent: read the question with question get before trying again. A
refusal before the mutation ran keeps its ordinary exit, and a connection that
never opened is "could not reach the server" (also exit 7) with no claim about
the amend.`,
		Example: `  hadron question amend hrn:node:acme.com:eng:questions:QA --expected-revision 3 --reason "clarify" --title "Pick a primary database"
  cat > amend.json <<'EOF'
{"assignee":null,"specsTouched":[{"spec":"<a spec citation>"}]}
EOF
  hadron question amend hrn:node:acme.com:eng:questions:QA --expected-revision 3 --reason "reroute" --input-file amend.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			if err := requireFlag(cmd, "expected-revision"); err != nil {
				return err
			}
			if err := requireReason(cmd, reason); err != nil {
				return err
			}
			data, err := in.fileData(cmd, f)
			if err != nil {
				return err
			}
			var input gen.AmendQuestionInput
			if data != nil {
				// Decoded here, into the generated input, as in ask.
				dec, err := inputDecoder(data, &input)
				if err != nil {
					return err
				}
				if err := finishInput(dec, dec.Decode(&input)); err != nil {
					return err
				}
				if err := refuseAmendNulls(data); err != nil {
					return err
				}
			}
			fl := cmd.Flags()
			if fl.Changed("assignee") && strings.TrimSpace(in.assignee) == "" {
				return exitcode.Newf(exitcode.Usage, "--assignee is empty: give a user id (put \"assignee\": null in the file to clear it)")
			}
			if fl.Changed("title") {
				input.Title = &in.title
			}
			if fl.Changed("background") {
				raw, _ := json.Marshal(in.background) // a string never fails to marshal
				input.Background = raw
			}
			if fl.Changed("assignee") {
				raw, _ := json.Marshal(in.assignee)
				input.Assignee = raw
			}
			if fl.Changed("tier") {
				t := gen.QuestionTier(in.tier)
				input.Tier = &t
			}
			if fl.Changed("priority") {
				p := gen.QuestionPriority(in.priority)
				input.Priority = &p
			}
			if amendIsEmpty(&input) {
				return errNothingToAmend()
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			readBack := readBackQuestion(ref)
			resp, err := gen.AmendQuestion(cmd.Context(), client, ref, &input, expected, reason)
			if err != nil {
				return questionWriteError(err, "amendQuestion", "amend", readBack, resp != nil && resp.AmendQuestion != nil)
			}
			if resp == nil || resp.AmendQuestion == nil {
				return questionWriteNotConfirmed("amend", readBack, "the server returned no question", nil)
			}
			return writeQuestion(f, questionFrom(&resp.AmendQuestion.QuestionFields), "✓ amended")
		},
	}
	addInputFlags(cmd, &in)
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the question revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it changes (required)")
	return cmd
}

// amendIsEmpty reports an amend that would change nothing: every field omitted.
// The tri-state values (raw: a literal null clears) and the lists (an explicit []
// clears) count by PRESENCE, not length: {"blocks":[]} is a real change.
func amendIsEmpty(in *gen.AmendQuestionInput) bool {
	return in.Title == nil && in.Tier == nil && in.Priority == nil && in.Yesno == nil &&
		in.Background == nil && in.WhyNow == nil && in.Assignee == nil && in.NeededBy == nil && in.DefaultAction == nil &&
		in.Options == nil && in.Recommendation == nil && in.NoRecommendation == nil &&
		in.Blocks == nil && in.Sources == nil && in.SpecsTouched == nil && in.SpecsMustChange == nil
}

// errNothingToAmend is the refusal for an amend that would change nothing.
func errNothingToAmend() error {
	return exitcode.Newf(exitcode.Usage, "nothing to amend: give --input-file with a field, or --title, --background, --tier, --priority or --assignee")
}

// --- question answer | retract ---

const answerVia = gen.QuestionAnswerViaCli

func newCmdAnswer(f *cmdutil.Factory) *cobra.Command {
	var expected int
	var option, text, textFile, reason string
	cmd := &cobra.Command{
		Use:   "answer <question> --expected-revision <n> [--option <key>] [--text <text> | --text-file <path|->] [--reason <text>]",
		Short: "Answer a question",
		Long: `Answer a question with one of its options (--option <key>), with free text
(--text, or --text-file for a file or - for piped stdin), or both: the text is
then a note beside the chosen option. Give at least one. --reason says why this
answer replaces an earlier one. The command records where the answer was given
(the command line) itself; it is not a flag.

--expected-revision is required and is the revision question get prints, so the
answer covers what you read: a question that changed since is refused
(NODE_WRITE_CONFLICT, exit 5), read it again. This command makes one server call
and adds no hidden read.

Only one of the question's deciders can answer, acting as themselves. The server
refuses a worker session, an agent session and an App key (QUESTION_NOT_DECIDER,
exit 8): an agent does not answer its own question. To answer from a worktree
bound to a worker, use the portal, or a shell whose session is bound to no worker.
An option key the question does not have is refused (QUESTION_UNKNOWN_OPTION,
exit 2); a withdrawn question is refused (QUESTION_WITHDRAWN, exit 5).

On success it prints "✓ answered <code>: <option key or (text)>" and the question
as question get shows it; --json prints the question as one object. Success is
claimed only when the server's reply shows an answer written for this call: an
answer (not a retraction) with the option key you gave, on a revision that did not
go back, and a newer revision when you gave a reason. The text and the reason are
not compared (the server normalises them): when they differ from what you gave, a
note on stderr says so.

Exit 1 is also the answer when the server replied but its reply does not show
that outcome: nothing is printed on stdout, the call is NOT resent, and the error
tells you to read the question with question get before trying again. Exit 7
is the answer when the reply never arrived, was unusable (a null question), or
came with an error beneath the mutation (a coded error on a field of the returned
question, which may follow the write): the same sentence, nothing resent. What the
server refused before running the mutation keeps its ordinary exit: a coded refusal of
the mutation itself (2, 4, 5, 8) and a plain HTTP 400, 401, 403, 404, 405, 413, 415 or
429 (401 is 3, 404 is 4, the others 1 with the server's message). 408, 409, any other 4xx
or 5xx, a dropped connection, and any reply whose body shows the question or an error
below the mutation are the unknown ones above. A connection that never opened is "could
not reach the server" (7) with no claim about the answer. In the unknown cases the answer
may have been recorded.`,
		Example: `  hadron question answer hrn:node:acme.com:eng:questions:QA --expected-revision 3 --option pg
  hadron question answer hrn:node:acme.com:eng:questions:QA --expected-revision 4 --option my --reason "the team prefers it now" --text "MySQL, with a read replica"
  echo "needs a different approach" | hadron question answer hrn:node:acme.com:eng:questions:QA --expected-revision 3 --text-file -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			if err := requireFlag(cmd, "expected-revision"); err != nil {
				return err
			}
			if fl.Changed("text") && fl.Changed("text-file") {
				return exitcode.Newf(exitcode.Usage, "give --text or --text-file, not both")
			}
			if !fl.Changed("option") && !fl.Changed("text") && !fl.Changed("text-file") {
				return exitcode.Newf(exitcode.Usage, "give --option <key> or a text (--text or --text-file); nothing was sent")
			}
			if fl.Changed("option") && strings.TrimSpace(option) == "" {
				return exitcode.Newf(exitcode.Usage, "--option is empty: give an option key, or omit it for a free-text answer")
			}
			input := gen.AnswerQuestionInput{}
			via := answerVia
			input.Via = &via
			if fl.Changed("option") {
				input.OptionKey = &option
			}
			switch {
			case fl.Changed("text"):
				input.Text = &text
			case fl.Changed("text-file"):
				data, err := readText(f.IOStreams, textFile)
				if err != nil {
					return err
				}
				s := string(data)
				input.Text = &s
			}
			if fl.Changed("reason") {
				input.Reason = &reason
			}
			// A blank text names no answer (the server treats it as absent): without an
			// option it would reach the mutation only to be refused, so refuse it here.
			if input.OptionKey == nil && (input.Text == nil || normalizeAnswerText(*input.Text) == "") {
				return exitcode.Newf(exitcode.Usage, "the text is blank, so this names no answer: give --option <key> or a text with something in it; nothing was sent")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AnswerQuestion(cmd.Context(), client, ref, &input, expected)
			if err != nil {
				return questionWriteError(err, "answerQuestion", "answer", readBackQuestion(ref), resp != nil && resp.AnswerQuestion != nil)
			}
			if resp == nil || resp.AnswerQuestion == nil {
				return questionWriteNotConfirmed("answer", readBackQuestion(ref), "the server returned no question", nil)
			}
			q := questionFrom(&resp.AnswerQuestion.QuestionFields)
			// Success is only claimed when the returned question carries a standing
			// answer: a reply without one does not confirm the answer, so the
			// outcome is unconfirmed (in either output mode) and must be read back
			// before retrying.
			if !answerConfirmed(q, input, expected) {
				return exitcode.Newf(exitcode.Error, "the server's reply does not show the answer you gave as the standing answer, so the outcome of this answer is unconfirmed; read it with `hadron question get %s` before retrying", cmdutil.ShellQuote(ref))
			}
			// The heading names the standing answer the server RETURNED, not what was
			// requested.
			given := answerHeading(q.Answer)
			for _, n := range answerNotes(q, input) {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %s\n", n)
			}
			return output.Write(f.IOStreams, f.JSON, q, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ answered %s: %s\n", output.SafeCell(q.Code), given); err != nil {
					return err
				}
				return writeQuestionBlock(w, q, "")
			})
		},
	}
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the question revision you read (required)")
	cmd.Flags().StringVar(&option, "option", "", "the option key to choose")
	cmd.Flags().StringVar(&text, "text", "", "a free-text answer, or a note beside the chosen option")
	cmd.Flags().StringVar(&textFile, "text-file", "", "read the text from a file (path) or - for piped stdin")
	cmd.Flags().StringVar(&reason, "reason", "", "why this answer replaces an earlier one")
	return cmd
}

func newCmdRetract(f *cmdutil.Factory) *cobra.Command {
	var expected int
	var reason string
	cmd := &cobra.Command{
		Use:   "retract <question> --expected-revision <n> [--reason <text>]",
		Short: "Take a question's answer back",
		Long: `Take the standing answer back: the question is pending again, and the retraction
stays on its answer list. --expected-revision is the revision question get
prints, as for question answer; this command makes one server call. The command
records where it was given (the command line) itself.

Only one of the question's deciders can retract, acting as themselves: the server
refuses a worker session, an agent session and an App key (QUESTION_NOT_DECIDER,
exit 8). A question with no standing answer is refused (QUESTION_NOT_ANSWERED,
exit 5).

On success it prints "✓ answer taken back; <code> is pending again" and the
question; --json prints the question as one object. Success is claimed only when
the server's reply shows the retraction: no standing answer, a retraction as the
last answer entry, and a revision newer than the one you read.

Exit 1 is also the answer when the server replied but its reply does not show
that: nothing is printed on stdout, the call is NOT resent, and the error tells you
to read the question with question get before trying again. Exit 7 is the
answer when the reply never arrived, was unusable (a null question), or came with
an error beneath the mutation (which may follow the write): the same sentence,
nothing resent. What the server refused before running the mutation keeps its
ordinary exit: a coded refusal of the mutation itself (2, 4, 5, 8) and a plain HTTP
400, 401, 403, 404, 405, 413, 415 or 429 (401 is 3, 404 is 4, the others 1 with the
server's message). 408, 409, any other 4xx or 5xx, a dropped connection, and any reply
whose body shows the question or an error below the mutation are the unknown ones
above. A connection that never opened is "could not reach the server" (7) with no claim
about the retraction. In the unknown cases the retraction may have been recorded.`,
		Example: `  hadron question retract hrn:node:acme.com:eng:questions:QA --expected-revision 4 --reason "wrong click"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			if err := requireFlag(cmd, "expected-revision"); err != nil {
				return err
			}
			via := answerVia
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RetractQuestionAnswer(cmd.Context(), client, ref, expected, optString(cmd, "reason", reason), &via)
			if err != nil {
				return questionWriteError(err, "retractQuestionAnswer", "retraction", readBackQuestion(ref), resp != nil && resp.RetractQuestionAnswer != nil)
			}
			if resp == nil || resp.RetractQuestionAnswer == nil {
				return questionWriteNotConfirmed("retraction", readBackQuestion(ref), "the server returned no question", nil)
			}
			q := questionFrom(&resp.RetractQuestionAnswer.QuestionFields)
			// "Pending again" is only claimed when the returned question has no
			// standing answer; otherwise the outcome is unconfirmed.
			if !retractionConfirmed(q, expected) {
				return exitcode.Newf(exitcode.Error, "the server's reply does not show the retraction (no standing answer, a retraction entry last, a newer revision), so the outcome is unconfirmed; read it with `hadron question get %s` before retrying", cmdutil.ShellQuote(ref))
			}
			return output.Write(f.IOStreams, f.JSON, q, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ answer taken back; %s is pending again\n", output.SafeCell(q.Code)); err != nil {
					return err
				}
				return writeQuestionBlock(w, q, "")
			})
		},
	}
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the question revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the answer is taken back")
	return cmd
}

// --- question get | list | verify ---

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	var logRef string
	cmd := &cobra.Command{
		Use:   "get <question> | get <code> --log <log>",
		Short: "Show a question",
		Long: `Show a question by ID or URN, or by its code (QA) within --log. The revision it
prints is the --expected-revision of the next amend, answer, retract or withdraw.`,
		Example: `  hadron question get hrn:node:acme.com:eng:questions:QA
  hadron question get QA --log hrn:node:acme.com:eng:questions --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			var ref, code, logPtr *string
			if cmd.Flags().Changed("log") {
				if _, err := refArg("log", logRef); err != nil {
					return err
				}
				code, logPtr = &arg, &logRef
			} else {
				ref = &arg
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.Question(cmd.Context(), client, ref, code, logPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Question == nil {
				return exitcode.Newf(exitcode.NotFound, "question %s not found (or not visible to you)", arg)
			}
			return writeQuestion(f, questionFrom(&resp.Question.QuestionFields), "")
		},
	}
	cmd.Flags().StringVar(&logRef, "log", "", "read the argument as a code within this question log")
	return cmd
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var app, logRef, tier, sort, assignee, askedByWorker, askedByApp, answeredSince string
	var states, priorities []string
	var awaitingMe, authorizes, answered, unassigned, askedByMe bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list (--app <app> | --log <log> | --awaiting-me) [--answered[=false]] [--state <s>...] [--priority <p>...] [--tier <t>] [--assignee <user>] [--unassigned] [--asked-by-me] [--asked-by-worker <id>] [--asked-by-app <id>] [--answered-since <time>] [--authorizes[=false]] [--sort <s>] [--limit <n>] [--offset <n>]",
		Aliases: []string{"ls"},
		Short:   "List the questions of an App or a log, or those awaiting you",
		Long: `List the questions of one App (--app: every question in the Memories of that App
that you can read), of one log (--log), or with --awaiting-me the open questions
you decide across every Memory you can read. Give at least one of the three (--awaiting-me
may stand alone or sit beside --app or --log), and not --app with --log. One page per call: --limit (server default 20) and --offset page
through; total counts every match. The server derives each state before
filtering; a list above 5000 candidates refuses a filter
(QUESTION_LOG_TOO_LARGE_FOR_FILTER).

--app, --log and --awaiting-me are the SCOPE; everything below is a filter on it.
Every filter is sent only when its flag was given:
  --answered           answered (ANSWERED_UNCONFIRMED, DECIDED or STALE);
                       --answered=false keeps the OPEN ones (no standing answer)
  --state, --priority  one or more values (repeat or comma-separate)
  --tier               ANSWERED, APPROVED or SIGNED
  --assignee <user>    the question's effective assignee is this user (the
                       explicit assignee, else the holder of the worker that asked)
  --unassigned         no effective assignee; with --assignee either match is kept
                       (--unassigned=false alone keeps the ones that have one)
  --asked-by-me        you asked it
  --asked-by-worker, --asked-by-app   asked by that worker or App (by ID)
  --answered-since     answered at or after this time (RFC 3339)
  --authorizes         keeps grant questions; --authorizes=false keeps the others
  --sort               PORTAL_DEFAULT (the server default), CODE, ASKED_AT_DESC,
                       ASKED_AT_ASC, PRIORITY_DESC or ANSWERED_AT_DESC

The table is CODE STATE PRIORITY TIER ASSIGNEE REV TITLE; "N of M" goes to stderr.
ASSIGNEE is the effective assignee: the explicit one, else the holder of the worker that
asked (so it can be inferred, not named by the asker), else a dash.`,
		Example: `  hadron question list --app hrn:app:acme.com:ops --answered=false --priority HIGH,URGENT
  hadron question list --awaiting-me --json
  hadron question list --log hrn:node:acme.com:eng:questions --asked-by-me --answered-since 2026-11-01T00:00:00Z`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			switch {
			case fl.Changed("app") && fl.Changed("log"):
				return exitcode.Newf(exitcode.Usage, "give --app or --log, not both")
			case !fl.Changed("app") && !fl.Changed("log") && (!fl.Changed("awaiting-me") || !awaitingMe):
				// --awaiting-me=false names no scope: it would send a request with none.
				return exitcode.Newf(exitcode.Usage, "give --app <app>, --log <log> or --awaiting-me (--awaiting-me=false is no scope)")
			}
			for _, s := range []struct{ name, val string }{{"app", app}, {"log", logRef}} {
				if fl.Changed(s.name) {
					if _, err := refArg(s.name, s.val); err != nil {
						return err
					}
				}
			}
			for _, b := range []struct{ name, val string }{{"assignee", assignee}, {"asked-by-worker", askedByWorker}, {"asked-by-app", askedByApp}} {
				if fl.Changed(b.name) && strings.TrimSpace(b.val) == "" {
					return exitcode.Newf(exitcode.Usage, "--%s is empty: give an id", b.name)
				}
			}
			var sinceVar *string
			if fl.Changed("answered-since") {
				if _, err := time.Parse(time.RFC3339, answeredSince); err != nil {
					return exitcode.Newf(exitcode.Usage, "--answered-since takes an RFC 3339 time such as 2026-11-01T00:00:00Z: %v", err)
				}
				sinceVar = &answeredSince
			}
			boolFlag := func(name string, v *bool) *bool {
				if fl.Changed(name) {
					return v
				}
				return nil
			}
			intFlag := func(name string, v *int) *int {
				if fl.Changed(name) {
					return v
				}
				return nil
			}
			var stateVals []gen.QuestionState
			for _, s := range states {
				stateVals = append(stateVals, gen.QuestionState(s))
			}
			var priorityVals []gen.QuestionPriority
			for _, p := range priorities {
				priorityVals = append(priorityVals, gen.QuestionPriority(p))
			}
			var tierPtr *gen.QuestionTier
			if fl.Changed("tier") {
				t := gen.QuestionTier(tier)
				tierPtr = &t
			}
			var sortPtr *gen.QuestionSort
			if fl.Changed("sort") {
				s := gen.QuestionSort(sort)
				sortPtr = &s
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.Questions(cmd.Context(), client,
				optString(cmd, "app", app), optString(cmd, "log", logRef),
				boolFlag("awaiting-me", &awaitingMe), boolFlag("authorizes", &authorizes), boolFlag("answered", &answered),
				sinceVar, boolFlag("asked-by-me", &askedByMe),
				optString(cmd, "asked-by-worker", askedByWorker), optString(cmd, "asked-by-app", askedByApp),
				optString(cmd, "assignee", assignee), boolFlag("unassigned", &unassigned),
				stateVals, tierPtr, priorityVals, sortPtr, intFlag("limit", &limit), intFlag("offset", &offset))
			if err != nil {
				return api.MapError(err)
			}
			if resp.Questions == nil {
				return exitcode.Newf(exitcode.Error, "server returned no question page")
			}
			page := questionPageDTO{Total: resp.Questions.Total, Items: []questionDTO{}}
			for _, it := range resp.Questions.Items {
				if it != nil {
					page.Items = append(page.Items, questionFrom(&it.QuestionFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				t := output.NewTable(w, "CODE", "STATE", "PRIORITY", "TIER", "ASSIGNEE", "REV", "TITLE")
				for _, q := range page.Items {
					who := "—"
					switch {
					case q.EffectiveAssignee != nil:
						who = output.SafeCell(actorName(*q.EffectiveAssignee))
					case q.EffectiveAssigneeID != nil:
						who = output.SafeCell(*q.EffectiveAssigneeID)
					}
					t.Row(output.SafeCell(q.Code), q.State, q.Priority, q.Tier, who, fmt.Sprint(q.Revision), output.SafeCell(q.Title))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				_, err := fmt.Fprintf(f.IOStreams.ErrOut, "%d of %d\n", len(page.Items), page.Total)
				return err
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&app, "app", "", "every question in the Memories of this App that you can read, by ID or URN")
	fl.StringVar(&logRef, "log", "", "the question log, by ID or URN")
	fl.BoolVar(&awaitingMe, "awaiting-me", false, "open questions you decide, across every Memory you can read")
	fl.BoolVar(&answered, "answered", false, "keep answered questions (=false keeps the pending ones)")
	fl.StringSliceVar(&states, "state", nil, "keep these states: OPEN, ANSWERED_UNCONFIRMED, DECIDED, STALE, WITHDRAWN, TAMPERED, UNVERIFIABLE")
	fl.StringVar(&tier, "tier", "", "keep this tier: ANSWERED, APPROVED or SIGNED")
	fl.StringSliceVar(&priorities, "priority", nil, "keep these priorities: LOW, NORMAL, HIGH, URGENT")
	fl.StringVar(&assignee, "assignee", "", "keep questions whose effective assignee is this user id")
	fl.BoolVar(&unassigned, "unassigned", false, "keep questions with no effective assignee")
	fl.BoolVar(&askedByMe, "asked-by-me", false, "keep the questions you asked")
	fl.StringVar(&askedByWorker, "asked-by-worker", "", "keep the questions this worker asked, by ID")
	fl.StringVar(&askedByApp, "asked-by-app", "", "keep the questions this App asked, by ID")
	fl.StringVar(&answeredSince, "answered-since", "", "keep questions answered at or after this time (RFC 3339)")
	fl.BoolVar(&authorizes, "authorizes", false, "keep grant questions (=false keeps the others)")
	fl.StringVar(&sort, "sort", "", "PORTAL_DEFAULT, CODE, ASKED_AT_DESC, ASKED_AT_ASC, PRIORITY_DESC or ANSWERED_AT_DESC")
	fl.IntVar(&limit, "limit", 0, "page size (server default 20)")
	fl.IntVar(&offset, "offset", 0, "skip this many matches")
	return cmd
}

func newCmdVerify(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <question>",
		Short: "Check a question's content, data and approval against each other",
		Long: `Re-render a question from its stored payload and check it against its content,
data and approval: the findings behind its state. An empty findings list with
state DECIDED is a ruling you may rely on.`,
		Example: `  hadron question verify hrn:node:acme.com:eng:questions:QA --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.VerifyQuestion(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp.VerifyQuestion == nil {
				return exitcode.Newf(exitcode.Error, "server returned no verification")
			}
			v := resp.VerifyQuestion
			dto := verificationDTO{
				State: string(v.State), Revision: v.Revision, ApprovedRevision: v.ApprovedRevision,
				ApprovalIntegrity: v.ApprovalIntegrity, ApprovalByDecider: v.ApprovalByDecider,
				Codec: v.Codec, Renderer: v.Renderer, Findings: append([]string{}, v.Findings...),
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				approved := "—"
				if dto.ApprovedRevision != nil {
					approved = fmt.Sprint(*dto.ApprovedRevision)
				}
				findings := "none"
				if len(dto.Findings) > 0 {
					findings = output.SafeCell(strings.Join(dto.Findings, ", "))
				}
				_, err := fmt.Fprintf(w, "state: %s\nrevision: %d (approved: %s)\napproval: %s (by a decider: %t)\ncodec: %s  renderer: %s\nfindings: %s\n",
					dto.State, dto.Revision, approved, output.SafeCell(dto.ApprovalIntegrity), dto.ApprovalByDecider,
					output.SafeCell(output.Dash(dto.Codec)), output.SafeCell(output.Dash(dto.Renderer)), findings)
				return err
			})
		},
	}
}

// --- question withdraw | purge ---

func newCmdWithdraw(f *cmdutil.Factory) *cobra.Command {
	var expected int
	var reason string
	var yes bool
	cmd := &cobra.Command{
		Use:   "withdraw <question> --expected-revision <n> --reason <text> [--yes]",
		Short: "Withdraw a question (final; its code stays used)",
		Long: `Withdraw a question. This is final, and its code is never reused. The server
decides who may: the asker or a decider, and only a decider once it is answered.
Prompts on a terminal; --yes is required otherwise.

Exit 7 is an unknown outcome: the reply never arrived, was unusable, or came
with an error beneath the mutation, so the withdrawal may have been recorded.
Nothing is resent: read the question with question get before trying again. A
refusal before the mutation ran keeps its ordinary exit, and a connection that
never opened is "could not reach the server" (also exit 7) with no claim about
the withdrawal.`,
		Example: `  hadron question withdraw hrn:node:acme.com:eng:questions:QA --expected-revision 3 --reason "no longer needed" --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			if err := requireFlag(cmd, "expected-revision"); err != nil {
				return err
			}
			if err := requireReason(cmd, reason); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, "Withdraw question "+output.SafeCell(ref)+"? Withdrawal is final and its code is never reused."); err != nil {
				return err
			}
			readBack := readBackQuestion(ref)
			resp, err := gen.WithdrawQuestion(cmd.Context(), client, ref, reason, expected)
			if err != nil {
				return questionWriteError(err, "withdrawQuestion", "withdrawal", readBack, resp != nil && resp.WithdrawQuestion != nil)
			}
			if resp == nil || resp.WithdrawQuestion == nil {
				return questionWriteNotConfirmed("withdrawal", readBack, "the server returned no question", nil)
			}
			return writeQuestion(f, questionFrom(&resp.WithdrawQuestion.QuestionFields), "✓ withdrew")
		},
	}
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the question revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it is withdrawn (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdPurge(f *cmdutil.Factory) *cobra.Command {
	var reason string
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge <question> --reason <text> [--yes]",
		Short: "Delete a never-answered question (platform admins)",
		Long: `Hard-delete a question that was never answered. The purge is recorded on its
log and the code stays used. Platform admins only, by server rule; an answered
question is refused (QUESTION_ANSWERED). A question left in the older question
format (before the shape change of November 2026) counts as never answered only
while its payload holds no answer or answer history: the server refuses one that
does. Prompts on a
terminal; --yes is required otherwise.

Exit 7 is an unknown outcome: the reply never arrived, was unusable, or came
with an error beneath the mutation, so the purge may have happened. Nothing is
resent: read it with question get. A question that is not found most likely
means the purge happened (check the ref is the one you meant); one that is
still there means it did not. A refusal before the mutation ran keeps its
ordinary exit, and a connection that never opened is "could not reach the
server" (also exit 7) with no claim about the purge.`,
		Example: `  hadron question purge hrn:node:acme.com:eng:questions:QC --reason "test data" --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("question", args[0])
			if err != nil {
				return err
			}
			if err := requireReason(cmd, reason); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "question "+ref+" (purge is permanent; the server refuses it for a question that was ever answered, including one in the older question format whose payload holds an answer)"); err != nil {
				return err
			}
			// A purged question no longer exists, so reading it back is how the outcome shows:
			// "not found" means the purge happened.
			readBack := "read it with `hadron question get " + cmdutil.ShellQuote(ref) + "`: a question that is not found most likely means the purge happened (check that the ref is the one you meant), one that is still there means it did not"
			resp, err := gen.PurgeUnansweredQuestion(cmd.Context(), client, ref, reason)
			if err != nil {
				return questionWriteError(err, "purgeUnansweredQuestion", "purge", readBack, resp != nil && resp.PurgeUnansweredQuestion != nil && *resp.PurgeUnansweredQuestion)
			}
			// A null or absent answer is not "no": the purge may have run, so it is an unknown outcome.
			if resp == nil || resp.PurgeUnansweredQuestion == nil {
				return questionWriteNotConfirmed("purge", readBack, "the server returned no answer", nil)
			}
			if !*resp.PurgeUnansweredQuestion {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the purge of %s", ref)
			}
			dto := purgeDTO{Ref: ref, Purged: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ purged question %s\n", output.SafeCell(ref))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why it is purged (required; recorded on the log)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// answerHeading is the option key of a returned answer, or (text) for a free-text one.
func answerHeading(a *answerDTO) string {
	if a.Key != nil && *a.Key != "" {
		return output.SafeCell(*a.Key)
	}
	return "(text)"
}

// answerConfirmed reports whether the returned question shows an answer written for
// this call: its standing answer is an ANSWER entry (not a retraction) with the option
// key that was asked for, on a revision that did not go backwards, and a revision that
// moved when a reason was given (a reason is only ever stored by a write; the same
// answer without a reason is the one documented no-op and may return unchanged).
//
// It does NOT compare text or reason: those pass through the server's own
// normalisation, which the CLI does not copy into its gate (a trailing newline, a cap or
// a canonical form would turn a write that happened into a reported failure). A
// difference there is a note on stderr (answerNotes), never a refusal.
func answerConfirmed(q questionDTO, in gen.AnswerQuestionInput, expectedRevision int) bool {
	a := q.Answer
	if !q.Answered || a == nil || a.Kind != string(gen.QuestionAnswerKindAnswer) {
		return false
	}
	if in.OptionKey != nil && (a.Key == nil || *a.Key != *in.OptionKey) {
		return false
	}
	if in.OptionKey == nil && a.Key != nil {
		return false
	}
	if q.Revision < expectedRevision {
		return false
	}
	if in.Reason != nil && hasVisibleText(*in.Reason) && q.Revision <= expectedRevision {
		return false
	}
	return true
}

// hasVisibleText is true when s certainly holds something the server will keep as text:
// an ASCII graphic character, or a letter or digit. It is deliberately conservative (an
// exotic whitespace-only string is "unknown", and unknown is never a refusal).
func hasVisibleText(s string) bool {
	for _, r := range s {
		if (r > 0x20 && r < 0x7f) || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// answerNotes are the differences between what was given and the standing answer the
// server returned that a person may want to know about: they never fail the command.
func answerNotes(q questionDTO, in gen.AnswerQuestionInput) []string {
	a := q.Answer
	if a == nil {
		return nil
	}
	var notes []string
	given := func(p *string) string {
		if p == nil {
			return ""
		}
		return normalizeAnswerText(*p)
	}
	if want, got := given(in.Text), given(a.Text); want != got {
		notes = append(notes, "the stored text is not exactly the text you gave (the server normalises it, and a note you did not give may be an earlier one): see `question get`")
	}
	if want, got := given(in.Reason), given(a.Reason); want != "" && want != got {
		notes = append(notes, "the stored reason is not exactly the reason you gave (the server normalises it): see `question get`")
	}
	return notes
}

// normalizeAnswerText is the server's rule for an answer's text, used only to word a
// note (never to gate success): CRLF and CR become LF, and a text of only whitespace
// is no text at all.
func normalizeAnswerText(t string) string {
	t = strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(t) == "" {
		return ""
	}
	return t
}

// retractionConfirmed reports whether the returned question shows the retraction: no
// standing answer, a RETRACTION as the last entry of the answer list, and a revision
// that moved past the one the caller read.
func retractionConfirmed(q questionDTO, expectedRevision int) bool {
	if q.Answered || q.Answer != nil || len(q.Answers) == 0 {
		return false
	}
	return q.Answers[len(q.Answers)-1].Kind == string(gen.QuestionAnswerKindRetraction) && q.Revision > expectedRevision
}

// wrapHelp breaks text on spaces so no line is longer than width (a word longer than
// width stays whole). It lets a sentence shared with an error, which is one line, sit in
// help text that is kept to 80 columns.
func wrapHelp(text string, width int) string {
	var lines []string
	line := ""
	for _, w := range helpWords(text) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// helpWords splits on spaces but keeps a `backticked span` whole, so a command in the text is
// never broken across lines (it must stay copyable from --help).
func helpWords(text string) []string {
	var words []string
	cur := ""
	inCode := false
	for _, r := range text {
		switch {
		case r == '`':
			inCode = !inCode
			cur += string(r)
		case r == ' ' && !inCode:
			if cur != "" {
				words = append(words, cur)
				cur = ""
			}
		default:
			cur += string(r)
		}
	}
	if cur != "" {
		words = append(words, cur)
	}
	return words
}

// AskReadBack (exported so the cmd tests can assert against the same sentence the help and
// the error carry) is the one recovery sentence for an unknown `question ask` outcome. The ask
// help and the exit-7 error are both built from it, so they cannot word the advice
// differently. logRef is the log as it should appear in the command: the caller's ref,
// shell-quoted, in the error; the placeholder <log> in the help.
func AskReadBack(logRef string) string {
	return "list the log with `hadron question list --log " + logRef + " --json` and look for the title you sent, matching askedBy and askedAt when titles repeat (the listing is paged: use --limit and --offset) before asking again"
}

// questionWriteError maps a failed question write (ask, amend, answer, retract, withdraw,
// purge; cli#2680, cli#2854). A coded refusal at the mutation's own level (no path, or exactly
// [field]), an HTTP 400/401/403/404/405/413/415/429 whose body shows no data and no error
// below the mutation, and a connection that never opened are definitive: nothing was written,
// so the ordinary typed exit applies. When the reply carries the result beside the error
// (dataSeen) a resolver ran, so it is never definitive. A coded error BELOW the field comes
// from resolving the returned value and so may follow the write (cli#398, cli#2680), and a
// reset or timeout after the request left, a 5xx, an internal error or an unusable answer may
// follow the commit too: those are an unknown outcome, exit 7, never resent, with the
// read-back sentence.
func questionWriteError(err error, field, what, readBack string, dataSeen bool) error {
	// The server returned the question beside the error: a resolver ran and the write may
	// have followed, whatever the error says.
	if dataSeen || api.ErrorShowsData(err) {
		return questionWriteNotConfirmed(what, readBack, api.MapError(err).Error(), err)
	}
	// A refusal of the mutation itself, an HTTP answer from the allowlist in
	// api.RequestNotProcessed (a refused credential, a rate limit, an invalid request) or a
	// connection that never opened: the write did not happen, so the ordinary mapping applies.
	if api.IsDefiniteRefusalAt(err, field) || api.RequestNotProcessed(err) {
		return api.MapError(err)
	}
	return questionWriteNotConfirmed(what, readBack, api.MapError(err).Error(), err)
}

// questionWriteNotConfirmed builds the unknown-outcome error. readBack is the sentence that
// tells the operator how to see what happened before trying again, worded per verb (a read of
// the question, a listing of the log for an ask that has no ref yet, a not-found read for a purge).
func questionWriteNotConfirmed(what, readBack, why string, cause error) error {
	e := exitcode.Newf(exitcode.Unavailable, "%s not confirmed (%s): the outcome is unknown and the call is not resent; %s", what, why, readBack)
	if cause != nil {
		return api.WithServerCode(e, cause)
	}
	return e
}

// readBackQuestion is the read-back sentence for a write on an existing question. The command is
// a copy-paste line, so the ref is shell-quoted (cmdutil.ShellQuote), not just made inert for the
// terminal (output.SafeCell): a ref with a space or a quote must still paste as one word.
func readBackQuestion(ref string) string {
	return "read the question with `hadron question get " + cmdutil.ShellQuote(ref) + "` before trying again"
}
