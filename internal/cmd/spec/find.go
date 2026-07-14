package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	specFindDefaultLimit = 15
	specFindPageSize     = 50
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
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "limit must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Honor the spec default (hadron spec use / active memory) even when
			// -m is omitted, so a bare `find` searches the configured corpus
			// instead of every accessible memory. Nothing configured ⇒ unscoped.
			var memURN string
			ref, rerr := effectiveSpecMemoryOptional(f, memory)
			if rerr != nil {
				return rerr
			}
			if ref != "" {
				memURN, err = resolveSpecMemoryURN(cmd, client, ref)
				if err != nil {
					return err
				}
			}
			resultLimit := limit
			if resultLimit == 0 {
				resultLimit = specFindDefaultLimit
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

			specs, degraded, reason, err := collectSpecFindResults(resultLimit, func(limit, offset int) (*api.FindNodesPage, error) {
				limitArg, offsetArg := limit, offset
				return api.FindNodes(cmd.Context(), client, &query, &mode, filter, nil, &limitArg, &offsetArg)
			})
			if err != nil {
				return api.MapError(err)
			}
			if note := degradedNote(degraded, reason); note != "" {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %s\n", note)
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
	cmd.Flags().IntVar(&limit, "limit", specFindDefaultLimit, "maximum number of spec results")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "additional tag filter (repeatable; --match-exactly only)")
	withFlagAliases(cmd, map[string]string{"exact": "match-exactly"})
	return cmd
}

func collectSpecFindResults(
	resultLimit int,
	fetch func(limit, offset int) (*api.FindNodesPage, error),
) ([]specDTO, *string, *string, error) {
	if resultLimit <= 0 {
		return []specDTO{}, nil, nil, nil
	}
	pageSize := specFindPageSize
	if pageSize < resultLimit {
		pageSize = resultLimit
	}
	specs := make([]specDTO, 0, resultLimit)
	var degraded, reason *string
	for offset := 0; len(specs) < resultLimit; offset += pageSize {
		page, err := fetch(pageSize, offset)
		if err != nil {
			return nil, nil, nil, err
		}
		if page == nil {
			break
		}
		if degraded == nil && page.Degraded != nil {
			degraded = page.Degraded
		}
		if reason == nil && page.Reason != nil {
			reason = page.Reason
		}
		for _, n := range page.Nodes {
			if n == nil || !isSpecNode(n.Tags, n.Loc) {
				continue
			}
			specs = append(specs, specDTO{Citation: n.Loc, MemoryID: n.MemoryId, Name: n.Name, NodeType: n.NodeType, Tags: n.Tags, UpdatedAt: n.UpdatedAt})
			if len(specs) == resultLimit {
				break
			}
		}
		if len(page.Nodes) < pageSize {
			break
		}
		if page.Total != nil && offset+pageSize >= *page.Total {
			break
		}
	}
	return specs, degraded, reason, nil
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
