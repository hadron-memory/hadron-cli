// Package decision is the `hadron decision` group: decision logs and the
// decisions asked in them (hadron-server #1641; hadron-cli#1335).
//
// Every verb is exactly one call. References go to the server exactly as
// typed, and who may ask, amend, withdraw, purge or change deciders is the
// server's rule (cor:api:240:01): the CLI refuses only usage mistakes, before
// any call, and maps every server refusal through api.MapError.
package decision

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdDecision is the group constructor, wired in internal/cmd/root.go.
func NewCmdDecision(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Ask, read, amend and withdraw decisions in a decision log",
		Long: `A decision log is a node in a Memory that issues decision codes (DA, DB, …)
and names who decides. A decision asks one or more questions (parts) for its
deciders to answer and approve. This group shows answers but cannot record
one: the server has no answer operation yet.

The server derives a decision's state on every read. Rely on a decision as a
ruling only while its state is DECIDED.

Servers speak one of two decision APIs, and this group works with both. A
newer server (Questions, hadron-server#4513) has one question per decision,
with a background, a priority (LOW, NORMAL, HIGH, URGENT), options that say why
to choose them and a recommendation. An older server has parts, each with its
options and recommendation, an urgency (NORMAL or URGENT) and a context. Each
command that reads or writes a decision first asks the server which it is, once,
and uses that server's operations; if the server cannot say, the command stops
rather than guess. Both are shown in one form: priority and background are also
printed as urgency and context in --json, a newer server's single question is
the decision's only part (numbered 1, cited <code>-1; its text is the title),
and a field only one server has is null or [] on the other. --background and
--priority are the flags; --context and --urgency are their older names and still
work. --input-file keys follow the server's own input: see decision ask --help.

Each command makes one server call (after that one check) and passes references
exactly as typed (ID or URN). The server decides who may do what; a refusal
exits with its code.`,
	}
	p := &prober{}
	cmd.AddCommand(
		newCmdLog(f),
		newCmdAsk(f, p),
		newCmdGet(f, p),
		newCmdList(f, p),
		newCmdAmend(f, p),
		newCmdWithdraw(f, p),
		newCmdVerify(f),
		newCmdPurge(f),
	)
	return cmd
}

func newCmdLog(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Create a decision log, read it, or change its deciders",
	}
	cmd.AddCommand(newCmdLogCreate(f), newCmdLogGet(f), newCmdLogSetDeciders(f))
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

// --- decision log create | get | set-deciders ---

func newCmdLogCreate(f *cmdutil.Factory) *cobra.Command {
	var loc, name, prefix string
	var deciders, eligible []string
	cmd := &cobra.Command{
		Use:   "create <memory> --loc <loc> --decider <user-id>... [--eligible <user-id>...] [--name <name>] [--prefix <prefix>]",
		Short: "Create a decision log in a Memory",
		Long: `Create a decision log at --loc in a Memory. --decider names the default
deciders by user ID (hadron user search lists them; repeat it or separate with
commas); each must be a user who can write the Memory. --eligible names who else a decision may name as a
decider; omitted, only the default deciders are eligible. --prefix sets the code prefix (D gives DA, DB, …);
omitted, the server uses D.

The server decides who may create a log (management rights on the Memory, or
the owner role in its App) and refuses encrypted, git-mirrored, system and
anonymous Memories.`,
		Example: `  hadron decision log create hrn:mem:acme.com:eng --loc decisions --decider u_123 --prefix R`,
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
			resp, err := gen.CreateDecisionLog(cmd.Context(), client, memoryRef, loc,
				optString(cmd, "name", name), optString(cmd, "prefix", prefix), deciders, eligiblePtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateQuestionLog == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision log")
			}
			return writeLog(f, decisionLogFrom(&resp.CreateQuestionLog.DecisionLogFields), "✓ created decision log")
		},
	}
	cmd.Flags().StringVar(&loc, "loc", "", "the log's loc in the Memory (required)")
	cmd.Flags().StringSliceVar(&deciders, "decider", nil, "a default decider, by user ID (required; repeat or comma-separate)")
	cmd.Flags().StringSliceVar(&eligible, "eligible", nil, "another eligible decider, by user ID (repeat or comma-separate)")
	cmd.Flags().StringVar(&name, "name", "", "the log's name (omitted: the server default)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "the code prefix (omitted: D)")
	return cmd
}

func newCmdLogGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "get <log>",
		Short:   "Show a decision log's settings",
		Example: `  hadron decision log get hrn:node:acme.com:eng:decisions --json`,
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
			resp, err := gen.DecisionLog(cmd.Context(), client, logRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.QuestionLog == nil {
				return exitcode.Newf(exitcode.NotFound, "decision log %s not found (or not visible to you)", logRef)
			}
			return writeLog(f, decisionLogFrom(&resp.QuestionLog.DecisionLogFields), "")
		},
	}
}

func newCmdLogSetDeciders(f *cmdutil.Factory) *cobra.Command {
	var deciders, eligible []string
	var expected int
	var reason string
	cmd := &cobra.Command{
		Use:   "set-deciders <log> --decider <user-id>... [--eligible <user-id>...] --expected-revision <n> --reason <text>",
		Short: "Replace a decision log's deciders",
		Long: `Replace a log's default deciders (and, with --eligible, its other eligible
deciders), by user ID (hadron user search lists them), as an audited revision of the log. Decisions already asked keep
their deciders. Without --eligible the other eligible deciders are kept;
--eligible "" clears them. --expected-revision is the revision you read
(decision log get shows it); a different current revision is refused and
nothing changes.`,
		Example: `  hadron decision log set-deciders hrn:node:acme.com:eng:decisions --decider u_123 --expected-revision 4 --reason "Ada took over"`,
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
			resp, err := gen.SetDecisionLogDeciders(cmd.Context(), client, logRef, deciders, eligiblePtr, expected, reason)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetQuestionLogDeciders == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision log")
			}
			return writeLog(f, decisionLogFrom(&resp.SetQuestionLogDeciders.DecisionLogFields), "✓ set deciders of")
		},
	}
	cmd.Flags().StringSliceVar(&deciders, "decider", nil, "a default decider, by user ID (required; repeat or comma-separate)")
	cmd.Flags().StringSliceVar(&eligible, "eligible", nil, "another eligible decider, by user ID (omitted: unchanged; --eligible \"\" clears them; repeat or comma-separate)")
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the log revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the deciders change (required)")
	return cmd
}

// --- decision ask | amend ---

// addInputFlags registers the shared ask/amend input flags. Each one, when
// given, sets the top-level key of the same name in the input object,
// overriding --input-file. --background and --priority are the names a newer
// server uses; --context and --urgency are the older names and keep working.
func addInputFlags(cmd *cobra.Command, in *inputFlags) {
	cmd.Flags().StringVar(&in.file, "input-file", "", `a JSON object of input fields ("-" reads piped stdin); unknown, mis-cased and duplicate keys are refused`)
	cmd.Flags().StringVar(&in.title, "title", "", "the decision's title (overrides the file's)")
	cmd.Flags().StringVar(&in.background, "background", "", "background for the deciders (overrides the file's; an older server calls it context)")
	cmd.Flags().StringVar(&in.context, "context", "", "the older name of --background")
	cmd.Flags().StringVar(&in.tier, "tier", "", "ANSWERED, APPROVED or SIGNED (overrides the file's; the server validates)")
	cmd.Flags().StringVar(&in.priority, "priority", "", "LOW, NORMAL, HIGH or URGENT (overrides the file's; an older server has NORMAL and URGENT only)")
	cmd.Flags().StringVar(&in.urgency, "urgency", "", "the older name of --priority, NORMAL or URGENT")
}

// inputFlags are the shared ask/amend field flags; each overrides the file.
type inputFlags struct {
	file, title, background, context, tier, priority, urgency string
}

// check refuses what is wrong whichever server is behind it: both spellings of
// one field, and an --urgency the older vocabulary does not have.
func (in *inputFlags) check(cmd *cobra.Command) error {
	fl := cmd.Flags()
	if fl.Changed("background") && fl.Changed("context") {
		return exitcode.Newf(exitcode.Usage, "give --background or --context (its older name), not both")
	}
	if fl.Changed("priority") && fl.Changed("urgency") {
		return exitcode.Newf(exitcode.Usage, "give --priority or --urgency (its older name), not both")
	}
	if fl.Changed("urgency") && in.urgency != "NORMAL" && in.urgency != "URGENT" {
		return exitcode.Newf(exitcode.Usage, "--urgency takes NORMAL or URGENT; use --priority for LOW or HIGH")
	}
	return nil
}

