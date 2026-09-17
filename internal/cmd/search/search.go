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

--with-properties / --with-data add those columns to each hit, so a search can
show the field it just filtered on. They are opt-in because a large result with
full envelopes is a much bigger payload. In text output either flag switches
the table for a per-hit block; combine with --long to get abstracts as well.

--where takes a JSON predicate over one of the node's two JSONB columns (a leaf
is a path plus one of eq|ne|in|lt|lte|gt|gte|between|exists|contains; branch
with and/or/not). A leaf reads "properties" UNLESS it sets "field":"data" — so
a predicate aimed at a node's free-form data envelope must say so, or it
searches the wrong column and returns a silent zero. --sort-property takes the
same "field" key and the same default, and overrides relevance.
--object-type filters the objectType collection facet.`,
		Example: `  hadron search "how do users report a bad actor" -m hrn:mem:micromentor.org:mmdata
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

			page, err := api.SearchNodesProjected(cmd.Context(), client, query, modeArg, filterArg, sortPropArg, limitArg, offsetArg, scopeArg, orgArg, appArg, withProperties, withData)
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

			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
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
	cmd.Flags().StringVar(&scope, "scope", "", "search under a named scope: a scope name (needs an App context), a scope id, `app` (the App's attached memories), or `global` (your active organization's view — set it with `hadron org use`)")
	cmd.Flags().StringVar(&mode, "mode", "hybrid", "ranking mode: hybrid|keyword|vector|regex")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by node loc prefix")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type")
	cmd.Flags().StringVar(&objectType, "object-type", "", "filter by objectType collection facet (e.g. competitor)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "filter by tag (repeatable)")
	cmd.Flags().StringVar(&where, "where", "", cmdutil.WhereFlagUsage)
	cmd.Flags().StringVar(&sortProp, "sort-property", "", cmdutil.SortPropertyFlagUsage)
	cmd.Flags().IntVar(&limit, "limit", 15, "maximum number of hits (0 = server default)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "per-hit block output including description/abstract")
	cmd.Flags().BoolVar(&withProperties, "with-properties", false, "include each hit's properties JSONB in the output")
	cmd.Flags().BoolVar(&withData, "with-data", false, "include each hit's data JSONB in the output")
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
