package search

import (
	"bytes"
	"encoding/json"
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
//
// Properties and Data arrive only when --with-properties / --with-data asked
// for them (#602). NON-POINTER json.RawMessage, deliberately, and for the same
// reason as node list's row: the key must distinguish three states, and a
// pointer collapses two of them —
//
//	not requested      -> key ABSENT   (nil, len 0 — omitempty drops it)
//	requested, a value -> key = value
//	requested, null    -> key = null   (the 4-byte literal, len 4 — survives)
//
// encoding/json sets a *RawMessage to nil for a JSON `null`, so a pointer would
// make a requested-but-null column vanish exactly as an unrequested one does,
// and a caller could not tell "this node has none" from "you did not ask".
// The FLAG therefore decides what is present here, never the returned value:
// on the wire an unselected column and a selected-null one are identical.
type hitDTO struct {
	Score         *float64        `json:"score"`
	MemoryID      string          `json:"memoryId"`
	Loc           string          `json:"loc"`
	Name          string          `json:"name"`
	NodeType      string          `json:"nodeType"`
	Tags          []string        `json:"tags"`
	Description   *string         `json:"description"`
	Abstract      *string         `json:"abstract"`
	AbstractStale bool            `json:"abstractStale,omitempty"`
	UpdatedAt     string          `json:"updatedAt"`
	Properties    json.RawMessage `json:"properties,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

// resultDTO is the stable --json envelope: hits plus the retrieval-quality
// signals (total, degraded, reason) an agent needs to interpret them.
//
// Scope is the fourth such signal (spec 049): under a lens, "5 hits" means
// something different than it does over everything the caller can read, and
// droppedCount says how much of the lens was invisible. An agent that cannot
// see which scope ran cannot interpret the count.
type resultDTO struct {
	Hits []hitDTO `json:"hits"`
	// Passages are the chunk results of --granularity chunk (cli#1341). The
	// key is present only on such a search, so every other search's envelope
	// is unchanged.
	Passages []passageDTO `json:"passages,omitempty"`
	Total    *int         `json:"total"`
	Degraded *string      `json:"degraded,omitempty"`
	Reason   *string      `json:"reason,omitempty"`
	Scope    *scopeDTO    `json:"scope,omitempty"`
}

// passageDTO is one chunk result: where in which node the match sits.
type passageDTO struct {
	ParentNodeID  string `json:"parentNodeId"`
	ParentNodeURN string `json:"parentNodeUrn"`
	ChunkIndex    int    `json:"chunkIndex"`
	CharStart     int    `json:"charStart"`
	CharEnd       int    `json:"charEnd"`
}

// searchFields are the NodeField values --field takes.
var searchFields = map[string]gen.NodeField{}

func init() {
	for _, v := range gen.AllNodeField {
		searchFields[strings.ToLower(string(v))] = v
	}
}

// rankingArgs turns the ranking flags into findNodes' arguments, each only
// when passed (cli#1341). An explicit value outside the server's enums is
// refused here rather than sent.
func rankingArgs(cmd *cobra.Command, expand int, fields []string, granularity string, minScore float64) (api.SearchRanking, error) {
	var r api.SearchRanking
	changed := cmd.Flags().Changed
	if changed("expand") {
		if expand < 0 || expand > 3 {
			return r, exitcode.Newf(exitcode.Usage, "--expand must be 0 to 3 (graph hops). Nothing was sent")
		}
		r.Expand = &expand
	}
	for _, name := range fields {
		v, ok := searchFields[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return r, exitcode.Newf(exitcode.Usage, "invalid --field %q (want one of abstract, alias, content, data, description, loc, name, properties, tags). Nothing was sent", name)
		}
		r.Fields = append(r.Fields, v)
	}
	if changed("granularity") {
		g := gen.SearchGranularity(strings.ToLower(strings.TrimSpace(granularity)))
		if g != gen.SearchGranularityNode && g != gen.SearchGranularityChunk {
			return r, exitcode.Newf(exitcode.Usage, "invalid --granularity %q (want node or chunk). Nothing was sent", granularity)
		}
		r.Granularity = &g
	}
	if changed("min-score") {
		r.MinScore = &minScore
	}
	return r, nil
}

// scopeDTO reports the scope a search RAN UNDER, exactly as the server
// resolved it. Every field is copied, never derived — the resolution ladder is
// the server's, and a client that inferred `source` would be a second
// implementation of it.
type scopeDTO struct {
	// SelectedBy is CLIENT-side provenance: "flag" when the caller passed
	// --scope, "config" when it came from the stored default. Distinct from
	// Source below, which is the SERVER reporting which rung of its own ladder
	// resolved the string. Both are needed: the server cannot know a value
	// came from local config, and the client cannot know how the server
	// resolved it.
	SelectedBy   string   `json:"selectedBy"`
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

		withProperties bool
		withData       bool
		commentsOnly   bool

		expand      int
		fields      []string
		granularity string
		minScore    float64
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search nodes, ranked by relevance",
		Long: `Find notes (nodes) about a topic in the memories you can read, best match
first:

  hadron search "how do users report a bad actor"

Without -m or --scope it searches every memory you can read, unless you have
saved a default scope (hadron scope use <scope>): then it searches that scope,
and the first line of the result says so ("scope: ... [your default scope
...]"). --scope on the command line wins over the default, and
hadron scope use "" clears it. -m/--memory (an id or hrn:mem: URN,
repeatable) narrows the search to those memories, inside the scope if one
applies.

The result is a table of SCORE, LOC (where the note sits in its memory) and
NAME. A higher score is a better match; scores only compare hits within one
search, not across searches or modes. Read a hit with
hadron node get <loc> -m <memory>, or add --long to see each hit's
description and abstract here (--json always carries them). No rows means
no hits in what was searched (with --offset, none past that point).

How it matches (--mode):
  hybrid   (default) meaning and keywords together; on a memory without a
           vector index it falls back to keyword and says so on stderr
  keyword  words, with stemming; supports UPPERCASE AND/OR/NOT, "quoted
           phrases" and -term to exclude
  vector   meaning only; needs a memory with a vector index
  regex    a POSIX regular expression over the note text

Filtering on a note's fields (--where, --sort-property): besides its text, a
note can hold two sets of fields, both JSON. "properties" are the fields of a
structured record (for example a competitor's funding stage); "data" is a
free-form envelope a tool or import attached (for example authorName). To see
which a note has, run hadron node get <loc> -m <memory> --json. --where is a
JSON condition: a path plus one test (eq, ne, in, lt, lte, gt, gte, between,
exists, contains), combined with and/or/not. It reads "properties" unless the
condition says "field":"data". Aimed at the wrong one it usually finds
nothing (a test such as "exists":false can instead match notes that lack the
field), and only one case warns: a condition that names no field and returns
an empty first page gets a note on stderr suggesting "field":"data". So when
a --where search is empty, check which of the two the note keeps the value in.
--with-properties / --with-data show those fields for each hit, and
--sort-property orders by one instead of by score.
--object-type keeps only records of one collection (for example competitor).

Ranking: --field (repeatable) weights the match toward those fields in
keyword and regex modes; --min-score drops hits scoring below a floor (the
scale differs per mode: keyword roughly 0 to a few, vector 0 to 1, hybrid a
small fraction); --expand N (0-3) appends the hits' graph neighbours, unscored,
after the hits; --granularity chunk (vector mode) returns the matching
passages of each node: its id and URN, the chunk number and the character
range.

--comments-only searches feedback comments instead of notes. Comment hits are
feedback, not verified facts about the notes they are on.

--scope searches a saved scope instead: a scope name or id, "app" (the
memories of the App in use: --app, else the one set with hadron app
set-active), or "global" (your organization's view). A scope name also needs
that App.`,
		Example: `  hadron search "how do users report a bad actor" -m hrn:mem:micromentor.org:mmdata
  hadron search "needs clarification" --comments-only --mode keyword --json
  hadron search "rate limiting" -m hrn:mem:acme.com:kb -m hrn:mem:acme.com:ops --mode keyword --json
  hadron search "(auth OR login) AND token" --mode keyword --prefix findings:
  hadron search 'reportUser|contentConcern' --mode regex --limit 30
  hadron search "pricing" --object-type insight --where '{"path":["source"],"eq":"substack"}'
  hadron search "standup" --where '{"field":"data","path":["authorName"],"exists":true}' --with-data
  hadron search "roadmap" --sort-property '{"path":["rank"],"as":"number","direction":"desc"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			if strings.TrimSpace(query) == "" {
				return exitcode.Newf(exitcode.Usage, "query must not be empty")
			}
			if commentsOnly && nodeType != "" && nodeType != "comment" {
				return exitcode.Newf(exitcode.Usage, "--comments-only cannot be combined with --type %q; omit --type or use --type comment", nodeType)
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
			ranking, err := rankingArgs(cmd, expand, fields, granularity, minScore)
			if err != nil {
				return err
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
			if commentsOnly {
				kind := gen.NodeContentScopeComments
				filter.ContentScope = &kind
				filterSet = true
			}
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

			// The default scope applies only when --scope was not given. It
			// changes what a FLAGLESS search returns, which is why every
			// result says where the scope came from (selectedBy / the header
			// line): a narrowing the reader did not ask for and cannot see is
			// indistinguishable from missing data.
			selectedBy := "flag"
			if scope == "" {
				cfg, err := f.Config()
				if err != nil {
					return err
				}
				if def := cfg.Scope(); def != "" {
					scope, selectedBy = def, "config"
				}
			}

			var scopeArg, orgArg, appArg *string
			if scope != "" {
				appCtx, err := requireAppContextForScope(f, scope)
				if err != nil {
					return err
				}
				if appCtx != "" {
					appArg = &appCtx
				}
				scopeArg = &scope
				// The active organization is sent ONLY for `global`, which is
				// the one scope defined in terms of it. Sending it on every
				// search would silently narrow an unscoped one to that org —
				// a change to what a flagless `hadron search` returns, which
				// is not this slice's to make.
				if scope == scopeGlobal {
					cfg, err := f.Config()
					if err != nil {
						return err
					}
					if org := cfg.Org(); org != "" {
						orgArg = &org
					}
				}
			}

			page, err := api.SearchNodesProjected(cmd.Context(), client, query, modeArg, filterArg, sortPropArg, limitArg, offsetArg, scopeArg, orgArg, appArg, withProperties, withData, ranking)
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
			// The silent-zero note (#603) is NOT text-only, unlike the one
			// above: no field of this envelope carries it, so under --json the
			// caller would otherwise be handed a bare empty hit list — which is
			// precisely the unqualified zero two of us published a conclusion
			// off. stderr, so the stdout --json contract is untouched.
			// Only at offset 0 does the hit count answer "did the predicate
			// match anything": --offset past the last hit empties the page
			// while the predicate matched plenty, and a note fired on that
			// would send a correct caller after the wrong column (@codex, PR
			// #604). `page.Total` looks like the better source and is not — the
			// server leaves it null even on a 50-hit response.
			var matched *int
			if offset == 0 {
				n := len(page.Hits)
				matched = &n
			}
			if note := cmdutil.WhereDefaultColumnNote(whereArg, matched); note != "" {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %s\n", note)
			}

			result := resultDTO{Hits: []hitDTO{}, Total: page.Total, Degraded: page.Degraded, Reason: page.Reason}
			if sc := page.Scope; sc != nil {
				urns := sc.MemoryUrns
				if urns == nil {
					urns = []string{}
				}
				result.Scope = &scopeDTO{
					SelectedBy:   selectedBy,
					Kind:         sc.Kind,
					Label:        sc.Label,
					Source:       sc.Source,
					OwnerURN:     sc.OwnerUrn,
					MemoryURNs:   urns,
					DroppedCount: sc.DroppedCount,
				}
			}
			for _, h := range page.Hits {
				hit := hitDTO{
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
				}
				// Keyed off the FLAG, not off whether the server sent a value:
				// an unselected column and a selected-but-null one both arrive
				// as a nil pointer, so only the flag knows which was asked for.
				if withProperties {
					hit.Properties = cmdutil.ProjectedColumn(h.Node.Properties)
				}
				if withData {
					hit.Data = cmdutil.ProjectedColumn(h.Node.Data)
				}
				result.Hits = append(result.Hits, hit)
			}
			chunks := ranking.Granularity != nil && *ranking.Granularity == gen.SearchGranularityChunk
			if chunks {
				result.Passages = []passageDTO{}
				for _, p := range page.Passages {
					result.Passages = append(result.Passages, passageDTO{
						ParentNodeID: p.ParentNodeId, ParentNodeURN: p.ParentNodeUrn,
						ChunkIndex: p.ChunkIndex, CharStart: p.CharStart, CharEnd: p.CharEnd,
					})
				}
			}

			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				if commentsOnly {
					if _, err := fmt.Fprintln(w, "Feedback comments (not verified target content):"); err != nil {
						return err
					}
				}
				// The scope header precedes the hits in BOTH layouts: it
				// changes what the result list means, so it cannot be a
				// footnote only the table branch prints.
				if line := scopeLine(result.Scope); line != "" {
					if _, err := io.WriteString(w, line+"\n"); err != nil {
						return err
					}
				}
				// Either projection flag forces the block layout, for the
				// same reason as node list: a table column would have to
				// truncate an arbitrarily long JSON value, which is the same
				// defect as not showing it, wearing the look of an answer.
				if long || withProperties || withData {
					return writeBlocks(w, result.Hits, long)
				}
				t := output.NewTable(w, "SCORE", "LOC", "NAME")
				for _, h := range result.Hits {
					t.Row(formatScore(h.Score), output.SafeCell(h.Loc), output.SafeCell(h.Name))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if !chunks {
					return nil
				}
				if len(result.Passages) == 0 {
					_, err := fmt.Fprintln(w, "no passages")
					return err
				}
				pt := output.NewTable(w, "NODE", "CHUNK", "CHARS")
				for _, p := range result.Passages {
					pt.Row(output.SafeCell(p.ParentNodeURN), fmt.Sprint(p.ChunkIndex), fmt.Sprintf("%d-%d", p.CharStart, p.CharEnd))
				}
				return pt.Flush()
			})
		},
	}
	// -m/--memory is an unordered SET of memories and narrows WITHIN a scope;
	// --scope names a stored lens and is resolved server-side. Two different
	// ideas, so the -m help no longer says "scope" (#578).
	cmd.Flags().StringArrayVarP(&memories, "memory", "m", nil, "restrict to a memory (ID or URN; repeatable; narrows within --scope)")
	cmd.Flags().StringVar(&scope, "scope", "", `search a saved scope: a scope name or id (a name needs the App in use), "app" (that App's memories) or "global" (your organization's view, set with hadron org use)`)
	cmd.Flags().StringVar(&mode, "mode", "hybrid", "ranking mode: hybrid|keyword|vector|regex")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by node loc prefix")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type")
	cmd.Flags().StringVar(&objectType, "object-type", "", "filter by objectType collection facet (e.g. competitor)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "filter by tag (repeatable)")
	cmd.Flags().StringVar(&where, "where", "", cmdutil.WhereFlagUsage)
	cmd.Flags().StringVar(&sortProp, "sort-property", "", cmdutil.SortPropertyFlagUsage)
	cmd.Flags().IntVar(&limit, "limit", 15, "maximum number of hits; 0 lets the server choose")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "per-hit block output including description/abstract")
	cmd.Flags().BoolVar(&withProperties, "with-properties", false, "include each hit's properties JSONB in the output")
	cmd.Flags().BoolVar(&commentsOnly, "comments-only", false, "search feedback comments instead of content nodes (all ranking modes)")
	cmd.Flags().BoolVar(&withData, "with-data", false, "include each hit's data JSONB in the output")
	cmd.Flags().IntVar(&expand, "expand", 0, "append the hits' graph neighbours up to N hops (0-3), unscored")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "weight keyword/regex matching toward this field (repeatable): abstract, alias, content, data, description, loc, name, properties, tags")
	cmd.Flags().StringVar(&granularity, "granularity", "", "node (default) or chunk: matching passages, vector mode only")
	cmd.Flags().Float64Var(&minScore, "min-score", 0, "drop hits scoring below this (the scale differs per mode)")
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