// anyFlag reports whether any field flag was given.
func (in *inputFlags) anyFlag(cmd *cobra.Command) bool {
	fl := cmd.Flags()
	for _, n := range []string{"title", "background", "context", "tier", "priority", "urgency"} {
		if fl.Changed(n) {
			return true
		}
	}
	return false
}

// fileData reads --input-file once, before any call; nil when it was not
// given. A given-but-blank --input-file is refused, not read as absent.
func (in *inputFlags) fileData(cmd *cobra.Command, f *cmdutil.Factory) ([]byte, error) {
	if !cmd.Flags().Changed("input-file") {
		return nil, nil
	}
	if strings.TrimSpace(in.file) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--input-file is empty: give a path, or - for piped stdin")
	}
	return readInputFile(f.IOStreams, in.file)
}

// decodeInput strictly decodes the file into target (a pointer to one shape's
// input struct): an unknown, duplicate or mis-cased key is refused. Decoding
// into the caller's own input struct is what lets the coverage matrix see every
// input field as reachable through the file.
func decodeInput(data []byte, target any) error {
	dec, err := inputDecoder(data, target)
	if err != nil {
		return err
	}
	return finishInput(dec, dec.Decode(target))
}

// precheck refuses, before any call, a file that neither server's input would
// take: the shape is not known until the server is asked, and a file that is
// valid for one shape is not this command's to refuse early. The error is the
// older shape's; decoding again once the shape is known gives the exact one.
func precheck(data []byte, legacy, s2 any) error {
	if data == nil {
		return nil
	}
	errLegacy := decodeInput(data, legacy)
	if errLegacy == nil {
		return nil
	}
	if decodeInput(data, s2) == nil {
		return nil
	}
	return errLegacy
}

// values resolves the flags that were given for the server's shape: nil for
// each one not given. On an older server the priority is its urgency, so LOW
// and HIGH, which it does not have, are refused here rather than as a schema
// error from the server.
func (in *inputFlags) values(cmd *cobra.Command, sh shape) (title, background, tier, priority *string, err error) {
	fl := cmd.Flags()
	if fl.Changed("title") {
		title = &in.title
	}
	switch {
	case fl.Changed("background"):
		background = &in.background
	case fl.Changed("context"):
		background = &in.context
	}
	if fl.Changed("tier") {
		tier = &in.tier
	}
	switch {
	case fl.Changed("priority"):
		priority = &in.priority
	case fl.Changed("urgency"):
		priority = &in.urgency
	}
	if sh == shapeLegacy && priority != nil && *priority != "NORMAL" && *priority != "URGENT" {
		return nil, nil, nil, nil, exitcode.Newf(exitcode.Usage,
			"this server's priority is its urgency, NORMAL or URGENT; %q needs a server with priorities (LOW, NORMAL, HIGH, URGENT)", *priority)
	}
	return title, background, tier, priority, nil
}

