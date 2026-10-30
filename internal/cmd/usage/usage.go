// Package usage exposes read-only provider usage metadata and counts.
package usage

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
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
	cmd := &cobra.Command{Use: "usage <command>", Short: "See what AI usage happened (not billing balances)", Long: "Look at the record of AI calls made in your memories: when, which model, how many\ntokens, and what it cost. This is the activity log; for how much credit an\norganization has left, use \"hadron billing balance\". Start with \"usage events\".", Example: "  hadron usage events --memory hrn:mem:acme.com:notes --limit 50"}
	cmd.AddCommand(newEvents(f))
	return cmd
}
func newEvents(f *cmdutil.Factory) *cobra.Command {
	var memory, loc, eventType, since, until string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "events [--memory <ref>] [--loc <loc>] [--type <type>] [--since <time>] [--until <time>] [--limit N] [--offset N]",
		Short: "Read one page of accessible usage events",
		Long: `List recent AI usage events, newest first: when each happened, its type, the
memory and node it was for, the provider and model, tokens in and out, and what
it cost. You only see events in memories you can read.

You get ONE page: 20 events by default, or --limit (1 to 200). To go further
back, run the command again with --offset set to the number of events already
seen (after a full page, stderr says "more may exist; request another page with
--offset N").
There is no total and no cost sum.

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
node loc, including hierarchical ones such as notes:ideas. --type is an exact
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
			if limit < 1 || limit > 200 {
				return exitcode.Newf(exitcode.Usage, "--limit must be between 1 and 200")
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--offset must be non-negative")
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
			resp, err := gen.UsageEvents(cmd.Context(), client, mem, locArg, typeArg, limit, off, sinceArg, untilArg)
			if err != nil {
				return api.MapError(err)
			}
			rows := []eventDTO{}
			for _, e := range resp.UsageEvents {
				if e == nil {
					return exitcode.Newf(exitcode.Error, "the server returned an invalid usage-event row")
				}
				rows = append(rows, eventDTO{ID: e.Id, Type: e.Type, CreatedAt: e.CreatedAt, MemoryID: e.MemoryId, NodeLoc: e.NodeLoc, Provider: e.Provider, Model: e.Model, KeyOwnerType: e.KeyOwnerType, TokensIn: e.TokensIn, TokensOut: e.TokensOut, CostMicroCents: e.CostMicroCents})
			}
			fmt.Fprintf(f.IOStreams.ErrOut, "page at offset %d (limit %d); no total available\n", offset, limit)
			if len(rows) == limit {
				fmt.Fprintln(f.IOStreams.ErrOut, "more may exist; request another page with --offset", offset+len(rows))
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
	cmd.Flags().StringVar(&loc, "loc", "", "verbatim exact node loc to filter")
	cmd.Flags().StringVar(&eventType, "type", "", "exact event type to filter")
	cmd.Flags().StringVar(&since, "since", "", "ISO date-time with Z or an offset; events at or after it (inclusive)")
	cmd.Flags().StringVar(&until, "until", "", "ISO date-time with Z or an offset; events before it (exclusive)")
	cmd.Flags().IntVar(&limit, "limit", 20, "page size (1-200)")
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
		v := strings.TrimSpace(b.value)
		t, ok := parseStrictDateTime(v)
		if !ok {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--%s takes an ISO date-time with Z or an explicit offset, e.g. 2026-10-22T00:00:00Z, got %q", b.name, b.value)
		}
		out[i], at[i] = &v, t
	}
	if out[0] != nil && out[1] != nil && !at[0].Before(at[1]) {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--since must be before --until (since is inclusive, until exclusive)")
	}
	return out[0], out[1], nil
}

// isoDateTime is the strict shape the server accepts: two-digit fields, a
// dot (never comma) fraction, and Z or a +-HH:MM offset. time.Parse alone is too
// lenient (it takes +24:00, +00:60 and a comma fraction), and anything it lets
// through would cost a server round trip instead of a usage error.
var isoDateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$`)

func parseStrictDateTime(v string) (time.Time, bool) {
	m := isoDateTime.FindStringSubmatch(v)
	if m == nil {
		return time.Time{}, false
	}
	if m[1] != "" { // numeric offset: hours 00-23, minutes 00-59
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if h > 23 || mm > 59 {
			return time.Time{}, false
		}
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
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
