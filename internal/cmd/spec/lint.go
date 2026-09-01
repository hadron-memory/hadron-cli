package spec

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	sevError   = "error"
	sevWarning = "warning"
	sevInfo    = "info"
)

// abstractSoftMax is the corpus-convention upper bound on a spec abstract, in
// characters. It is deliberately well below the server's generic 2000-char cap
// (spec 031) but far above the 1000 originally proposed in #347: measured
// against the production embedding model (nomic-embed-text-v1.5), retrieval
// quality is a broad plateau from roughly 700 to 1700 characters, and only
// past ~2400 does added length measurably cost anything. 1600 marks the top of
// that plateau — the point where more text has stopped paying for itself —
// rather than an optimum. See docs/plans/spec-abstract-length.md.
const abstractSoftMax = 1600

var (
	// Matches the "what invalidates" statement whether it's a heading
	// (## What invalidates …) or inline bold (**What invalidates:** …),
	// both of which the platform-specs corpus uses.
	reInvalidates = regexp.MustCompile(`(?im)^\s*[#*]*\s*what invalidates`)
)

func newCmdLint(f *cmdutil.Factory) *cobra.Command {
	var memory, product, module, prefixFlag string
	var all, strict bool
	cmd := &cobra.Command{
		Use:     "lint [<citation>]",
		Aliases: []string{"check", "validate"},
		Short:   "Validate specs against the rubric and stability rules",
		Long: fmt.Sprintf(`Validate one spec, a subtree, a product, a module, or the whole
corpus against the loc-as-citation rubric and stability rules.

Scope is one of: a single <citation> argument, --prefix <citation> (that
node plus its descendants — e.g. one feature and its rules), --product
<ppp>, --module <mmm> (optionally within --product), or --all. Errors
(rubric/stability violations) exit with code 5; --strict promotes warnings
to errors too.

Rule abstract-length warns above ~%d characters. That is a ceiling, not
a target: retrieval holds up across roughly 700-1700 characters, so a long
abstract is only worth shortening once it has stopped being about one
subject. Off-topic sentences dilute the embedding far more than length.`, abstractSoftMax),
		Example: `  hadron spec lint msg:010:02 -m hrn:mem:micromentor.org:platform-specs
  hadron spec lint --prefix cor:api:140 -m hrn:mem:hadronmemory.com:specs
  hadron spec lint --module msg -m hrn:mem:micromentor.org:platform-specs
  hadron spec lint --product cli -m hrn:mem:hadronmemory.com:platform-specs
  hadron spec lint --all -m hrn:mem:micromentor.org:platform-specs --strict`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := lintScopeError(len(args) == 1, prefixFlag, product, module, all); err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			var nodes []specNode
			var corpus bool
			// scopeRoot is the loc at the top of a --product/--module scope; it
			// bounds the cross-node parent-exists check (see lintCorpus). "" means
			// the whole corpus (--all), where every parent must be present.
			var scopeRoot string
			switch {
			case len(args) == 1:
				n, err := fetchSpecNode(cmd, client, memURN, args[0])
				if err != nil {
					return err
				}
				nodes = []specNode{nodeFromGQL(n)}
			case prefixFlag != "":
				// A citation prefix — that node plus its descendants (one feature
				// and its rules, a module, etc.). Mirrors `spec get --prefix`;
				// linted as a corpus so subtree parent-exists checks run.
				scopeRoot = prefixFlag
				nodes, err = scanPrefixDetail(cmd, client, memURN, prefixFlag)
				if err != nil {
					return err
				}
				if len(nodes) == 0 {
					return exitcode.Newf(exitcode.NotFound, "no specs found under %q", prefixFlag)
				}
				corpus = true
			case product != "" || module != "":
				if product != "" && !reModule.MatchString(product) {
					return exitcode.Newf(exitcode.Usage, "--product %q must be 3 lowercase letters", product)
				}
				if module != "" && !reModule.MatchString(module) {
					return exitcode.Newf(exitcode.Usage, "--module %q must be 3 lowercase letters", module)
				}
				prefix := module
				if product != "" {
					prefix = Citation{Product: product, Module: module}.Format()
				}
				scopeRoot = prefix
				nodes, err = scanPrefixDetail(cmd, client, memURN, prefix)
				if err != nil {
					return err
				}
				// A bare --module finds nothing in a product-rooted corpus because
				// the loc is <product>:<module>. Rather than dead-end, infer the
				// product when the memory declares exactly one; list them when it's
				// ambiguous (issue #99 item 4).
				if len(nodes) == 0 && product == "" {
					products, derr := discoverProducts(cmd, client, memURN)
					if derr != nil {
						return derr
					}
					switch len(products) {
					case 1:
						product = products[0]
						prefix = Citation{Product: product, Module: module}.Format()
						scopeRoot = prefix
						fmt.Fprintf(f.IOStreams.ErrOut, "note: inferred --product %s (the memory's only product)\n", product)
						nodes, err = scanPrefixDetail(cmd, client, memURN, prefix)
						if err != nil {
							return err
						}
					case 0:
						// A flat (or empty) corpus has no product to infer — the
						// module simply isn't here. Fail loudly without the
						// misleading "scope with --product" hint.
						return exitcode.Newf(exitcode.NotFound, "no specs found under %q", prefix)
					default:
						return exitcode.Newf(exitcode.Usage,
							"module %q is ambiguous — the memory declares multiple products (%s); scope with --product <ppp>",
							module, strings.Join(products, ", "))
					}
				}
				// Reachable only with product set (given, or inferred above), so
				// no "scope with --product" hint is warranted here.
				if len(nodes) == 0 {
					return exitcode.Newf(exitcode.NotFound, "no specs found under %q", prefix)
				}
				corpus = true
			case all:
				nodes, err = scanAllSpecsDetail(cmd, client, memURN)
				if err != nil {
					return err
				}
				corpus = true
			default:
				return exitcode.Newf(exitcode.Usage, "specify a <citation>, --prefix <citation>, --product <ppp>, --module <mmm>, or --all")
			}

			findings := []lintFindingDTO{}
			if corpus {
				findings = lintCorpus(nodes, scopeRoot, memURN)
			} else {
				for _, n := range nodes {
					findings = append(findings, lintNode(n)...)
				}
			}
			// A spec corpus leans on the abstract being embedded for semantic
			// `spec find`; warn once if the memory has no vector index (#42).
			if vw := vectorIndexWarning(cmd, client, memURN); vw != nil {
				findings = append(findings, *vw)
			}
			if strict {
				for i := range findings {
					if findings[i].Severity == sevWarning {
						findings[i].Severity = sevError
					}
				}
			}

			hasError := false
			for _, fnd := range findings {
				if fnd.Severity == sevError {
					hasError = true
					break
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, findings, func(w io.Writer) error {
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ %d spec(s) OK\n", len(nodes))
					return nil
				}
				t := output.NewTable(w, "CITATION", "SEVERITY", "RULE", "MESSAGE")
				for _, fnd := range findings {
					t.Row(fnd.Citation, fnd.Severity, fnd.Rule, fnd.Message)
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVar(&prefixFlag, "prefix", "", "lint every spec under this citation prefix (that node + its descendants, e.g. a feature: cor:api:140)")
	cmd.Flags().StringVar(&product, "product", "", "lint every spec under this product")
	cmd.Flags().StringVar(&module, "module", "", "lint every spec under this module (optionally within --product)")
	cmd.Flags().BoolVar(&all, "all", false, "lint every spec in the memory")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	return cmd
}

// lintScopeError enforces that exactly one scope selector is used: a positional
// <citation>, --prefix, --product/--module, or --all are mutually exclusive.
// Returns a usage error describing the conflict, or nil when the combination is
// valid (including the no-scope case, which the RunE switch's default handles).
func lintScopeError(hasCitationArg bool, prefixFlag, product, module string, all bool) error {
	if hasCitationArg && (product != "" || module != "" || all || prefixFlag != "") {
		return exitcode.Newf(exitcode.Usage, "a <citation> argument cannot be combined with --prefix/--product/--module/--all")
	}
	if prefixFlag != "" && (product != "" || module != "" || all) {
		return exitcode.Newf(exitcode.Usage, "--prefix cannot be combined with --product/--module/--all")
	}
	if all && (product != "" || module != "") {
		return exitcode.Newf(exitcode.Usage, "--all cannot be combined with --product/--module")
	}
	return nil
}

// lintNode runs the per-node rules and returns findings tagged with the
// node's citation. Header nodes (module/feature, level < 3) only get the
// universal checks; the rubric proper applies to rules and flows.
func lintNode(n specNode) []lintFindingDTO {
	var fs []lintFindingDTO
	add := func(rule, sev, msg string) {
		fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: rule, Severity: sev, Message: msg})
	}
	if n.Unavailable {
		add("unavailable", sevError, "node was listed by the corpus scan but could not be read")
		return fs
	}

	c, err := ParseCitation(n.Loc)
	if err != nil {
		add("loc-shape", sevError, "loc is not a valid citation: "+err.Error())
	}
	if !strings.HasPrefix(n.Name, n.Loc+" — ") {
		add("name-prefix", sevError, fmt.Sprintf("name must start with %q", n.Loc+" — "))
	}
	if n.NodeType != "info" {
		add("nodetype-info", sevError, fmt.Sprintf("nodeType must be \"info\", got %q", n.NodeType))
	}
	if !hasTag(n.Tags, "spec") {
		add("tag-spec", sevError, `missing "spec" tag`)
	}

	// #545. A UNIVERSAL check, above the tier early-returns: one field having
	// absorbed another is corruption at any level, and a header node can be
	// corrupted exactly as a rule can. Error, not warning — the absorbed field
	// may be the only copy of what it held, so this must fail a corpus run
	// rather than accumulate in a warning list somebody skims.
	//
	// Reported per FIELD: knowing it is the abstract rather than the body is
	// what tells an author which one swallowed the other, and it is the first
	// thing they need before touching either.
	for _, field := range []struct {
		name string
		text *string
	}{{"abstract", n.Abstract}, {"body", n.Content}} {
		if field.text == nil {
			continue
		}
		if leaked := leakedMarkers(*field.text); len(leaked) > 0 {
			add("serialization-leak", sevError, fmt.Sprintf(
				"%s contains writing-tool markup (%s) — a field has absorbed another, and the absorbed one may be the only copy; recover it before editing, and do NOT truncate at the marker",
				field.name, strings.Join(leaked, ", ")))
		}
	}

	if err != nil || c.Level() < 3 {
		return fs
	}

	// A feature's :00 contract is co-scaffolded automatically with every new
	// feature (#69) so the feature's rules have an inheritance target — but
	// contracts are rare by convention, so creating a feature shouldn't force
	// authoring one. While its abstract is still the scaffold placeholder the
	// author hasn't engaged it, so it's exempt from the rubric errors (#99
	// item 1); replacing the placeholder abstract restores the full rubric.
	if c.IsContract() && isPlaceholderAbstract(n.Abstract) {
		add("placeholder-contract", sevInfo, "untouched placeholder contract — exempt from the rubric until a rule needs its shared provisions and you author it")
		return fs
	}

	// #545. Below the placeholder early-return ON PURPOSE: a genuinely untouched
	// contract returns above and is reported once, as placeholder-contract.
	// Reaching here with a scaffold body therefore means the INTERESTING case —
	// an authored abstract over a body nobody wrote, which is exactly what
	// cor:api:090:00 was after its provisions leaked into the abstract.
	//
	// This is the gap that let an unauthored spec look authored: the one rubric
	// check that reads the body tests for a "what invalidates" heading, and the
	// scaffold SHIPS with that heading, so a never-written body passes it.
	//
	// Warning rather than error, and a regression guard rather than a cleanup
	// driver: the blast radius across the corpus was ZERO when this shipped,
	// because both scaffold bodies belong to the two exempt contracts above. It
	// earns its place by catching the next one.
	if isScaffoldBody(n.Content) {
		add("scaffold-body", sevWarning,
			"body is still the `spec new` scaffold — its filler prose is unreplaced, so this spec is unauthored even though its abstract reads otherwise")
	}

	// Rubric proper. Top-level specs (rules) are the compliance-loadable
	// retrieval surface, so a missing abstract or invalidation is an error;
	// flows are pulled on demand, so the same gaps are advisory there.
	rubricSev := sevError
	if c.Level() == 4 {
		rubricSev = sevWarning
	}
	if !abstractPresent(n.Abstract) {
		add("abstract", rubricSev, "missing abstract — the vector-search retrieval surface (or still a placeholder); state the questions this spec answers, in your own words, keeping every sentence on its topic")
	} else if l := abstractLength(n.Abstract); l > abstractSoftMax {
		// Length itself is a weak lever — an on-topic abstract costs almost
		// nothing up to the server's cap — so this is advisory even at the
		// rule tier, and info-level for flows. What actually dilutes the
		// vector is off-topic material, which the message points at.
		sev := sevWarning
		if c.Level() == 4 {
			sev = sevInfo
		}
		add("abstract-length", sev, fmt.Sprintf(
			"abstract is %d chars — past ~%d added length stops paying for itself; distill it, and check every sentence is still about this spec (off-topic sentences dilute the vector far more than length does)",
			l, abstractSoftMax))
	}
	if n.Content == nil || !reInvalidates.MatchString(*n.Content) {
		add("invalidates", rubricSev, `body should state what invalidates this spec`)
	}
	if n.DataVersion == "" {
		add("data-version", sevWarning, "data.version is not set (expected e.g. 0.0.1)")
	}
	if p, ok := c.Parent(); ok && !hasOutEdgeTo(n, p.Format()) {
		add("toc-edge", sevWarning, "no table-of-contents edge to parent "+p.Format())
	}
	return fs
}

// lintCorpus runs the per-node rules on every node plus the cross-node
// checks (collisions, parent existence, inheritance edges). scopeRoot is the
// loc at the top of a --product/--module scope (e.g. "cor:acl"); the
// parent-exists check is suppressed for a parent that lives above it, since a
// scoped scan deliberately omits the subtree's attach point. An empty
// scopeRoot lints the whole corpus (--all), where every parent must exist.
// memURN qualifies the node refs in the inheritance-edge remedy message so the
// suggested `hadron edge add` command is copy-pasteable.
func lintCorpus(nodes []specNode, scopeRoot, memURN string) []lintFindingDTO {
	fs := []lintFindingDTO{}
	locCount := map[string]int{}
	contracts := map[string]bool{}
	productCodes := map[string]bool{}
	flatCodes := map[string]bool{}
	for _, n := range nodes {
		fs = append(fs, lintNode(n)...)
		locCount[n.Loc]++
		if n.Unavailable {
			continue
		}
		c, err := ParseCitation(n.Loc)
		if err != nil {
			continue
		}
		if c.IsContract() {
			contracts[c.Format()] = true
		}
		switch {
		case c.Product != "":
			productCodes[c.Product] = true
		case c.Feature != "": // a flat module with a numeric child
			flatCodes[c.Module] = true
		}
	}

	dupReported := map[string]bool{}
	for _, n := range nodes {
		if n.Unavailable {
			continue
		}
		c, err := ParseCitation(n.Loc)
		if err != nil {
			continue
		}
		if locCount[n.Loc] > 1 && !dupReported[n.Loc] {
			dupReported[n.Loc] = true
			fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "duplicate-loc", Severity: sevError, Message: "duplicate citation — two nodes share this loc"})
		}
		if p, ok := c.Parent(); ok {
			pLoc := p.Format()
			if locCount[pLoc] == 0 && parentInScope(pLoc, scopeRoot) {
				fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "parent-exists", Severity: sevError, Message: "parent " + pLoc + " does not exist"})
			}
		}
		// Any non-contract node inherits the reserved contract at its tier
		// (rule→feature:00, feature→module:000, product-rooted module→product:gen).
		if !c.IsContract() {
			if cl, ok := c.InheritedContractLoc(); ok && contracts[cl.Format()] && !hasOutEdgeTo(n, cl.Format()) {
				fs = append(fs, lintFindingDTO{
					Citation: n.Loc, Rule: "inheritance-edge", Severity: sevWarning,
					Message: fmt.Sprintf("no inheritance edge to general-provisions contract %s — add it: hadron edge add --from %s --to %s --label %q",
						cl.Format(), specNodeRef(memURN, n.Loc), specNodeRef(memURN, cl.Format()), inheritEdgeLabel),
				})
			}
		}
	}

	// Hygiene: a memory should be all-flat or all-product, never both.
	if len(productCodes) > 0 && len(flatCodes) > 0 {
		fs = append(fs, lintFindingDTO{
			Citation: "(memory)", Rule: "mixed-arity", Severity: sevWarning,
			Message: "memory mixes flat (" + strings.Join(sortedStringKeys(flatCodes), ", ") + ") and product-rooted (" + strings.Join(sortedStringKeys(productCodes), ", ") + ") citations — keep one arity per memory",
		})
	}
	return fs
}