func newCmdAsk(f *cmdutil.Factory, p *prober) *cobra.Command {
	var in inputFlags
	cmd := &cobra.Command{
		Use:   "ask <log> --input-file <path> [--title <t>] [--background <b>] [--tier <tier>] [--priority <p>]",
		Short: "Ask a decision in a decision log",
		Long: `Ask a decision. The server issues its code (the log prefix plus the next
letters) and prints the decision; decision get <code> --log <log> reads it
back later. --input-file is a JSON object with the fields of the server's
AskQuestionInput, passed through unchanged. A newer server (see decision
--help) takes these; a key an older server's input has (parts, context,
urgency, followUps) is refused with its name:

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
  deciders, tier, authorizes, grant   as for an older server below, except
                    that tier also takes ANSWERED, the default, where a
                    decider's answer alone settles it
  assignee          a decider the question is routed to
  neededBy, defaultAction   when it is needed, and what happens if it is not
  blocks, sources, specsTouched, specsMustChange   lists of references, each
                    {"ref": "..."}, {"node": "..."} or {"spec": "..."}

An older server takes these keys instead:

  title (required)  the decision's headline, as the deciders see it
  parts (required)  the questions to answer, each an object:
                    {"question": "...", "options": [{"key": "pg",
                    "label": "Postgres"}, ...], "recommendation":
                    {"key": "pg", "reason": "we run it already"}} — or
                    "yesno": true instead of options, and
                    "noRecommendation": {"reason": "..."} when none is given
  context           background for the deciders
  deciders          decider user IDs (hadron user search lists them);
                    narrows the log's default deciders, never widens past
                    its eligible set
  urgency           NORMAL (the default) or URGENT
  tier              APPROVED (the default) or SIGNED — SIGNED is forced
                    when authorizes is true, and APPROVED is then refused
                    (QUESTION_TIER_TOO_LOW). APPROVED: the ruling counts
                    once a decider has approved the answered decision.
                    SIGNED: a decider's passkey signature rather than an
                    approval; passkey signing is not available yet, so a
                    SIGNED decision stays ANSWERED_UNCONFIRMED
  authorizes        true when this decision grants an authorization
  grant             the authorization a grant decision carries, as JSON
                    (authorizes: true only)
  blocks            what is waiting on this decision
  sources           where the question came from
  followUps         citations and references the ruling must reach
                    — blocks, sources and followUps are lists of references,
                    each {"ref": "hadron-server#1640"}, {"node": "<id or
                    URN>"} or {"spec": "<citation>"}

--title, --background (--context), --tier and --priority (--urgency) override
the file's value; on an older server --priority is its urgency, so LOW and HIGH
are refused there. Keys are exact and case-sensitive, and a key given twice is
refused. Every field's exact shape is AskQuestionInput in the server's
schema (an older server's is vendored in this repo at schema/schema.graphql;
a newer server's is its own). The server validates the object (QUESTION_INVALID
names each bad field by JSON pointer) and decides who may ask: any writer of
the log's Memory.

On success the decision prints as decision get shows it — code, title,
state, tier, priority, revision, deciders and each part — prefixed "✓ asked";
--json prints the same decision as one object.`,
		Example: `  cat > ask.json <<'EOF'
{"title":"Pick a database","parts":[{"question":"Which one?",
  "options":[{"key":"pg","label":"Postgres"},{"key":"my","label":"MySQL"}],
  "recommendation":{"key":"pg","reason":"we run it already"}}]}
EOF
  hadron decision ask hrn:node:acme.com:eng:decisions --input-file ask.json
  # a newer server: options and a recommendation, no parts
  echo '{"title":"Pick a database","options":[{"key":"pg","label":"Postgres"},{"key":"my","label":"MySQL"}],"recommendation":{"key":"pg","reason":"we run it already"}}' > ask2.json
  hadron decision ask hrn:node:acme.com:eng:decisions --input-file ask2.json --priority HIGH`,
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
			if err := precheck(data, &gen.AskQuestionInput{}, &s2AskInput{}); err != nil {
				return err
			}
			if err := in.check(cmd); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sh, err := p.of(cmd.Context(), client)
			if err != nil {
				return err
			}
			title, background, tier, priority, err := in.values(cmd, sh)
			if err != nil {
				return err
			}
			if sh == shapeS2 {
				var input s2AskInput
				if data != nil {
					if err := decodeInput(data, &input); err != nil {
						return err
					}
				}
				if title != nil {
					input.Title = *title
				}
				if background != nil {
					input.Background = background
				}
				if tier != nil {
					input.Tier = tier
				}
				if priority != nil {
					input.Priority = priority
				}
				d, err := s2Ask(cmd.Context(), client, logRef, &input)
				if err != nil {
					return err
				}
				return writeDecision(f, *d, "✓ asked")
			}
			var input gen.AskQuestionInput
			if data != nil {
				// Decoded here, into the generated input, so the coverage
				// matrix sees every field of it as reachable through the file.
				dec, err := inputDecoder(data, &input)
				if err != nil {
					return err
				}
				if err := finishInput(dec, dec.Decode(&input)); err != nil {
					return err
				}
			}
			if title != nil {
				input.Title = *title
			}
			if background != nil {
				input.Context = background
			}
			if tier != nil {
				t := gen.QuestionTier(*tier)
				input.Tier = &t
			}
			if priority != nil {
				u := gen.QuestionUrgency(*priority)
				input.Urgency = &u
			}
			resp, err := gen.AskDecision(cmd.Context(), client, logRef, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AskQuestion == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision")
			}
			return writeDecision(f, decisionFrom(&resp.AskQuestion.DecisionFields), "✓ asked")
		},
	}
	addInputFlags(cmd, &in)
	return cmd
}

