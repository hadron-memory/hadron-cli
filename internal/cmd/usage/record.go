package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// recordDTO is the --json shape of "usage record". It is the same for every
// accepted event: the server answers true whether or not the node or session
// was attributed, so nothing here can say which happened (cli#1409).
type recordDTO struct {
	Recorded bool `json:"recorded"`
}

// parseGraphQLInt reads a flag as a signed GraphQL Int (32 bit). It adds no
// sign policy: the server stores whatever Int it is given (cli#1409).
func parseGraphQLInt(flag, value string) (*int, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be an integer from -2147483648 to 2147483647, got %q", flag, value)
	}
	v := int(n)
	return &v, nil
}

func newRecord(f *cmdutil.Factory) *cobra.Command {
	var eventType, nodeLoc, session, model, tokensIn, tokensOut, actionArgs string
	cmd := &cobra.Command{
		Use:   "record --type <type> [--node-loc <loc>] [--session <id>] [--model <name>] [--tokens-in N] [--tokens-out N] [--action-args <json>]",
		Short: "Record one usage event attributed to you",
		Long: `Record ONE usage event, attributed to the account or App key you are signed in
as (WRITE, not a read). The server answers only "recorded"; this command sends
one request, never retries and never reads anything back.

--type names the kind of event; it is sent as given and the server decides what
to accept. Documented kinds include read, write, action run, edge traversal and
session summary, as examples and not a closed list. The optional flags are
sent only when you give them: --node-loc (a node loc), --session (a session
ID), --model, --tokens-in, --tokens-out (each a whole number from -2147483648
to 2147483647, sent unchanged; an explicit 0 is sent as 0, a flag you leave
out is left out) and --action-args (a JSON value, sent as given).

An event is attributed to a node only if you can read that node. An unreadable
or unknown node loc is still recorded, but WITHOUT node or memory attribution,
and a session you do not own is recorded without attribution to it. Both look
the same as success, so this command never says whether attribution happened.
With --session, an event on a live session you own counts as that session's
activity; an App key's event can resume its own lapsed session, never a dormant one.
An owned ended, expired or idle session takes the record late, without being revived or counted as activity.

The CLI checks none of this: no role, session-ownership or loc check, and it
does not validate --type or any string (an explicit empty value is sent as an
empty string, not left out). Refusals from the server (for example FORBIDDEN) are
reported as errors, exit 8. A value that is not an integer in range, or
--action-args that is not valid JSON, exits 2 before any request.

Text prints "Usage event recorded"; --json prints {"recorded": true}.`,
		Example: `  hadron usage record --type read --node-loc notes:ideas
  hadron usage record --type "action run" --model claude-sonnet-5-5 --tokens-in 1200 --tokens-out 340
  hadron usage record --type write --session 01abc --action-args '{"source":"import"}' --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			event := &gen.UsageEventInput{Type: eventType}
			for _, s := range []struct {
				name, value string
				dst         **string
			}{{"node-loc", nodeLoc, &event.NodeLoc}, {"session", session, &event.SessionId}, {"model", model, &event.Model}} {
				if cmd.Flags().Changed(s.name) {
					v := s.value
					*s.dst = &v
				}
			}
			for _, n := range []struct {
				name, value string
				dst         **int
			}{{"tokens-in", tokensIn, &event.TokensIn}, {"tokens-out", tokensOut, &event.TokensOut}} {
				if cmd.Flags().Changed(n.name) {
					v, err := parseGraphQLInt(n.name, n.value)
					if err != nil {
						return err
					}
					*n.dst = v
				}
			}
			if cmd.Flags().Changed("action-args") {
				if !json.Valid([]byte(actionArgs)) {
					return exitcode.Newf(exitcode.Usage, "--action-args must be valid JSON")
				}
				raw := json.RawMessage(actionArgs)
				event.ActionArgs = &raw
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RecordUsage(cmd.Context(), client, event)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.RecordUsage {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the usage event")
			}
			return output.Write(f.IOStreams, f.JSON, recordDTO{Recorded: true}, func(w io.Writer) error {
				_, err := fmt.Fprintln(w, "Usage event recorded")
				return err
			})
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "event kind, sent as given (required)")
	_ = cmd.MarkFlagRequired("type")
	cmd.Flags().StringVar(&nodeLoc, "node-loc", "", "node loc the event is about; recorded without attribution if unreadable")
	cmd.Flags().StringVar(&session, "session", "", "session ID to attribute the event to; counts as activity only for a live session you own")
	cmd.Flags().StringVar(&model, "model", "", "model name")
	cmd.Flags().StringVar(&tokensIn, "tokens-in", "", "input token count, an integer from -2147483648 to 2147483647 sent unchanged")
	cmd.Flags().StringVar(&tokensOut, "tokens-out", "", "output token count, an integer from -2147483648 to 2147483647 sent unchanged")
	cmd.Flags().StringVar(&actionArgs, "action-args", "", "JSON value sent as given")
	return cmd
}
