package object

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// findDTO is the stable --json envelope for a collection query. Objects is always
// non-nil so an empty result renders [], not null.
type findDTO struct {
	Objects []json.RawMessage `json:"objects"`
	Total   *int              `json:"total"`
}

func newCmdFind(f *cmdutil.Factory) *cobra.Command {
	var (
		memory     string
		objectType string
		match      string
		where      string
		sort       string
		limit      int
		offset     int
	)
	cmd := &cobra.Command{
		Use:     "find -m <memory> --type <type>",
		Aliases: []string{"list", "ls"},
		Short:   "Query a collection",
		Long: `List the records of one collection in a memory, then a count line
("2 shown, 2 total"). With no filter, every record of the type matches.

--match keeps records whose fields equal the given values: a JSON object
{"field": value, ...}; all must match.
--where takes a full condition (greater than, less than, in, exists, and/or/not)
in the JSON grammar of search --where; the second example keeps records with
fundingUsd over 10,000,000. --where and --match can be combined (both must hold).
--sort orders by one field: {"<field>": "asc"} or {"<field>": "desc"}.

At most 20 records are returned per page unless --limit says otherwise (at
most 200). "shown" counts this page only. To page, add each page's "shown" to
--offset and run again; stop when --offset plus "shown" reaches "total", or
when a page shows 0.`,
		Example: `  hadron object find -m hrn:mem:acme.com:market --type competitor --match '{"stage":"series-a"}'
  hadron object find -m hrn:mem:acme.com:market --type competitor \
    --where '{"path":["fundingUsd"],"as":"number","gt":10000000}' \
    --sort '{"fundingUsd":"desc"}' --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if objectType == "" {
				return exitcode.Newf(exitcode.Usage, "--type is required")
			}
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
			}
			matchArg, err := resolveJSON("--match", match, "")
			if err != nil {
				return err
			}
			whereArg, err := resolveJSON("--where", where, "")
			if err != nil {
				return err
			}
			sortArg, err := resolveJSON("--sort", sort, "")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var limitArg, offsetArg *int
			if limit > 0 {
				limitArg = &limit
			}
			if offset > 0 {
				offsetArg = &offset
			}
			resp, err := gen.FindObjects(cmd.Context(), client, cmdutil.CanonicalMemoryRef(memory), objectType, matchArg, whereArg, sortArg, limitArg, offsetArg)
			if err != nil {
				return api.MapError(err)
			}
			dto := findDTO{Objects: []json.RawMessage{}, Total: resp.FindObjects.Total}
			dto.Objects = append(dto.Objects, resp.FindObjects.Objects...)

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				for _, o := range dto.Objects {
					if _, werr := fmt.Fprintln(w, string(o)); werr != nil {
						return werr
					}
				}
				total := "?"
				if dto.Total != nil {
					total = fmt.Sprint(*dto.Total)
				}
				_, werr := fmt.Fprintf(w, "\n%d shown, %s total\n", len(dto.Objects), total)
				return werr
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&objectType, "type", "", "collection / object type (required)")
	cmd.Flags().StringVar(&match, "match", "", "equality shorthand: JSON object { field: value } ANDed into eq predicates")
	cmd.Flags().StringVar(&where, "where", "", "full structured predicate as JSON (same grammar as 'search --where')")
	cmd.Flags().StringVar(&sort, "sort", "", `single-field sort: {"<field>":"asc"|"desc"}`)
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of records (default 20, at most 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many records (for the next page)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}