func newCmdAmend(f *cmdutil.Factory, p *prober) *cobra.Command {
	var in inputFlags
	var reason string
	var expected int
	cmd := &cobra.Command{
		Use:   "amend <decision> --expected-revision <n> --reason <text> [--input-file <path>] [--title <t>] [--background <b>] [--tier <tier>] [--priority <p>]",
		Short: "Amend a decision",
		Long: `Amend a decision. Only the fields you give change; the rest stay as they
are. --input-file is a JSON object with the fields of the server's
AmendQuestionInput, passed through unchanged:

  title, tier, and background and priority (an older server: context and
  urgency)   as the flags of the same name (what the tiers mean: hadron
             decision ask --help)
  parts      changes to existing parts, each {"n": <part number>} plus only
             the fields to change: "question", "options", "yesno",
             "recommendation", "noRecommendation". Part numbers are
             permanent; a change that renumbers parts is refused
             (QUESTION_PART_RENUMBER)
  addParts   new parts appended at the end, each shaped like ask's parts:
             exactly one of "recommendation" or "noRecommendation" — a new
             part with neither is refused (QUESTION_RECOMMENDATION_MISSING)
  blocks, followUps, sources   replace that whole list; each entry a
             reference: {"ref": "hadron-server#1640"}, {"node": "<id or
             URN>"} or {"spec": "<citation>"}

A newer server's input takes, besides those, whyNow, assignee, neededBy,
defaultAction, options, yesno, recommendation, noRecommendation, blocks,
sources, specsTouched and specsMustChange (as in ask), and has no parts,
addParts, context, urgency or followUps; the older keys are refused there with
their name. Answers never change here.

--title, --background (--context), --tier and --priority (--urgency) set that
field (overriding the file). An empty blocks, followUps, sources, specsTouched
or specsMustChange list ([]) clears that list, and "context": null (a newer
server: "background", "whyNow", "assignee", "neededBy" or "defaultAction": null)
clears that value; leaving a key out keeps it. Keys are exact
and case-sensitive, and a key given twice is refused.
--expected-revision is the revision you read (decision get shows it). If the
decision changed since, the amend is refused (NODE_WRITE_CONFLICT, exit 5):
read it again and retry with the new revision. The server decides who may
amend what: before any answer any writer, after one a decider only, and
priority and tier are a decider's to change.

On success the decision prints as decision get shows it, prefixed
"✓ amended".`,
		Example: `  hadron decision amend hrn:node:acme.com:eng:decisions:DA --expected-revision 3 --reason "clarify" --title "Pick a primary database"
  cat > amend.json <<'EOF'
{"parts":[{"n":1,"question":"Postgres or MySQL, and why?"}],
 "addParts":[{"question":"Who owns the migration?","yesno":true,
   "noRecommendation":{"reason":"outside the asker's remit"}}]}
EOF
  hadron decision amend hrn:node:acme.com:eng:decisions:DA --expected-revision 3 --reason "sharpen part 1" --input-file amend.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("decision", args[0])
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
			if err := precheck(data, &gen.AmendQuestionInput{}, &s2AmendInput{}); err != nil {
				return err
			}
			if err := in.check(cmd); err != nil {
				return err
			}
			// Nothing given at all is refused before any call, whichever server
			// is behind it; an input that only looks empty to one shape (an empty
			// list of parts) is refused once the shape is known.
			if !in.anyFlag(cmd) && (data == nil || fileHasNoKeys(data)) {
				return errNothingToAmend()
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sh, err := p.of(cmd.Context(), client)
			if err != nil {
				return err
			}
			title, background, tier, priority, err := in.values(cmd, sh)
			if err != nil {
				return err
			}
			if sh == shapeS2 {
				var input s2AmendInput
				if data != nil {
					if err := decodeInput(data, &input); err != nil {
						return err
					}
					if err := refuseAmendNulls(data); err != nil {
						return err
					}
				}
				if title != nil {
					input.Title = title
				}
				if background != nil {
					// A string never fails to marshal.
					raw, _ := json.Marshal(*background)
					input.Background = raw
				}
				if tier != nil {
					input.Tier = tier
				}
				if priority != nil {
					input.Priority = priority
				}
				if input.isEmpty() {
					return errNothingToAmend()
				}
				d, err := s2Amend(cmd.Context(), client, ref, &input, expected, reason)
				if err != nil {
					return err
				}
				return writeDecision(f, *d, "✓ amended")
			}
			var input gen.AmendQuestionInput
			if data != nil {
				// Decoded here, into the generated input, so the coverage
				// matrix sees every field of it as reachable through the file.
				dec, err := inputDecoder(data, &input)
				if err != nil {
					return err
				}
				if err := finishInput(dec, dec.Decode(&input)); err != nil {
					return err
				}
			}
			if title != nil {
				input.Title = title
			}
			if background != nil {
				// Context is the raw JSON value (absent / null / string); a
				// string never fails to marshal.
				raw, _ := json.Marshal(*background)
				input.Context = raw
			}
			if tier != nil {
				t := gen.QuestionTier(*tier)
				input.Tier = &t
			}
			if priority != nil {
				u := gen.QuestionUrgency(*priority)
				input.Urgency = &u
			}
			if amendIsEmpty(&input) {
				return errNothingToAmend()
			}
			resp, err := gen.AmendDecision(cmd.Context(), client, ref, &input, expected, reason)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AmendQuestion == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision")
			}
			return writeDecision(f, decisionFrom(&resp.AmendQuestion.DecisionFields), "✓ amended")
		},
	}
	addInputFlags(cmd, &in)
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the decision revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it changes (required)")
	return cmd
}

// --- decision get | list | verify ---

func newCmdGet(f *cmdutil.Factory, p *prober) *cobra.Command {
	var logRef string
	cmd := &cobra.Command{
		Use:   "get <decision> | get <code> --log <log>",
		Short: "Show a decision",
		Long: `Show a decision by ID or URN, or by its code (DU, or a part citation DU-1)
within --log.`,
		Example: `  hadron decision get hrn:node:acme.com:eng:decisions:DA
  hadron decision get DA-1 --log hrn:node:acme.com:eng:decisions --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg, err := refArg("decision", args[0])
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
			sh, err := p.of(cmd.Context(), client)
			if err != nil {
				return err
			}
			if sh == shapeS2 {
				d, err := s2Decision(cmd.Context(), client, ref, code, logPtr)
				if err != nil {
					return err
				}
				if d == nil {
					return exitcode.Newf(exitcode.NotFound, "decision %s not found (or not visible to you)", arg)
				}
				return writeDecision(f, *d, "")
			}
			resp, err := gen.Decision(cmd.Context(), client, ref, code, logPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Question == nil {
				return exitcode.Newf(exitcode.NotFound, "decision %s not found (or not visible to you)", arg)
			}
			return writeDecision(f, decisionFrom(&resp.Question.DecisionFields), "")
		},
	}
	cmd.Flags().StringVar(&logRef, "log", "", "read the argument as a code within this decision log")
	return cmd
}

