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
		Aliases: []string{"ls"},
		Short:   "Query a collection",
		Long: `Query one collection in a memory and print the matching objects plus a total.

--match is an equality shorthand — a JSON object { field: value, … } ANDed into
an eq predicate per field (comparison type inferred from the schema).
--where is the full structured predicate (the same JSON grammar as
'search --where'), AND-combined with --match.
--sort is a single-field shorthand: { "<field>": "asc" | "desc" }.`,
		Example: `  hadron object find -m acme.com::market --type competitor --match '{"stage":"series-a"}'
  hadron object find -m acme.com::market --type competitor \
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
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of objects")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}