// vectorIndexWarning returns a memory-level warning when the target memory has
// no vector index: its spec abstracts are not embedded, so semantic `spec find`
// silently degrades to keyword search and the abstract — the documented RAG
// retrieval surface every spec must carry — isn't actually serving retrieval
// (issue #42). Best-effort: the node scan already proved the memory reachable,
// so a failed lookup skips the check rather than failing the lint.
func vectorIndexWarning(cmd *cobra.Command, client graphql.Client, memURN string) *lintFindingDTO {
	memID, _, err := resolveSpecMemoryID(cmd, client, memURN)
	if err != nil {
		return nil
	}
	resp, err := gen.GetMemory(cmd.Context(), client, memID)
	if err != nil || resp.Memory == nil || resp.Memory.VectorIndexEnabled {
		return nil
	}
	return &lintFindingDTO{
		Citation: "(memory)",
		Rule:     "vector-index",
		Severity: sevWarning,
		Message:  "memory has no vector index — spec abstracts are not embedded and semantic `spec find` degrades to keyword search",
	}
}

func sortedStringKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- small predicates ----

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func abstractPresent(a *string) bool {
	if a == nil {
		return false
	}
	s := strings.TrimSpace(*a)
	return s != "" && !strings.Contains(s, abstractPlaceholder)
}

