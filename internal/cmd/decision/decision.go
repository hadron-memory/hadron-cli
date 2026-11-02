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

Each command makes one server call and passes references exactly as typed (ID
or URN). The server decides who may do what; a refusal exits with its code.`,
	}
	cmd.AddCommand(
		newCmdLog(f),
		newCmdAsk(f),
		newCmdGet(f),
		newCmdList(f),
		newCmdAmend(f),
		newCmdWithdraw(f),
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
deciders by user ID (repeat it or separate with commas); each must be a user
who can write the Memory. --eligible names who else a decision may name as a
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
			if resp.CreateDecisionLog == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision log")
			}
			return writeLog(f, decisionLogFrom(&resp.CreateDecisionLog.DecisionLogFields), "✓ created decision log")
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
			if resp.DecisionLog == nil {
				return exitcode.Newf(exitcode.NotFound, "decision log %s not found (or not visible to you)", logRef)
			}
			return writeLog(f, decisionLogFrom(&resp.DecisionLog.DecisionLogFields), "")
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
deciders), by user ID, as an audited revision of the log. Decisions already asked keep
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
			if resp.SetDecisionLogDeciders == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision log")
			}
			return writeLog(f, decisionLogFrom(&resp.SetDecisionLogDeciders.DecisionLogFields), "✓ set deciders of")
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
// overriding --input-file.
func addInputFlags(cmd *cobra.Command, in *inputFlags) {
	cmd.Flags().StringVar(&in.file, "input-file", "", `a JSON object of input fields ("-" reads piped stdin); unknown, mis-cased and duplicate keys are refused`)
	cmd.Flags().StringVar(&in.title, "title", "", "the decision's title (overrides the file's)")
	cmd.Flags().StringVar(&in.context, "context", "", "background for the deciders (overrides the file's)")
	cmd.Flags().StringVar(&in.tier, "tier", "", "APPROVED or SIGNED (overrides the file's; the server validates)")
	cmd.Flags().StringVar(&in.urgency, "urgency", "", "NORMAL or URGENT (overrides the file's; the server validates)")
}

// inputFlags are the shared ask/amend field flags; each overrides the file.
type inputFlags struct {
	file, title, context, tier, urgency string
}

// decoder opens --input-file for a strict decode; nil when it was not given.
// A given-but-blank --input-file is refused, not read as absent.
// The caller decodes into its own generated input struct, which is what lets
// the coverage matrix see every input field as reachable through the file.
func (in *inputFlags) decoder(cmd *cobra.Command, f *cmdutil.Factory, target any) (*json.Decoder, error) {
	if !cmd.Flags().Changed("input-file") {
		return nil, nil
	}
	if strings.TrimSpace(in.file) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--input-file is empty: give a path, or - for piped stdin")
	}
	data, err := readInputFile(f.IOStreams, in.file)
	if err != nil {
		return nil, err
	}
	return inputDecoder(data, target)
}

// overrides returns a pointer for each field flag that was given, nil
// otherwise (so an unset flag leaves the file's value, or omits the field).
func (in *inputFlags) overrides(cmd *cobra.Command) (title, context *string, tier *gen.DecisionTier, urgency *gen.DecisionUrgency) {
	fl := cmd.Flags()
	if fl.Changed("title") {
		title = &in.title
	}
	if fl.Changed("context") {
		context = &in.context
	}
	if fl.Changed("tier") {
		t := gen.DecisionTier(in.tier)
		tier = &t
	}
	if fl.Changed("urgency") {
		u := gen.DecisionUrgency(in.urgency)
		urgency = &u
	}
	return title, context, tier, urgency
}

func newCmdAsk(f *cmdutil.Factory) *cobra.Command {
	var in inputFlags
	cmd := &cobra.Command{
		Use:   "ask <log> --input-file <path> [--title <t>] [--context <c>] [--tier <tier>] [--urgency <u>]",
		Short: "Ask a decision in a decision log",
		Long: `Ask a decision. The server issues its code (the log prefix plus the next
letters) and renders it. --input-file is a JSON object with the fields of the
server's AskDecisionInput, passed through unchanged:

  title (required), parts (required: each a question, with options or
  "yesno": true, and a recommendation or noRecommendation), context, tier,
  urgency, deciders, authorizes, grant, blocks, followUps, sources