func newCmdList(f *cmdutil.Factory, p *prober) *cobra.Command {
	var logRef, tier, sort string
	var states, priorities, urgencies []string
	var awaitingMe, authorizes bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list (--log <log> | --awaiting-me) [--state <s>...] [--tier <t>] [--priority <p>...] [--authorizes] [--sort CODE|PORTAL_DEFAULT] [--limit <n>] [--offset <n>]",
		Aliases: []string{"ls"},
		Short:   "List the decisions of a log, or those awaiting you",
		Long: `List the decisions of one log (--log), or with --awaiting-me the open
decisions you decide across every Memory you can read. One page per call:
--limit (server default 20) and --offset page through; total counts every
match. The server derives each state before filtering; a log above 5000
decisions refuses a filter (QUESTION_LOG_TOO_LARGE_FOR_FILTER).

--state and --priority take one or more values (repeat or comma-separate).
--urgency is --priority's older name and takes NORMAL or URGENT; an older server
has no LOW or HIGH, so --priority LOW or HIGH is refused there.
--authorizes keeps grant decisions; --authorizes=false keeps the others.`,
		Example: `  hadron decision list --log hrn:node:acme.com:eng:decisions --state OPEN,STALE
  hadron decision list --awaiting-me --json
  hadron decision list --log hrn:node:acme.com:eng:decisions --priority HIGH,URGENT`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if !fl.Changed("log") && !fl.Changed("awaiting-me") {
				return exitcode.Newf(exitcode.Usage, "give --log <log> or --awaiting-me")
			}
			var logPtr *string
			if fl.Changed("log") {
				if _, err := refArg("log", logRef); err != nil {
					return err
				}
				logPtr = &logRef
			}
			if fl.Changed("priority") && fl.Changed("urgency") {
				return exitcode.Newf(exitcode.Usage, "give --priority or --urgency (its older name), not both")
			}
			for _, u := range urgencies {
				if u != "NORMAL" && u != "URGENT" {
					return exitcode.Newf(exitcode.Usage, "--urgency takes NORMAL or URGENT; use --priority for LOW or HIGH")
				}
			}
			wanted := priorities
			if fl.Changed("urgency") {
				wanted = urgencies
			}
			var awaitPtr, authPtr *bool
			if fl.Changed("awaiting-me") {
				awaitPtr = &awaitingMe
			}
			if fl.Changed("authorizes") {
				authPtr = &authorizes
			}
			var limitPtr, offsetPtr *int
			if fl.Changed("limit") {
				limitPtr = &limit
			}
			if fl.Changed("offset") {
				offsetPtr = &offset
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sh, err := p.of(cmd.Context(), client)
			if err != nil {
				return err
			}
			var page decisionPageDTO
			if sh == shapeS2 {
				filters := s2ListFilters{
					logRef: logPtr, awaitingMe: awaitPtr, authorize: authPtr,
					states: states, priorities: wanted, limit: limitPtr, offset: offsetPtr,
				}
				if fl.Changed("tier") {
					filters.tier = &tier
				}
				if fl.Changed("sort") {
					filters.sort = &sort
				}
				pg, err := s2Decisions(cmd.Context(), client, filters)
				if err != nil {
					return err
				}
				page = *pg
			} else {
				var urgencyVals []gen.QuestionUrgency
				for _, u := range wanted {
					if u != "NORMAL" && u != "URGENT" {
						return exitcode.Newf(exitcode.Usage,
							"this server's priority is its urgency, NORMAL or URGENT; %q needs a server with priorities (LOW, NORMAL, HIGH, URGENT)", u)
					}
					urgencyVals = append(urgencyVals, gen.QuestionUrgency(u))
				}
				var stateVals []gen.QuestionState
				for _, s := range states {
					stateVals = append(stateVals, gen.QuestionState(s))
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
				resp, err := gen.Decisions(cmd.Context(), client, logPtr, awaitPtr, authPtr,
					stateVals, tierPtr, urgencyVals, sortPtr, limitPtr, offsetPtr)
				if err != nil {
					return api.MapError(err)
				}
				if resp.Questions == nil {
					return exitcode.Newf(exitcode.Error, "server returned no decision page")
				}
				page = decisionPageDTO{Total: resp.Questions.Total, Items: []decisionDTO{}}
				for _, it := range resp.Questions.Items {
					if it != nil {
						page.Items = append(page.Items, decisionFrom(&it.DecisionFields))
					}
				}
			}
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				t := output.NewTable(w, "CODE", "STATE", "PRIORITY", "TIER", "REV", "TITLE")
				for _, d := range page.Items {
					t.Row(output.SafeCell(d.Code), d.State, d.Priority, d.Tier, fmt.Sprint(d.Revision), output.SafeCell(d.Title))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				_, err := fmt.Fprintf(f.IOStreams.ErrOut, "%d of %d\n", len(page.Items), page.Total)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&logRef, "log", "", "the decision log, by ID or URN")
	cmd.Flags().BoolVar(&awaitingMe, "awaiting-me", false, "open decisions you decide, across every Memory you can read")
	cmd.Flags().StringSliceVar(&states, "state", nil, "keep these states: OPEN, ANSWERED_UNCONFIRMED, DECIDED, STALE, WITHDRAWN, TAMPERED, UNVERIFIABLE (SUPERSEDED on an older server)")
	cmd.Flags().StringVar(&tier, "tier", "", "keep this tier: ANSWERED, APPROVED or SIGNED")
	cmd.Flags().StringSliceVar(&priorities, "priority", nil, "keep these priorities: LOW, NORMAL, HIGH, URGENT (an older server has NORMAL and URGENT only)")
	cmd.Flags().StringSliceVar(&urgencies, "urgency", nil, "the older name of --priority: NORMAL, URGENT")
	cmd.Flags().BoolVar(&authorizes, "authorizes", false, "keep grant decisions (=false keeps the others)")
	cmd.Flags().StringVar(&sort, "sort", "", "CODE, or PORTAL_DEFAULT (urgent, then open, then newest; the server default)")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default 20)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many matches")
	return cmd
}

func newCmdVerify(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <decision>",
		Short: "Check a decision's content, data and approval against each other",
		Long: `Re-render a decision from its stored payload and check it against its
content, data and approval: the findings behind its state. An empty findings
list with state DECIDED is a ruling you may rely on.`,
		Example: `  hadron decision verify hrn:node:acme.com:eng:decisions:DA --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("decision", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.VerifyDecision(cmd.Context(), client, ref)
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

// --- decision withdraw | purge ---

func newCmdWithdraw(f *cmdutil.Factory, p *prober) *cobra.Command {
	var expected int
	var reason string
	var yes bool
	cmd := &cobra.Command{
		Use:   "withdraw <decision> --expected-revision <n> --reason <text> [--yes]",
		Short: "Withdraw a decision (final; its code stays used)",
		Long: `Withdraw a decision. This is final, and its code is never reused. The
server decides who may: the asker or a decider, and only a decider once it is
answered. A DECIDED decision is superseded instead (QUESTION_ANSWERED_DECIDED).
Prompts on a terminal; --yes is required otherwise.`,
		Example: `  hadron decision withdraw hrn:node:acme.com:eng:decisions:DA --expected-revision 3 --reason "no longer needed" --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("decision", args[0])
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
			if err := cmdutil.Confirm(f.IOStreams, yes, "Withdraw decision "+output.SafeCell(ref)+"? Withdrawal is final and its code is never reused."); err != nil {
				return err
			}
			sh, err := p.of(cmd.Context(), client)
			if err != nil {
				return err
			}
			if sh == shapeS2 {
				d, err := s2Withdraw(cmd.Context(), client, ref, reason, expected)
				if err != nil {
					return err
				}
				return writeDecision(f, *d, "✓ withdrew")
			}
			resp, err := gen.WithdrawDecision(cmd.Context(), client, ref, reason, expected)
			if err != nil {
				return api.MapError(err)
			}
			if resp.WithdrawQuestion == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision")
			}
			return writeDecision(f, decisionFrom(&resp.WithdrawQuestion.DecisionFields), "✓ withdrew")
		},
	}
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the decision revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it is withdrawn (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdPurge(f *cmdutil.Factory) *cobra.Command {
	var reason string
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge <decision> --reason <text> [--yes]",
		Short: "Delete a never-answered decision (platform admins)",
		Long: `Hard-delete a decision that was never answered. The purge is recorded on
its log and the code stays used. Platform admins only, by server rule; an
answered decision is refused (QUESTION_ANSWERED). Prompts on a terminal;
--yes is required otherwise.`,
		Example: `  hadron decision purge hrn:node:acme.com:eng:decisions:DC --reason "test data" --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg("decision", args[0])
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
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "decision "+ref+" (purge is permanent)"); err != nil {
				return err
			}
			resp, err := gen.PurgeUnansweredDecision(cmd.Context(), client, ref, reason)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.PurgeUnansweredQuestion {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the purge of %s", ref)
			}
			dto := purgeDTO{Ref: ref, Purged: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ purged decision %s\n", output.SafeCell(ref))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why it is purged (required; recorded on the log)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// amendIsEmpty reports an amend that would change nothing: every field omitted.
// context (raw: a literal null clears it) and blocks, followUps and sources
// count by PRESENCE, not length: an explicit []
// clears that list on the server, so {"blocks":[]} is a real change. parts and
// addParts count by length: an empty change list changes nothing.
func amendIsEmpty(in *gen.AmendQuestionInput) bool {
	return in.Title == nil && in.Context == nil && in.Tier == nil && in.Urgency == nil &&
		len(in.Parts) == 0 && len(in.AddParts) == 0 &&
		in.Blocks == nil && in.FollowUps == nil && in.Sources == nil
}

// errNothingToAmend is the refusal for an amend that would change nothing.
func errNothingToAmend() error {
	return exitcode.Newf(exitcode.Usage, "nothing to amend: give --input-file with a field, or --title, --background, --tier or --priority (the older names --context and --urgency work too)")
}

// fileHasNoKeys reports an --input-file whose JSON object has no key at all
// (its validity was checked already).
func fileHasNoKeys(data []byte) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(data, &m) == nil && len(m) == 0
}
