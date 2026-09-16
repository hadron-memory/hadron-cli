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
//
// Scope is the fourth such signal (spec 049): under a lens, "5 hits" means
// something different than it does over everything the caller can read, and
// droppedCount says how much of the lens was invisible. An agent that cannot
// see which scope ran cannot interpret the count.
type resultDTO struct {
	Hits     []hitDTO  `json:"hits"`
	Total    *int      `json:"total"`
	Degraded *string   `json:"degraded,omitempty"`
	Reason   *string   `json:"reason,omitempty"`
	Scope    *scopeDTO `json:"scope,omitempty"`
}

// scopeDTO reports the scope a search RAN UNDER, exactly as the server
// resolved it. Every field is copied, never derived — the resolution ladder is
// the server's, and a client that inferred `source` would be a second
// implementation of it.
type scopeDTO struct {
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	Source       string   `json:"source"`
	OwnerURN     *string  `json:"ownerUrn"`
	MemoryURNs   []string `json:"memoryUrns"`
	DroppedCount int      `json:"droppedCount"`
}

// NewCmdSearch wires `hadron search` — the ranked node-retrieval front door.
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	var (
		memories   []string
		scope      string
		mode       string
		prefix     string
		nodeType   string
		objectType string
		tags       []string
		where      string
		sortProp   string
		limit      int
		offset     int
		long       bool
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
prints abstracts in the text output too.

--where takes a JSON predicate over the node's properties/data JSONB (a leaf is
a path plus one of eq|ne|in|lt|lte|gt|gte|between|exists|contains; branch with
and/or/not). --object-type filters the objectType collection facet.
--sort-property orders by a properties/data JSON path (overrides relevance).`,
		Example: `  hadron search "how do users report a bad actor" -m hrn:mem:micromentor.org:mmdata
  hadron search "rate limiting" -m hrn:mem:acme.com:kb -m hrn:mem:acme.com:ops --mode keyword --json
  hadron search "(auth OR login) AND token" --mode keyword --prefix findings:
  hadron search 'reportUser|contentConcern' --mode regex --limit 30
  hadron search "pricing" --object-type insight --where '{"path":["source"],"eq":"substack"}'
  hadron search "roadmap" --sort-property '{"path":["rank"],"as":"number","direction":"desc"}'`,
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
			whereArg, err := cmdutil.ParseNodeWhere(where)
			if err != nil {
				return err
			}
			sortPropArg, err := cmdutil.ParseNodePropertySort(sortProp)
			if err != nil {
				return err
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
			if objectType != "" {
				filter.ObjectType = &objectType
				filterSet = true
			}
			if len(tags) > 0 {
				filter.Tags = tags
				filterSet = true
			}
			if whereArg != nil {
				filter.Where = whereArg
				filterSet = true
			}
			// Send no filter object at all when nothing is constrained,
			// mirroring node list / spec find.
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

			var scopeArg *string
			if scope != "" {
				if err := requireAppContextForScope(f, scope); err != nil {
					return err
				}
				scopeArg = &scope
			}

			page, err := api.SearchNodes(cmd.Context(), client, query, modeArg, filterArg, sortPropArg, limitArg, offsetArg, scopeArg)
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
			if sc := page.Scope; sc != nil {
				urns := sc.MemoryUrns
				if urns == nil {
					urns = []string{}
				}
				result.Scope = &scopeDTO{
					Kind:         sc.Kind,
					Label:        sc.Label,
					Source:       sc.Source,
					OwnerURN:     sc.OwnerUrn,
					MemoryURNs:   urns,
					DroppedCount: sc.DroppedCount,
				}
			}
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
				// The scope header precedes the hits in BOTH layouts: it
				// changes what the result list means, so it cannot be a
				// footnote only the table branch prints.
				if line := scopeLine(result.Scope); line != "" {
					if _, err := io.WriteString(w, line+"\n"); err != nil {
						return err
					}
				}
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
	// -m/--memory is an unordered SET of memories and narrows WITHIN a scope;
	// --scope names a stored lens and is resolved server-side. Two different
	// ideas, so the -m help no longer says "scope" (#578).
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "restrict to a memory (ID or URN; repeatable; narrows within --scope)")
	cmd.Flags().StringVar(&scope, "scope", "", "search under a named scope: a scope name (needs an App context), a scope id, `app` (the App's attached memories), or `global` (your active organization's view)")
	cmd.Flags().StringVar(&mode, "mode", "hybrid", "ranking mode: hybrid|keyword|vector|regex")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by node loc prefix")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type")
	cmd.Flags().StringVar(&objectType, "object-type", "", "filter by objectType collection facet (e.g. competitor)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "filter by tag (repeatable)")
	cmd.Flags().StringVar(&where, "where", "", "structured predicate over properties/data as JSON (e.g. '{\"path\":[\"source\"],\"eq\":\"substack\"}')")
	cmd.Flags().StringVar(&sortProp, "sort-property", "", "order by a properties/data JSON path as JSON (e.g. '{\"path\":[\"rank\"],\"as\":\"number\",\"direction\":\"desc\"}')")
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

// The two scope keywords the server reserves. `app` resolves against the App
// context; `global` against the active organization, which is why only the
// first is guarded here — the CLI gains a way to select an organization in
// #578's slice 3, and until then `global` is the server's to refuse.
const (
	scopeApp    = "app"
	scopeGlobal = "global"
)

// scopeLine renders the scope disclosure for the human branches.
//
// Returns "" when no scope applied, so an unscoped search looks exactly as it
// did before #578. When one did apply, the line always names the scope and the
// rung that chose it — `source` matters because a scope the user did not type
// (an active App, a configured default) is precisely the one they need told
// about — and appends the dropped count when the server reports one.
func scopeLine(s *scopeDTO) string {
	if s == nil {
		return ""
	}
	label := s.Label
	if label == "" {
		label = s.Kind
	}
	line := fmt.Sprintf("scope: %s (%s, via %s)", label, s.Kind, s.Source)
	if s.DroppedCount > 0 {
		// Count only, never names: the server withholds them deliberately so a
		// lens cannot be used to enumerate memories the caller may not read.
		line += fmt.Sprintf(" — %d memory/memories in this scope are not readable by you and were not searched", s.DroppedCount)
	}
	return line
}

// requireAppContextForScope refuses, before the round trip, a --scope value the
// server can only resolve with an App context.
//
// The server does refuse these itself — but in the vocabulary of other
// surfaces: `pass appRef (GraphQL) or select an App (MCP)`. A CLI reader can
// type neither, so the remedy they are handed does not exist for them. Same
// defect as the `appRef` message `hadron scope get` used to leak (#594), and
// the same reason CanonicalAppRef exists (#540).
//
// This is CONTEXT ARITY, not resolution: which scope wins, and whether it
// exists, stay entirely server-side. Only `app` and a bare NAME need the
// context; a scope id is self-contained, and `global` keys off the active
// organization instead (see the note in requireAppContextForScope's caller).
func requireAppContextForScope(f *cmdutil.Factory, scope string) error {
	if scope == scopeGlobal || cmdutil.IsBareID(scope) {
		return nil
	}
	app, err := f.App()
	if err != nil {
		return err
	}
	if app != "" {
		return nil
	}
	if scope == scopeApp {
		return exitcode.Newf(exitcode.Usage,
			"--scope app means the active App's attached memories, and no App is selected — pass --app <ref> or run `hadron app set-active <ref>`")
	}
	return exitcode.Newf(exitcode.Usage,
		"--scope %q is a scope name, which resolves in an App's context — pass --app <ref>, run `hadron app set-active <ref>`, or give the scope's id instead", scope)
}
