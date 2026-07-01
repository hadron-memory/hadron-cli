package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdFind(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var matchExactly bool
	var limit int
	var tags []string
	cmd := &cobra.Command{
		Use:   "find <query>",
		Short: "Find specs by meaning (default) or literal keyword",
		Long: `Find specs by meaning. By default the query is matched semantically
(hybrid keyword + vector search); on a memory without a vector index this
degrades to keyword search with a note. --match-exactly forces literal
regex matching over name/loc/description/tags — use it for exact-fragment
matches (e.g. a citation), since keyword search is now FTS-ranked/stemmed
rather than substring.

Results are filtered to spec nodes.`,
		Example: `  hadron spec find "win back users who never engaged" -m micromentor.org::platform-specs
  hadron spec find "msg:010" -m micromentor.org::platform-specs --match-exactly`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var memURN string
			if memory != "" {
				memURN, err = resolveSpecMemoryURN(cmd, client, memory)
				if err != nil {
					return err
				}
			}
			var limitArg *int
			if limit > 0 {
				limitArg = &limit
			}

			var memoryArg *string
			if memURN != "" {
				memoryArg = &memURN
			}

			// --match-exactly wants literal-fragment matching. Keyword mode is
			// now FTS-ranked/stemmed (not substring), so exact matching runs as
			// mode:regex; the default fuzzy path stays hybrid (semantic +
			// keyword, degrading to keyword on a vector-less memory). Only the
			// exact path pins a server-side tag filter (spec + any --tag), as
			// the old `nodes`-backed path did — the fuzzy path scopes to specs
			// client-side via isSpecNode so citation-shaped nodes without the
			// `spec` tag aren't dropped.
			mode := gen.FindNodesModeHybrid
			var tagFilter []string
			if matchExactly {
				mode = gen.FindNodesModeRegex
				tagFilter = append([]string{"spec"}, tags...)
			}
			filter := newNodeFilter(memoryArg, nil, tagFilter)

			page, err := api.FindNodes(cmd.Context(), client, &query, &mode, filter, nil, limitArg, nil)
			if err != nil {
				return api.MapError(err)
			}
			if note := degradedNote(page.Degraded, page.Reason); note != "" {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %s\n", note)
			}
			specs := []specDTO{}
			for _, n := range page.Nodes {
				if n == nil || !isSpecNode(n.Tags, n.Loc) {
					continue
				}
				specs = append(specs, specDTO{Citation: n.Loc, MemoryID: n.MemoryId, Name: n.Name, NodeType: n.NodeType, Tags: n.Tags, UpdatedAt: n.UpdatedAt})
			}

			return output.Write(f.IOStreams, f.JSON, specs, func(w io.Writer) error {
				t := output.NewTable(w, "CITATION", "NAME")
				for _, s := range specs {
					t.Row(s.Citation, s.Name)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "scope to a memory (ID or fully-qualified URN)")
	cmd.Flags().BoolVar(&matchExactly, "match-exactly", false, "literal keyword search instead of semantic (alias: --exact)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of results")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "additional tag filter (repeatable; --match-exactly only)")
	withFlagAliases(cmd, map[string]string{"exact": "match-exactly"})
	return cmd
}

// isSpecNode reports whether a search hit is a spec: it carries the spec
// tag or its loc is a valid citation.
func isSpecNode(tags []string, loc string) bool {
	if hasTag(tags, "spec") {
		return true
	}
	_, err := ParseCitation(loc)
	return err == nil
}

func degradedNote(degraded, reason *string) string {
	var parts []string
	if degraded != nil && strings.TrimSpace(*degraded) != "" {
		parts = append(parts, "search degraded: "+strings.TrimSpace(*degraded))
	}
	if reason != nil && strings.TrimSpace(*reason) != "" {
		parts = append(parts, strings.TrimSpace(*reason))
	}
	return strings.Join(parts, " — ")
}
