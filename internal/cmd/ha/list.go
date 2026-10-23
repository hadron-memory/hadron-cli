package ha

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	// defaultLimit is the CLI's own page size, always sent explicitly. It is
	// NOT the server's default (50), which only applies to a request that
	// omits `limit`.
	defaultLimit = 20
	maxLimit     = 200
)

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var org string
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list [--org <ref>] [--limit N] [--offset N]",
		Aliases: []string{"ls"},
		Short:   "List registered Home Assistant instances",
		Long: fmt.Sprintf(`List registered Home Assistant instances, optionally scoped to one org.

Returns ONE page, slug-ascending: --limit 1..%d (default %d, sent explicitly;
the server's own default is 50) and --offset N. When more rows exist, stderr
says how to fetch the next page; the --json output stays a bare array.`, maxLimit, defaultLimit),
		Example: `  hadron ha list --org acme.com --json
  hadron ha list --limit 50 --offset 50`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > maxLimit {
				return exitcode.Newf(exitcode.Usage, "--limit must be between 1 and %d", maxLimit)
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--offset must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var orgRef *string
			if org != "" {
				orgRef = &org
			}
			// limit is always sent; offset 0 is omitted (a nil pointer).
			var offsetArg *int
			if offset > 0 {
				offsetArg = &offset
			}
			resp, err := gen.HomeAssistantInstances(cmd.Context(), client, orgRef, &limit, offsetArg)
			if err != nil {
				return api.MapError(err)
			}
			instances := []instanceDTO{}
			total := 0
			if resp != nil && resp.HomeAssistantInstances != nil {
				total = resp.HomeAssistantInstances.Total
				for _, it := range resp.HomeAssistantInstances.Items {
					if it == nil {
						continue
					}
					instances = append(instances, dtoFromFields(it.HomeAssistantInstanceFields))
				}
			}
			if next := offset + len(instances); len(instances) > 0 && next < total {
				fmt.Fprintf(f.IOStreams.ErrOut, "showing %d of %d; next page: --offset %d --limit %d\n", len(instances), total, next, limit)
			}
			return output.Write(f.IOStreams, f.JSON, instances, func(w io.Writer) error {
				table := output.NewTable(w, "ID", "SLUG", "NAME", "URL", "OPS", "STATUS")
				for _, s := range instances {
					table.Row(s.ID, s.Slug, s.Name, s.URL, s.allowlistLabel(), s.statusLabel())
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to scope the list to (ID or URN)")
	cmd.Flags().IntVar(&limit, "limit", defaultLimit, fmt.Sprintf("page size, 1..%d", maxLimit))
	cmd.Flags().IntVar(&offset, "offset", 0, "rows to skip")
	return cmd
}
