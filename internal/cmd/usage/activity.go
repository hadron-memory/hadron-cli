package usage

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type activityBucketDTO struct {
	Date   string `json:"date"`
	Reads  int    `json:"reads"`
	Writes int    `json:"writes"`
}

func newActivity(f *cmdutil.Factory) *cobra.Command {
	var from, to, bucket, zone string
	var memories []string
	cmd := &cobra.Command{
		Use:   "activity [--from <time>] [--to <time>] [--bucket DAY|WEEK|MONTH] [--memory <ref>]... [--time-zone <zone>]",
		Short: "Read bucketed read and write activity",
		Long: `Read the server's dated read/write counts, in the order returned. Sign in first.
With no --memory, the server selects your own activity. Repeat --memory with
memory IDs or URNs to select resource activity instead (all principals' counts
in those memories, not their identities). Refs you cannot read, or that do not
exist, are silently excluded by the server.
Counts may cover fewer memories than you named; the result does not identify
which refs contributed. If all refs are excluded, no buckets are returned.
Malformed refs are errors.
An empty or unreadable resource result never falls back to your own activity
(cor:acl:090:01). The server enforces reference-count limits and other rules.

--from is inclusive and --to exclusive (ISO date-times). --bucket is DAY, WEEK
or MONTH; --time-zone is an IANA zone such as Asia/Tokyo. Omitted flags are
omitted on the wire: the server defaults to seven days, DAY and UTC. These are
server defaults, not values the CLI computes. The server may shorten a requested
window; the CLI cannot report its resolved bounds. A date labels a bucket in the
requested zone; it is not converted into a UTC timestamp.

Text names the requested scope and prints the flags sent (or "server default"), then DATE, READS and WRITES.
The server does not echo its resolved window. Only returned buckets are printed:
there is no client zero-fill, sum, billing balance or invented total. --json is
an array of {date,reads,writes}, [] when empty, preserving returned order/values.
An explicitly empty flag is a usage error (exit 2); other validation and access
refusals come from the server. Nothing is recorded; no provider is called.`,
		Example: `  hadron usage activity
  hadron usage activity --memory hrn:mem:acme.com:notes --memory hrn:mem:acme.com:work --json
  hadron usage activity --from 2026-10-01T00:00:00Z --to 2026-10-08T00:00:00Z --bucket DAY --time-zone UTC`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var fromArg, toArg, zoneArg *string
			for _, flag := range []struct {
				name, value string
				dst         **string
			}{{"from", from, &fromArg}, {"to", to, &toArg}, {"time-zone", zone, &zoneArg}} {
				if cmd.Flags().Changed(flag.name) {
					if strings.TrimSpace(flag.value) == "" {
						return exitcode.Newf(exitcode.Usage, "--%s must not be empty; omit it for the server default", flag.name)
					}
					v := flag.value
					*flag.dst = &v
				}
			}
			var bucketArg *gen.UsageActivityBucketSize
			if cmd.Flags().Changed("bucket") {
				if strings.TrimSpace(bucket) == "" {
					return exitcode.Newf(exitcode.Usage, "--bucket must not be empty; omit it for the server default")
				}
				v := gen.UsageActivityBucketSize(bucket)
				bucketArg = &v
			}
			for _, ref := range memories {
				if strings.TrimSpace(ref) == "" {
					return exitcode.Newf(exitcode.Usage, "--memory must not be empty; omit it for own activity")
				}
			}
			// No client cap, resource resolution, filtering or fallback. In particular,
			// nil stays omitted; an explicit resource request is never retried unscoped.
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UsageActivity(cmd.Context(), client, fromArg, toArg, bucketArg, memories, zoneArg)
			if err != nil {
				return api.MapError(err)
			}
			rows := []activityBucketDTO{}
			for _, b := range resp.UsageActivity {
				if b == nil {
					return exitcode.Newf(exitcode.Error, "the server returned an invalid usage-activity bucket")
				}
				rows = append(rows, activityBucketDTO{Date: b.Date, Reads: b.Reads, Writes: b.Writes})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				scope := "own activity (no --memory sent)"
				if len(memories) > 0 {
					scope = fmt.Sprintf("requested resource union (refs as sent: %q; unreadable or unknown refs are excluded by the server, not reported)", memories)
				}
				if _, err := fmt.Fprintf(w, "scope: %s\n", scope); err != nil {
					return err
				}
				label := func(name, value string) string {
					if !cmd.Flags().Changed(name) {
						return "server default"
					}
					return fmt.Sprintf("%q", value)
				}
				if _, err := fmt.Fprintf(w, "from: %s; to: %s; bucket: %s; time-zone: %s (flags sent; resolved window not returned)\n", label("from", from), label("to", to), label("bucket", bucket), label("time-zone", zone)); err != nil {
					return err
				}
				table := output.NewTable(w, "DATE", "READS", "WRITES")
				for _, b := range rows {
					table.Row(output.SafeCell(b.Date), fmt.Sprint(b.Reads), fmt.Sprint(b.Writes))
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "inclusive start ISO date-time; omitted uses server default")
	cmd.Flags().StringVar(&to, "to", "", "exclusive end ISO date-time; omitted uses server default")
	cmd.Flags().StringVar(&bucket, "bucket", "", "server bucket enum DAY, WEEK or MONTH; omitted uses server default")
	cmd.Flags().StringArrayVar(&memories, "memory", nil, "resource memory ID or URN; repeat for a union, omit for own activity")
	cmd.Flags().StringVar(&zone, "time-zone", "", "IANA bucket zone; omitted uses server default")
	return cmd
}
