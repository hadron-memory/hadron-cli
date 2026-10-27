// Package usage exposes read-only provider usage metadata and counts.
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
	cmd := &cobra.Command{Use: "usage <command>", Short: "Read usage metadata (not billing balances)"}
	cmd.AddCommand(newEvents(f))
	return cmd
}
func newEvents(f *cmdutil.Factory) *cobra.Command {
	var memory, loc, eventType string
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "events [--memory <ref>] [--loc <loc>] [--type <type>] [--limit N] [--offset N]",
		Short: "Read one page of accessible usage events",
		Long: `Read provider usage metadata, most recent first, scoped by the server to
readable memories. Optional --memory accepts a memory id or URN (short forms
are normalized locally). --loc is an exact, verbatim loc filter, including
hierarchical locs; it is never sent as nodeId. --type is an exact event type.

This is ONE page: default limit 20, locally bounded to 1-200, nonnegative offset.
The provider supplies no total or time filter. Stderr labels the offset and
limit and says no total is available; a full page says more may exist. There
is no --since, automatic paging, cost sum or billing balance.

costMicroCents stays an arbitrary-size base-10 integer string (units: 1e-8 USD),
not a floating point value. Null means unpriced and prints unpriced, never 0.
Token nulls remain unknown. JSON is an array of metadata objects; empty is [].
A malformed null event row fails before any rows or page note are printed.
Action arguments and caller identity payloads are not selected. No accounting
record is written and no model/provider call is made.`,
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
			var off *int
			if offset != 0 {
				off = &offset
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UsageEvents(cmd.Context(), client, mem, locArg, typeArg, limit, off)
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
	cmd.Flags().IntVar(&limit, "limit", 20, "page size (1-200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "rows to skip (nonnegative)")
	return cmd
}
func tokenLabel(v *int) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprint(*v)
}

// NewCmdMemoryUsage is registered under memory; the server owns the aggregation.
func NewCmdMemoryUsage(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "usage <memoryRef>", Short: "Read server-reported usage counts by node loc and type", Long: `Read the provider's per-loc, per-type usage counts for one readable memory.
This is not a client sum over event pages, and not a cost or billing balance.
Historical events without an attributable memory/loc may be absent. The server
owns access and refusals. JSON is an array of {nodeLoc,type,count}; empty is [].`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