// abstractLength counts the abstract in characters (runes), matching how the
// server measures its own 2000-char cap for the text a spec corpus actually
// carries. Trimmed first so trailing editor whitespace never tips the bound.
// utf8.RuneCountInString rather than len([]rune(…)): a corpus lint scans every
// node, and this counts without allocating a rune slice per abstract.
func abstractLength(a *string) int {
	if a == nil {
		return 0
	}
	return utf8.RuneCountInString(strings.TrimSpace(*a))
}

// isPlaceholderAbstract reports whether an abstract still carries the scaffold
// marker — the signal that the author hasn't engaged the node yet. Distinct
// from !abstractPresent: an empty or absent abstract is a node whose abstract
// was removed, not an untouched scaffold.
func isPlaceholderAbstract(a *string) bool {
	return a != nil && strings.Contains(*a, abstractPlaceholder)
}

func hasOutEdgeTo(n specNode, targetLoc string) bool {
	for _, e := range n.OutEdges {
		if e.Loc == targetLoc {
			return true
		}
	}
	return false
}

// parentInScope reports whether a node's parent loc falls within the linted
// scope. With no scope (scopeRoot == "") the whole corpus is in scope, so
// every parent must exist. Otherwise only the scope root and its descendants
// are in scope: a parent above the scope root is the subtree's attach point,
// intentionally absent from a scoped scan, so flagging it missing would be a
// false positive (issue #21). A missing parent at or below the scope root —
// a genuinely dangling intermediate — is still reported.
func parentInScope(parentLoc, scopeRoot string) bool {
	return scopeRoot == "" || parentLoc == scopeRoot || strings.HasPrefix(parentLoc, scopeRoot+":")
}