--title, --context, --tier and --urgency override the file's value. The server
validates the object (DECISION_INVALID names each bad field by JSON pointer)
and decides who may ask: any writer of the log's Memory.`,
		Example: `  hadron decision ask hrn:node:acme.com:eng:decisions --input-file ask.json
  # ask.json:
  # {"title":"Pick a database","parts":[{"question":"Which one?",
  #   "options":[{"key":"pg","label":"Postgres"},{"key":"my","label":"MySQL"}],
  #   "recommendation":{"key":"pg","reason":"we run it already"}}]}`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logRef, err := refArg("log", args[0])
			if err != nil {
				return err
			}
			if err := requireFlag(cmd, "input-file"); err != nil {
				return err
			}
			var input gen.AskDecisionInput
			dec, err := in.decoder(cmd, f, &input)
			if err != nil {
				return err
			}
			if dec != nil {
				if err := finishInput(dec, dec.Decode(&input)); err != nil {
					return err
				}
			}
			title, context, tier, urgency := in.overrides(cmd)
			if title != nil {
				input.Title = *title
			}
			if context != nil {
				input.Context = context
			}
			if tier != nil {
				input.Tier = tier
			}
			if urgency != nil {
				input.Urgency = urgency
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AskDecision(cmd.Context(), client, logRef, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AskDecision == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision")
			}
			return writeDecision(f, decisionFrom(&resp.AskDecision.DecisionFields), "✓ asked")
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
		Use:   "amend <decision> --expected-revision <n> --reason <text> [--input-file <path>] [--title <t>] [--context <c>] [--tier <tier>] [--urgency <u>]",
		Short: "Amend a decision",
		Long: `Amend a decision. Only the fields you give change; the rest stay as they
are. --input-file is a JSON object with the fields of the server's
AmendDecisionInput, passed through unchanged:

  title, context, tier, urgency, parts (changes to existing parts by "n"),
  addParts (new parts), blocks, followUps, sources

--title, --context, --tier and --urgency set that field (overriding the file).
An empty blocks, followUps or sources list ([]) clears that list, and
"context": null clears the context; leaving a key out keeps it. Keys are exact and case-sensitive, and a key given
twice is refused.
--expected-revision is the revision you read (decision get shows it). The
server decides who may amend what: before any answer any writer, after one a
decider only, and urgency and tier are a decider's to change.`,
		Example: `  hadron decision amend hrn:node:acme.com:eng:decisions:DA --expected-revision 3 --reason "clarify" --title "Pick a primary database"`,
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
			var input gen.AmendDecisionInput
			dec, err := in.decoder(cmd, f, &input)
			if err != nil {
				return err
			}
			if dec != nil {
				if err := finishInput(dec, dec.Decode(&input)); err != nil {
					return err
				}
			}
			title, context, tier, urgency := in.overrides(cmd)
			if title != nil {
				input.Title = title
			}
			if context != nil {
				// Context is the raw JSON value (absent / null / string); a
				// string never fails to marshal.
				raw, _ := json.Marshal(*context)
				input.Context = raw
			}
			if tier != nil {
				input.Tier = tier
			}
			if urgency != nil {
				input.Urgency = urgency
			}
			if amendIsEmpty(&input) {
				return exitcode.Newf(exitcode.Usage, "nothing to amend: give --input-file with a field, or --title, --context, --tier or --urgency")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AmendDecision(cmd.Context(), client, ref, &input, expected, reason)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AmendDecision == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision")
			}
			return writeDecision(f, decisionFrom(&resp.AmendDecision.DecisionFields), "✓ amended")
		},
	}
	addInputFlags(cmd, &in)
	cmd.Flags().IntVar(&expected, "expected-revision", 0, "the decision revision you read (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why it changes (required)")
	return cmd
}