// writeBlocks renders the per-hit block layout. Two different flags reach it
// and they ask for different things, so each half is gated separately:
// --long wants the abstract/description, --with-properties/--with-data want the
// JSONB columns. Passing --with-data alone must NOT start printing abstracts —
// that is --long's job, and a flag that quietly turns on a neighbour's output
// is the "output shape changed because of an unrelated flag" surprise this PR
// declined to introduce elsewhere.
func writeBlocks(w io.Writer, hits []hitDTO, showAbout bool) error {
	for i, h := range hits {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s  %s  %s\n", formatScore(h.Score), h.Loc, h.Name); err != nil {
			return err
		}
		if showAbout {
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
		// A column that was requested but is null prints `null` rather than
		// being skipped: an absent line reads as "not requested", which is the
		// ambiguity the projection exists to remove. The DTO already encodes
		// that distinction, so length is the test and the flags are not needed.
		for _, col := range []struct {
			label string
			raw   json.RawMessage
		}{{"properties", h.Properties}, {"data", h.Data}} {
			if len(col.raw) == 0 {
				continue
			}
			if _, err := fmt.Fprintf(w, "  %s: %s\n", col.label, compactJSON(col.raw)); err != nil {
				return err
			}
		}
	}
	return nil
}

// compactJSON strips insignificant whitespace so one column is one line. A
// value json.Compact cannot parse is returned VERBATIM rather than dropped or
// error-rendered: it came off the wire as the node's stored column, and showing
// it as-is is strictly more informative than showing nothing.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
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
	if s.SelectedBy == "config" {
		// Said plainly and with the remedy: this search was narrowed by a
		// setting, not by anything on this command line.
		line += " [your default scope — override with --scope, clear with `hadron scope use \"\"`]"
	}
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
// It RETURNS the resolved context rather than only validating it: an earlier
// version checked the App was present and then dropped it, so the server got a
// scope it could not resolve — validated-then-discarded is the shape of that
// bug, and returning the value is what makes it impossible (@codex, #595).
func requireAppContextForScope(f *cmdutil.Factory, scope string) (string, error) {
	if scope == scopeGlobal || cmdutil.IsBareID(scope) {
		return "", nil
	}
	app, err := f.App()
	if err != nil {
		return "", err
	}
	if app != "" {
		return app, nil
	}
	if scope == scopeApp {
		return "", exitcode.Newf(exitcode.Usage,
			"--scope app means the active App's attached memories, and no App is selected — pass --app <ref> or run `hadron app set-active <ref>`")
	}
	return "", exitcode.Newf(exitcode.Usage,
		"--scope %q is a scope name, which resolves in an App's context — pass --app <ref>, run `hadron app set-active <ref>`, or give the scope's id instead", scope)
}