// ---- corpus scans (one Nodes query + per-node detail reads) ----

// scanPrefixDetail reads every node under a loc prefix (headers + specs) with
// full detail for linting. The prefix is any citation prefix — a product, a
// module, a product-qualified module, or a deeper subtree (e.g. "cli", "msg",
// "cli:cha", or a feature like "cor:api:140"). The scan pages to exhaustion so
// a subtree larger than one server page is linted whole (#23).
func scanPrefixDetail(cmd *cobra.Command, client graphql.Client, memURN, prefix string) ([]specNode, error) {
	all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix, nil)
	if err != nil {
		return nil, err
	}
	var nodes []*api.ListNode
	for _, n := range all {
		if n == nil {
			continue
		}
		if n.Loc != prefix && !strings.HasPrefix(n.Loc, prefix+":") {
			continue // keep the scan scoped to the requested subtree
		}
		if _, err := ParseCitation(n.Loc); err == nil {
			nodes = append(nodes, n)
		}
	}
	return fetchDetails(cmd, client, nodes)
}

// scanAllSpecsDetail reads every citation-shaped node in the memory with full
// detail; non-citation nodes (e.g. the register) are skipped. The scan is not
// pre-filtered by tag because lintNode itself validates the required "spec"
// tag, including on malformed corpus members (#241). The scan pages to
// exhaustion so a corpus larger than one server page is linted whole (#23).
func scanAllSpecsDetail(cmd *cobra.Command, client graphql.Client, memURN string) ([]specNode, error) {
	all, err := scanAllNodes(cmd.Context(), client, &memURN, nil, nil)
	if err != nil {
		return nil, err
	}
	nodes := citationListNodes(all)
	return fetchDetails(cmd, client, nodes)
}

