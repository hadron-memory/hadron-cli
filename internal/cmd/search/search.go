package search

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

// hitDTO is the stable --json shape of one search hit.
type hitDTO struct {
	Score         *float64 `json:"score"`
	MemoryID      string   `json:"memoryId"`
	Loc           string   `json:"loc"`
	Name          string   `json:"name"`
	NodeType      string   `json:"nodeType"`
	Tags          []string `json:"tags"`
	Description   *string  `json:"description"`
	Abstract      *string  `json:"abstract"`
	AbstractStale bool     `json:"abstractStale,omitempty"`
	UpdatedAt     string   `json:"updatedAt"`
}

// resultDTO is the stable --json envelope: hits plus the retrieval-quality
// signals (total, degraded, reason) an agent needs to interpret them.
type resultDTO struct {
	Hits     []hitDTO `json:"hits"`
	Total    *int     `json:"total"`
	Degraded *string  `json:"degraded,omitempty"`
	Reason   *string  `json:"reason,omitempty"`
}

// NewCmdSearch wires `hadron search` — the ranked node-retrieval front door.
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	var (
		memories []string
		mode     string
		prefix   string
		nodeType string
		tags     []string
		limit    int
		offset   int
		long     bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search nodes, ranked by relevance",
		Long: `Search nodes you can access, ranked by relevance.

The default mode is hybrid (semantic + keyword, fused); on a memory without
a vector index it degrades to keyword with a note on stderr. --mode selects
keyword (stemmed full-text with boolean operators — UPPERCASE AND/OR/NOT,
quoted phrases, -term), vector (semantic only), or regex (POSIX, literal
fragments).

-m/--memory scopes to a memory (ID or fully-qualified URN); repeat it to
search several. Omit for everything you can access.

Each hit carries a score plus the node's description and abstract (--json),
so results are assessable without a follow-up 'node get' per hit. --long
prints abstracts in the text output too.`,
		Example: `  hadron search "how do users report a bad actor" -m micromentor.org::mmdata
  hadron search "rate limiting" -m acme.com::kb -m acme.com::ops --mode keyword --json
  hadron search "(auth OR login) AND token" --mode keyword --prefix findings:
  hadron search 'reportUser|contentConcern' --mode regex --limit 30`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			if strings.TrimSpace(query) == "" {
				return exitcode.Newf(exitcode.Usage, "query must not be empty")
			}
			modeArg, err := parseMode(mode)
			if err != nil {
				return err
			}
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "limit must be non-negative")
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "offset must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var filter gen.NodeFilter
			var filterSet bool
			if len(memories) > 0 {
				filter.MemoryIds = memories
				filterSet = true
			}
			if prefix != "" {
				filter.LocPrefix = &prefix
				filterSet = true
			}
			if nodeType != "" {
				filter.NodeType = &nodeType
				filterSet = true
			}
			if len(tags) > 0 {
				filter.Tags = tags
				filterSet = true
			}
			// Send no filter object at all when nothing is constrained,
			// mirroring node ls / spec find.
			var filterArg *gen.NodeFilter
			if filterSet {
				filterArg = &filter
			}
			var limitArg, offsetArg *int
			if limit > 0 {
				limitArg = &limit
			}
			if offset > 0 {
				offsetArg = &offset
			}

			page, err := api.SearchNodes(cmd.Context(), client, query, modeArg, filterArg, limitArg, offsetArg)
			if err != nil {
				return api.MapError(err)
			}
			// The --json envelope already carries degraded/reason; the
			// human-readable note is text-mode only.
			if !f.JSON {
				if note := degradedNote(page.Degraded, page.Reason); note != "" {
					fmt.Fprintf(f.IOStreams.ErrOut, "note: %s\n", note)
				}
			}

			result := resultDTO{Hits: []hitDTO{}, Total: page.Total, Degraded: page.Degraded, Reason: page.Reason}
			for _, h := range page.Hits {
				result.Hits = append(result.Hits, hitDTO{
					Score:         h.Score,
					MemoryID:      h.Node.MemoryId,
					Loc:           h.Node.Loc,
					Name:          h.Node.Name,
					NodeType:      h.Node.NodeType,
					Tags:          h.Node.Tags,
					Description:   h.Node.Description,
					Abstract:      h.Node.Abstract,
					AbstractStale: h.AbstractStale,
					UpdatedAt:     h.Node.UpdatedAt,
				})
			}

			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				if long {
					return writeLong(w, result.Hits)
				}
				t := output.NewTable(w, "SCORE", "LOC", "NAME")
				for _, h := range result.Hits {
					t.Row(formatScore(h.Score), h.Loc, h.Name)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "scope to a memory (ID or URN; repeatable)")
	cmd.Flags().StringVar(&mode, "mode", "hybrid", "ranking mode: hybrid|keyword|vector|regex")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by node loc prefix")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "filter by tag (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 15, "maximum number of hits (0 = server default)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "per-hit block output including description/abstract")
	return cmd
}

func parseMode(s string) (*gen.FindNodesMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "hybrid":
		m := gen.FindNodesModeHybrid
		return &m, nil
	case "keyword":
		m := gen.FindNodesModeKeyword
		return &m, nil
	case "vector":
		m := gen.FindNodesModeVector
		return &m, nil
	case "regex":
		m := gen.FindNodesModeRegex
		return &m, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "invalid --mode %q (expected hybrid, keyword, vector, or regex)", s)
	}
}

func formatScore(score *float64) string {
	if score == nil {
		return "-"
	}
	return fmt.Sprintf("%.3f", *score)
}

func writeLong(w io.Writer, hits []hitDTO) error {
	for i, h := range hits {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s  %s  %s\n", formatScore(h.Score), h.Loc, h.Name); err != nil {
			return err
		}
		about := ""
		if h.Abstract != nil && strings.TrimSpace(*h.Abstract) != "" {
			about = strings.TrimSpace(*h.Abstract)
			if h.AbstractStale {
				about += "\n  (abstract may be stale)"
			}
		} else if h.Description != nil && strings.TrimSpace(*h.Description) != "" {
			about = strings.TrimSpace(*h.Description)
		}
		if about != "" {
			for _, line := range strings.Split(about, "\n") {
				if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func degradedNote(degraded, reason *string) string {
	var parts []string
	if degraded != nil && strings.TrimSpace(*degraded) != "" {
		parts = append(parts, "search degraded: "+strings.TrimSpace(*degraded))
	}
	if reason != nil && strings.TrimSpace(*reason) != "" {
		parts = append(parts, strings.TrimSpace(*reason))
	}
	return strings.Join(parts, "; ")
}
