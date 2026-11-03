package team

// `team session outcome` — report how a session went (cli#1351 over
// hadron-server#2896). The range check, the owner gate and the MCP-only OAuth
// refusal are the server's (cor:api:240:01); the client only parses.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// sessionOutcomeDTO is the stable --json shape: the session's outcome fields
// as the server holds them after the write.
type sessionOutcomeDTO struct {
	SessionID   string          `json:"sessionId"`
	Outcome     *float64        `json:"outcome"`
	OutcomeRef  *string         `json:"outcomeRef"`
	OutcomeMeta json.RawMessage `json:"outcomeMeta"`
}

func newCmdSessionOutcome(f *cmdutil.Factory) *cobra.Command {
	var ref, meta string
	cmd := &cobra.Command{
		Use:   "outcome <session-id> <0..1> [--ref <text>] [--meta <json>]",
		Short: "Record how a session went: an outcome from 0 to 1",
		Long: `Record a session's outcome: a number from 0 (failed) to 1 (succeeded), with
an optional reference of yours (--ref: a ticket, a survey id) and optional
JSON metadata (--meta). It works on any session you own, not only the one
this worktree is bound to; find the id with ` + "`team session list`" + `.

--ref and --meta are written only when given: leaving one out keeps what
the session already has, --ref "" stores an empty reference and --meta null
clears the metadata. --meta must be valid JSON (an object, a list, a
number, or a string in JSON quotes); malformed JSON is refused with exit 2
and nothing sent.

Recording an outcome is not driving the session: a lapsed session stays
lapsed, and an ended one can still be given its outcome.

A negative number reads as a flag, so pass one after ` + "`--`" + ` (it is out of
range, and the server refuses it with exit 2).

On success it prints one line naming the outcome and the session; --json
prints {sessionId, outcome, outcomeRef, outcomeMeta} as the server now
holds them (null when unset).

WHO MAY RUN IT: the session's owner, which is the person the session is
attributed to, or an App key for a session of its own App with no person
attributed. The strict owner wins over a platform admin. The server refuses
anyone else (exit 8) and an OAuth credential limited to the MCP surface
(exit 3). An outcome outside 0..1 and an unknown session are refused with
exit 2. --json keeps the server's code in the error's serverCode.`,
		Example: `  hadron team session outcome 01234567-89ab-4cde-8f01-23456789abcd 0.8
  hadron team session outcome 01234567-89ab-4cde-8f01-23456789abcd 1 --ref TICKET-42 --meta '{"survey":"csat","score":5}' --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "pass a session id (see `hadron team session list`)")
			}
			outcome, err := strconv.ParseFloat(strings.TrimSpace(args[1]), 64)
			if err != nil || math.IsNaN(outcome) || math.IsInf(outcome, 0) {
				return exitcode.Newf(exitcode.Usage, "the outcome must be a number from 0 to 1, got %q", args[1])
			}
			var refArg *string
			if cmd.Flags().Changed("ref") {
				refArg = &ref
			}
			var metaArg *json.RawMessage
			if cmd.Flags().Changed("meta") {
				if !json.Valid([]byte(meta)) {
					return exitcode.Newf(exitcode.Usage, "--meta is not valid JSON; nothing was sent")
				}
				raw := json.RawMessage(meta)
				metaArg = &raw
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RecordSessionOutcome(cmd.Context(), client, id, outcome, refArg, metaArg)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.RecordSessionOutcome == nil {
				return exitcode.Newf(exitcode.Error, "the server did not return session %s; run `hadron team session list` to see whether the outcome was recorded", id)
			}
			s := resp.RecordSessionOutcome
			dto := sessionOutcomeDTO{SessionID: s.Id, Outcome: s.Outcome, OutcomeRef: s.OutcomeRef}
			if s.OutcomeMeta != nil {
				dto.OutcomeMeta = *s.OutcomeMeta
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ recorded outcome %s on session %s\n", strconv.FormatFloat(outcome, 'g', -1, 64), output.SafeCell(s.Id))
				return err
			})
		},
	}
	// A negative number reads as a shorthand flag; say how to pass one rather
	// than leave a bare "unknown shorthand flag".
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		if _, tok, ok := strings.Cut(err.Error(), " in "); ok {
			if _, perr := strconv.ParseFloat(tok, 64); perr == nil {
				return exitcode.Newf(exitcode.Usage, "%s reads as a flag: pass a negative outcome after `--` (hadron team session outcome <session-id> -- %s)", tok, tok)
			}
		}
		return exitcode.New(exitcode.Usage, err)
	})
	cmd.Flags().StringVar(&ref, "ref", "", "your reference for the outcome (a ticket, a survey id); kept as given")
	cmd.Flags().StringVar(&meta, "meta", "", "JSON metadata to keep with the outcome")
	return cmd
}