// scanAllCitationLocs reads every citation-shaped node in the memory. It is
// intentionally tag-agnostic so inventory views don't hide malformed specs
// before lint can report the missing tag.
func scanAllCitationLocs(cmd *cobra.Command, client graphql.Client, memURN string) ([]string, error) {
	all, err := scanAllNodes(cmd.Context(), client, &memURN, nil, nil)
	if err != nil {
		return nil, err
	}
	locs := make([]string, 0, len(all))
	for _, n := range all {
		if n == nil {
			continue
		}
		if _, err := ParseCitation(n.Loc); err == nil {
			locs = append(locs, n.Loc)
		}
	}
	return locs, nil
}

func citationListNodes(all []*api.ListNode) []*api.ListNode {
	nodes := make([]*api.ListNode, 0, len(all))
	for _, n := range all {
		if n == nil {
			continue
		}
		if _, err := ParseCitation(n.Loc); err == nil {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// discoverProducts returns the distinct product codes present in the memory's
// spec corpus, sorted. It pages to exhaustion and reads only the loc (no
// per-node detail), so it's cheap enough to run on the --module dead-end path
// to infer or disambiguate the product (#99 item 4).
func discoverProducts(cmd *cobra.Command, client graphql.Client, memURN string) ([]string, error) {
	locs, err := scanAllCitationLocs(cmd, client, memURN)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, loc := range locs {
		if c, perr := ParseCitation(loc); perr == nil && c.Product != "" {
			set[c.Product] = true
		}
	}
	return sortedStringKeys(set), nil
}

func fetchDetails(cmd *cobra.Command, client graphql.Client, list []*api.ListNode) ([]specNode, error) {
	nodes, unavailable, err := fetchDetailsWithUnavailable(cmd, client, list)
	if err != nil {
		return nil, err
	}
	if len(unavailable) > 0 {
		nodes = append(nodes, unavailableSpecNodes(list, unavailable)...)
	}
	return nodes, nil
}

func fetchDetailsWithUnavailable(cmd *cobra.Command, client graphql.Client, list []*api.ListNode) ([]specNode, []string, error) {
	ids := make([]string, 0, len(list))
	for _, n := range list {
		if n == nil {
			continue
		}
		ids = append(ids, n.Id)
	}
	batched, unavailable, err := collectSpecDetails(cmd, client, ids)
	if err != nil {
		return nil, nil, err
	}
	out := make([]specNode, 0, len(list))
	for _, bn := range batched {
		if bn == nil {
			continue
		}
		out = append(out, nodeFromGQL(nodeByIDFromBatch(bn)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	sort.Strings(unavailable)
	return out, unavailable, nil
}

func collectSpecDetails(cmd *cobra.Command, client graphql.Client, ids []string) ([]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode, []string, error) {
	return collectSpecDetailBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
		resp, ferr := gen.NodeBatch(cmd.Context(), client, chunk, nil, nil)
		if ferr != nil {
			return nil, api.MapError(ferr)
		}
		return resp.NodeBatch, nil
	})
}

func collectSpecDetailBatch(
	ids []string,
	fetch func([]string) (*gen.NodeBatchNodeBatchNodeBatchResult, error),
) ([]*gen.NodeBatchNodeBatchNodeBatchResultNodesNode, []string, error) {
	batched, unavailable, err := api.CollectNodeBatch(ids, fetch)
	if err != nil {
		return nil, nil, err
	}
	return batched, unavailable, nil
}

func unavailableSpecNodes(list []*api.ListNode, unavailable []string) []specNode {
	if len(unavailable) == 0 {
		return nil
	}
	locByID := map[string]string{}
	for _, n := range list {
		if n == nil {
			continue
		}
		locByID[n.Id] = n.Loc
	}
	out := make([]specNode, 0, len(unavailable))
	for _, id := range unavailable {
		loc := locByID[id]
		if loc == "" {
			loc = "unavailable:" + id
		}
		out = append(out, specNode{Loc: loc, Unavailable: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	return out
}

// serializationMarkers are field delimiters of the WRITING TOOL, never spec
// content (#545). Their presence means one field has absorbed another — and the
// absorbed one may be the only copy of it, which is what happened to
// cor:api:090:00: its provisions ended up inside its own abstract while its
// body stayed an unfilled scaffold. The obvious fix for the resulting
// abstract-length warning, truncating at the stray tag, would have destroyed
// them.
//
// Measured at zero occurrences across all 305 specs when this shipped. The
// corpus's legitimate angle brackets are grammar placeholders (<org>:<slug>,
// <actor>) and share no token with this list.
var serializationMarkers = []string{
	"</abstract>", "<abstract>",
	"</content>", "<content>",
	"<parameter name=", "</parameter>",
	"<function_calls>", "</function_calls>",
	"<invoke name=", "</invoke>",
}

// scaffoldFillers are sentences `spec new` emits and an author is expected to
// replace. Finding one means the body was never written.
//
// Matched on the FILLER PROSE rather than a sentinel, deliberately: a
// TODO(body:) marker would only cover specs created after it shipped, and the
// corpus this rule protects is the one that already exists. See scaffoldBody.
var scaffoldFillers = []string{
	"State the shared rules and defaults.",
	"The changes that repeal or supersede these general provisions. (Mandatory.)",
	"The specific changes that repeal or supersede this spec. (Mandatory.)",
	"One-line definition of what this spec governs.",
	"State the rule precisely. Give concrete examples and edge cases.",
}

// withoutCode removes the places a spec may legitimately QUOTE a marker: a
// fenced block, or an inline code span.
//
// This is the self-reference guard — a spec documenting this very leak would
// otherwise be reported as corrupted by the rule that documents it, the
// closing-keyword shape from `conventions` where quoting the thing re-triggers
// it.
//
// THE GOVERNING RULE, and it is what stopped three review rounds of chasing
// CommonMark: this function may cause a FALSE POSITIVE, and must never cause a
// FALSE NEGATIVE. serialization-leak is an error-severity check on content that
// may be the only copy of itself. A spurious finding is loud, cheap and fixed
// in a minute; a suppressed one hides the corruption the rule exists to catch,
// and nothing will ever say so.
//
// So every ambiguity resolves toward SCANNING rather than stripping:
//
//   - an UNCLOSED fence restores its lines as prose. We do not know where the
//     code ended, and assuming "everything after it" blinds the rule for the
//     rest of the document — measured, that swallowed a real leak whole.
//   - an indented code block is NOT stripped. A ≥4-space line merely cannot
//     OPEN a fence (CommonMark allows at most 3), which was the actual defect;
//     stripping such blocks as well would add a hiding place to fix a case the
//     corpus does not have.
//   - an unterminated backtick run stays literal text, so a stray backtick
//     cannot swallow the rest of a line.
//
// It is a scanner rather than a regexp because a closing delimiter must match
// the opening RUN LENGTH, and Go's RE2 has no backreferences.
func withoutCode(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, len(lines))
	open, openAt := "", -1
	for i, line := range lines {
		indent, trimmed := splitIndent(line)
		switch {
		case open != "":
			// A closing fence carries ONLY whitespace after its run
			// (CommonMark); ```not-a-close is fenced content, not a close.
			if run := fenceRun(trimmed); indent <= 3 && run != "" && run[0] == open[0] &&
				len(run) >= len(open) && strings.TrimSpace(trimmed[len(run):]) == "" {
				open = ""
			}
			kept[i] = ""
		case indent <= 3 && fenceOpener(trimmed) != "":
			open, openAt = fenceOpener(trimmed), i
			kept[i] = ""
		default:
			kept[i] = line
		}
	}
	if open != "" {
		// Never blind: restore from the opening fence onward and scan it.
		copy(kept[openAt:], lines[openAt:])
	}
	// Spans are stripped PER LINE, and that is a deliberate reversal.
	//
	// Round 2 of this review made them cross newlines, because CommonMark spans
	// may — a correct observation about Markdown, and the wrong call here. A
	// cross-line span pairs the delimiters of fence-looking prose several lines
	// apart and swallows everything between them, which is how a real marker
	// disappeared. It traded an ACCEPTABLE failure (a span crossing a newline is
	// reported, spuriously) for a FORBIDDEN one (a marker silently unseen).
	//
	// The property decides it: prefer the false positive. A marker quoted across
	// a line break is reported, and the remedy is to fence the example.
	for i, line := range kept {
		kept[i] = stripInlineCode(line)
	}
	return strings.Join(kept, "\n")
}

// splitIndent returns the line's indentation width (a tab counting as 4, per
// CommonMark) and the line with that indentation removed.
func splitIndent(line string) (int, string) {
	w := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			w++
		case '\t':
			w += 4
		default:
			return w, line[i:]
		}
	}
	return w, ""
}

// fenceRun returns the leading delimiter run when the line opens or closes a
// fence — three or more backticks or tildes — and "" otherwise.
func fenceRun(trimmed string) string {
	if trimmed == "" {
		return ""
	}
	c := trimmed[0]
	if c != '`' && c != '~' {
		return ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return ""
	}
	return trimmed[:n]
}

// fenceOpener returns the run when the line OPENS a fence, and "" otherwise.
//
// Distinct from fenceRun because opening is stricter than closing: a BACKTICK
// fence's info string may not contain a backtick (CommonMark), so such a line
// is not a fence at all. Accepting it opened a fence that the next ``` closed,
// stripping the prose between them — and a marker in that prose vanished
// (@codex, PR #547). A tilde fence has no such restriction.
func fenceOpener(trimmed string) string {
	run := fenceRun(trimmed)
	if run == "" {
		return ""
	}
	if run[0] == '`' && strings.Contains(trimmed[len(run):], "`") {
		return ""
	}
	return run
}

// stripInlineCode removes CommonMark code spans: a run of N backticks opens
// one, and the next run of EXACTLY N backticks closes it WITHIN THE LINE. An
// unterminated run is literal text and is kept, so a stray backtick cannot
// swallow the rest of a line.
func stripInlineCode(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '`' {
			b.WriteByte(text[i])
			i++
			continue
		}
		n := 0
		for i+n < len(text) && text[i+n] == '`' {
			n++
		}
		closed := false
		for j := i + n; j < len(text); {
			if text[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(text) && text[j+m] == '`' {
				m++
			}
			if m == n {
				b.WriteByte(' ')
				i = j + m
				closed = true
				break
			}
			j += m
		}
		if !closed {
			b.WriteString(text[i : i+n])
			i += n
		}
	}
	return b.String()
}

// leakedMarkers returns the serialization markers present in s outside code.
func leakedMarkers(s string) []string {
	prose := withoutCode(s)
	var found []string
	for _, m := range serializationMarkers {
		if strings.Contains(prose, m) {
			found = append(found, m)
		}
	}
	return found
}

// isScaffoldBody reports whether the body is still `spec new`'s output — the
// gap that let an unauthored spec look authored. The rubric's one body-reading
// check tests for a "what invalidates" heading, and the scaffold SHIPS with
// that heading, so a never-written body passes it.
func isScaffoldBody(content *string) bool {
	if content == nil {
		return false
	}
	// withoutCode for the same reason rule A uses it: a spec DOCUMENTING
	// `spec new` quotes its filler in an example, and matching that would call
	// an authored spec unauthored. Rule A had this guard from the start and
	// rule B did not — an inconsistency in my own implementation rather than a
	// new case (@codex, PR #547).
	prose := withoutCode(*content)
	for _, f := range scaffoldFillers {
		if strings.Contains(prose, f) {
			return true
		}
	}
	return false
}
