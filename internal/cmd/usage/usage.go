// Package usage exposes read-only provider usage metadata and counts.
package usage

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type eventDTO struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	CreatedAt      string  `json:"createdAt"`
	MemoryID       *string `json:"memoryId"`
	NodeLoc        *string `json:"nodeLoc"`
	Provider       *string `json:"provider"`
	Model          *string `json:"model"`
	KeyOwnerType   *string `json:"keyOwnerType"`
	TokensIn       *int    `json:"tokensIn"`
	TokensOut      *int    `json:"tokensOut"`
	CostMicroCents *string `json:"costMicroCents"`
}
type summaryDTO struct {
	NodeLoc string `json:"nodeLoc"`
	Type    string `json:"type"`
	Count   int    `json:"count"`
}

func NewCmdUsage(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "usage <command>", Short: "Read activity counts and AI usage events, or record one (not billing balances)", Long: "Look at read/write activity counts with \"usage activity\", or AI-call events\n(model, tokens and recorded cost) with \"usage events\". To record one usage event, use \"usage record\". For organization\ncredit, use \"hadron billing balance\". For a model subscription account's 5h/7d allowance, use \"hadron team usage\".", Example: "  hadron usage events --memory hrn:mem:acme.com:notes --limit 50"}
	cmd.AddCommand(newEvents(f), newActivity(f), newRecord(f))
	return cmd
}
func newEvents(f *cmdutil.Factory) *cobra.Command {
	var memory, loc, node, eventType, since, until string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "events [--memory <ref>] [--loc <loc> | --node <ref>] [--type <type>] [--since <time>] [--until <time>] [--limit N] [--offset N]",
		Short: "Read one page of accessible usage events",
		Long: `List recent AI usage events, newest first: when each happened, its type, the
memory and node it was for, the provider and model, tokens in and out, and what
it cost. You only see events in memories you can read.

You get ONE page: 20 events by default, or --limit (0 to 200; 0 sends the request and returns no rows, only the total on
stderr). To go further
back, run the command again with --offset set to the number of events already
seen: stderr says "showing a-b of N" with the real total (for the same filters
and window) and, when more exist, "more: --offset N". There is no cost sum.

--since and --until bound the window by creation time: --since is INCLUSIVE and
--until is EXCLUSIVE, so you get events with since <= CREATED < until. Either
may be left out. Each is an ISO date-time WITH a "Z" or an explicit offset (for
example 2026-10-22T00:00:00Z or 2026-10-22T09:00:00+02:00); a date alone or a
time without an offset is refused, and so is a window where --since is not
before --until. Note that "hadron team events --since" is the opposite: it is
EXCLUSIVE ("strictly after"). Windowing narrows by time only; you still see
just the memories you can read. To answer "what happened last week", give both
bounds and page with --offset.

--memory is a memory ID or URN (for example hrn:mem:acme.com:notes). --loc is an exact
node loc, including hierarchical ones such as notes:ideas. --node instead takes
a node PK or fully-qualified node URN; it cannot be combined with --loc. The
CLI does not resolve a loc to a node; pass a PK or fully-qualified node URN. --type is an exact
event type. Leave a flag out for no filter; an empty value is refused.

COST is in micro-cents: 1 USD = 100,000,000 micro-cents (so 5000000 is 0.05 USD,
five cents). "unpriced" means the event has no price recorded; it does not mean
free. "unknown" under TOKENS IN or TOKENS OUT means the count was not recorded;
a "-" under MEMORY ID, LOC, PROVIDER or MODEL means the event has none.

Details:
  Cost is an arbitrary-size base-10 integer kept as a string in --json
  (costMicroCents), never a floating point number; null is unpriced. Token
  counts that are null stay unknown. --json prints an array of event objects
  (empty is []). If the server returns a malformed row, the command fails
  before printing any rows. The action's arguments and caller identity are not
  requested. Nothing is recorded and no model or provider is called.`,
		Example: `  hadron usage events
  hadron usage events --memory hrn:mem:acme.com:notes --limit 100
  hadron usage events --since 2026-10-22T00:00:00Z --until 2026-10-23T00:00:00Z
  hadron usage events --type <event-type> --offset 20 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 0 || limit > 200 {
				return exitcode.Newf(exitcode.Usage, "--limit must be between 0 and 200")
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--offset must be non-negative")
			}
			if cmd.Flags().Changed("node") && cmd.Flags().Changed("loc") {
				return exitcode.Newf(exitcode.Usage, "--node and --loc cannot be combined")
			}
			var nodeArg *string
			if cmd.Flags().Changed("node") {
				if strings.TrimSpace(node) == "" {
					return exitcode.Newf(exitcode.Usage, "--node must not be empty; omit it for no filter")
				}
				v := node
				if !cmdutil.IsEntityID(v) {
					var err error
					v, err = cmdutil.CanonicalNodeURN(v)
					if err != nil {
						return err
					}
				}
				nodeArg = &v
			}
			var mem, locArg, typeArg *string
			for _, arg := range []struct {
				name, value string
				dst         **string
			}{{"memory", memory, &mem}, {"loc", loc, &locArg}, {"type", eventType, &typeArg}} {
				if cmd.Flags().Changed(arg.name) {
					if strings.TrimSpace(arg.value) == "" {
						return exitcode.Newf(exitcode.Usage, "--%s must not be empty; omit the flag for no filter", arg.name)
					}
					v := arg.value
					if arg.name == "memory" {
						v = cmdutil.CanonicalMemoryRef(v)
					}
					*arg.dst = &v
				}
			}
			sinceArg, untilArg, err := parseWindow(cmd, since, until)
			if err != nil {
				return err
			}
			var off *int
			if offset != 0 {
				off = &offset
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UsageEventsPage(cmd.Context(), client, mem, locArg, nodeArg, typeArg, limit, off, sinceArg, untilArg)
			if err != nil {
				return api.MapError(err)
			}
			rows := []eventDTO{}
			for _, e := range resp.UsageEventsPage.Items {
				if e == nil {
					return exitcode.Newf(exitcode.Error, "the server returned an invalid usage-event row")
				}
				rows = append(rows, eventDTO{ID: e.Id, Type: e.Type, CreatedAt: e.CreatedAt, MemoryID: e.MemoryId, NodeLoc: e.NodeLoc, Provider: e.Provider, Model: e.Model, KeyOwnerType: e.KeyOwnerType, TokensIn: e.TokensIn, TokensOut: e.TokensOut, CostMicroCents: e.CostMicroCents})
			}
			// The twin's real total, windowed like the rows (cli#1339). stdout and
			// --json stay the bare array; the position and the next page are on stderr.
			page := resp.UsageEventsPage
			if len(rows) == 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "no events at offset %d (total %d)\n", offset, page.Total)
			} else {
				fmt.Fprintf(f.IOStreams.ErrOut, "showing %d-%d of %d\n", offset+1, offset+len(rows), page.Total)
			}
			if page.HasMore {
				fmt.Fprintln(f.IOStreams.ErrOut, "more: --offset", offset+len(rows))
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				t := output.NewTable(w, "CREATED", "TYPE", "MEMORY ID", "LOC", "PROVIDER", "MODEL", "TOKENS IN", "TOKENS OUT", "COST (micro-cents)")
				for _, e := range rows {
					cost := "unpriced"
					if e.CostMicroCents != nil {
						cost = *e.CostMicroCents
					}
					t.Row(output.SafeCell(e.CreatedAt), output.SafeCell(e.Type), output.SafeCell(output.Dash(e.MemoryID)), output.SafeCell(output.Dash(e.NodeLoc)), output.SafeCell(output.Dash(e.Provider)), output.SafeCell(output.Dash(e.Model)), tokenLabel(e.TokensIn), tokenLabel(e.TokensOut), output.SafeCell(cost))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&memory, "memory", "", "memory id or URN to filter")
	cmd.Flags().StringVar(&node, "node", "", "node PK or fully-qualified URN to filter; excludes --loc")
	cmd.Flags().StringVar(&loc, "loc", "", "verbatim exact node loc to filter")
	cmd.Flags().StringVar(&eventType, "type", "", "exact event type to filter")
	cmd.Flags().StringVar(&since, "since", "", "ISO date-time with Z or an offset; events at or after it (inclusive)")
	cmd.Flags().StringVar(&until, "until", "", "ISO date-time with Z or an offset; events before it (exclusive)")
	cmd.Flags().IntVar(&limit, "limit", 20, "page size (0-200; 0 counts only)")
	cmd.Flags().IntVar(&offset, "offset", 0, "rows to skip (nonnegative)")
	return cmd
}

// parseWindow validates --since/--until client-side (RFC 3339 requires the Z or
// numeric offset, so an offset-less or date-only value is refused) and returns
// them verbatim for the wire; an unset flag stays nil so it is omitted.
// since >= until is also refused here, before any request (cli#1205).
func parseWindow(cmd *cobra.Command, since, until string) (*string, *string, error) {
	var out [2]*string
	var at [2]time.Time
	for i, b := range []struct{ name, value string }{{"since", since}, {"until", until}} {
		if !cmd.Flags().Changed(b.name) {
			continue
		}
		v, err := cmdutil.ParseTimeFlag(b.name, b.value)
		if err != nil {
			return nil, nil, err
		}
		out[i] = v
		at[i], _ = cmdutil.ParseStrictDateTime(*v)
	}
	if out[0] != nil && out[1] != nil && !at[0].Before(at[1]) {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--since must be before --until (since is inclusive, until exclusive)")
	}
	return out[0], out[1], nil
}

func tokenLabel(v *int) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprint(*v)
}

// NewCmdMemoryUsage is registered under memory; the server owns the aggregation.
func NewCmdMemoryUsage(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "usage <memoryRef>", Short: "Count how often each node in a memory was read and written", Long: `Count how often each node in a memory was read and written: one row per node
loc and kind (read or write), with the number of times, as the server
recorded them. Counts stay after a node is deleted. Use it to see which notes
are used and which are not. It is not a cost or billing figure; AI calls and
their cost are in hadron usage events.

You need read access to the memory. Old activity that was recorded without a
node may be missing. --json prints an array of {nodeLoc,type,count}, [] when
there is none.`, Example: `  hadron memory usage hrn:mem:acme.com:kb`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return exitcode.Newf(exitcode.Usage, "memory reference must not be empty")
		}
		memory := cmdutil.CanonicalMemoryRef(args[0])
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.MemoryUsageSummary(cmd.Context(), client, memory)
		if err != nil {
			return api.MapError(err)
		}
		rows := []summaryDTO{}
		for _, e := range resp.MemoryUsageSummary {
			if e != nil {
				rows = append(rows, summaryDTO{NodeLoc: e.NodeLoc, Type: e.Type, Count: e.Count})
			}
		}
		return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
			t := output.NewTable(w, "LOC", "TYPE", "COUNT")
			for _, e := range rows {
				t.Row(output.SafeCell(e.NodeLoc), output.SafeCell(e.Type), fmt.Sprint(e.Count))
			}
			return t.Flush()
		})
	}}
}