// --- decision get | list | verify ---

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
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
			resp, err := gen.Decision(cmd.Context(), client, ref, code, logPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Decision == nil {
				return exitcode.Newf(exitcode.NotFound, "decision %s not found (or not visible to you)", arg)
			}
			return writeDecision(f, decisionFrom(&resp.Decision.DecisionFields), "")
		},
	}
	cmd.Flags().StringVar(&logRef, "log", "", "read the argument as a code within this decision log")
	return cmd
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var logRef, tier, sort string
	var states, urgencies []string
	var awaitingMe, authorizes bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list (--log <log> | --awaiting-me) [--state <s>...] [--tier <t>] [--urgency <u>...] [--authorizes] [--sort CODE|PORTAL_DEFAULT] [--limit <n>] [--offset <n>]",
		Aliases: []string{"ls"},
		Short:   "List the decisions of a log, or those awaiting you",
		Long: `List the decisions of one log (--log), or with --awaiting-me the open
decisions you decide across every Memory you can read. One page per call:
--limit (server default 20) and --offset page through; total counts every
match. The server derives each state before filtering; a log above 5000
decisions refuses a filter (DECISION_LOG_TOO_LARGE_FOR_FILTER).

--state and --urgency take one or more values (repeat or comma-separate).
--authorizes keeps grant decisions; --authorizes=false keeps the others.`,
		Example: `  hadron decision list --log hrn:node:acme.com:eng:decisions --state OPEN,STALE
  hadron decision list --awaiting-me --json`,
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
			var stateVals []gen.DecisionState
			for _, s := range states {
				stateVals = append(stateVals, gen.DecisionState(s))
			}
			var urgencyVals []gen.DecisionUrgency
			for _, u := range urgencies {
				urgencyVals = append(urgencyVals, gen.DecisionUrgency(u))
			}
			var tierPtr *gen.DecisionTier
			if fl.Changed("tier") {
				t := gen.DecisionTier(tier)
				tierPtr = &t
			}
			var sortPtr *gen.DecisionSort
			if fl.Changed("sort") {
				s := gen.DecisionSort(sort)
				sortPtr = &s
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
			resp, err := gen.Decisions(cmd.Context(), client, logPtr, awaitPtr, authPtr,
				stateVals, tierPtr, urgencyVals, sortPtr, limitPtr, offsetPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Decisions == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision page")
			}
			page := decisionPageDTO{Total: resp.Decisions.Total, Items: []decisionDTO{}}
			for _, it := range resp.Decisions.Items {
				if it != nil {
					page.Items = append(page.Items, decisionFrom(&it.DecisionFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				t := output.NewTable(w, "CODE", "STATE", "URGENCY", "TIER", "REV", "TITLE")
				for _, d := range page.Items {
					t.Row(output.SafeCell(d.Code), d.State, d.Urgency, d.Tier, fmt.Sprint(d.Revision), output.SafeCell(d.Title))
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
	cmd.Flags().StringSliceVar(&states, "state", nil, "keep these states: OPEN, ANSWERED_UNCONFIRMED, DECIDED, STALE, SUPERSEDED, WITHDRAWN, TAMPERED, UNVERIFIABLE")
	cmd.Flags().StringVar(&tier, "tier", "", "keep this tier: APPROVED or SIGNED")
	cmd.Flags().StringSliceVar(&urgencies, "urgency", nil, "keep these urgencies: NORMAL, URGENT")
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
			if resp.VerifyDecision == nil {
				return exitcode.Newf(exitcode.Error, "server returned no verification")
			}
			v := resp.VerifyDecision
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

func newCmdWithdraw(f *cmdutil.Factory) *cobra.Command {
	var expected int
	var reason string
	var yes bool
	cmd := &cobra.Command{
		Use:   "withdraw <decision> --expected-revision <n> --reason <text> [--yes]",
		Short: "Withdraw a decision (final; its code stays used)",
		Long: `Withdraw a decision. This is final, and its code is never reused. The
server decides who may: the asker or a decider, and only a decider once it is
answered. A DECIDED decision is superseded instead (DECISION_ANSWERED_DECIDED).
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
			resp, err := gen.WithdrawDecision(cmd.Context(), client, ref, reason, expected)
			if err != nil {
				return api.MapError(err)
			}
			if resp.WithdrawDecision == nil {
				return exitcode.Newf(exitcode.Error, "server returned no decision")
			}
			return writeDecision(f, decisionFrom(&resp.WithdrawDecision.DecisionFields), "✓ withdrew")
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
answered decision is refused (DECISION_ANSWERED). Prompts on a terminal;
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
			if !resp.PurgeUnansweredDecision {
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
func amendIsEmpty(in *gen.AmendDecisionInput) bool {
	return in.Title == nil && in.Context == nil && in.Tier == nil && in.Urgency == nil &&
		len(in.Parts) == 0 && len(in.AddParts) == 0 &&
		in.Blocks == nil && in.FollowUps == nil && in.Sources == nil
}
