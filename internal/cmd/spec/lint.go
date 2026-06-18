package spec

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

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

var (
	rePLevel = regexp.MustCompile(`^p[0-3]$`)
	// Matches the "what invalidates" statement whether it's a heading
	// (## What invalidates …) or inline bold (**What invalidates:** …),
	// both of which the platform-specs corpus uses.
	reInvalidates = regexp.MustCompile(`(?im)^\s*[#*]*\s*what invalidates`)
)

func newCmdLint(f *cmdutil.Factory) *cobra.Command {
	var memory, product, module string
	var all, strict bool
	cmd := &cobra.Command{
		Use:     "lint [<citation>]",
		Aliases: []string{"check", "validate"},
		Short:   "Validate specs against the rubric and stability rules",
		Long: `Validate one spec, a product, a module, or the whole corpus
against the loc-as-citation rubric and stability rules.

Scope is one of: a single <citation> argument, --product <ppp>, --module
<mmm> (optionally within --product), or --all. Errors (rubric/stability
violations) exit with code 5; --strict promotes warnings to errors too.`,
		Example: `  hadron spec lint msg:010:02 -m micromentor.org::platform-specs
  hadron spec lint --module msg -m micromentor.org::platform-specs
  hadron spec lint --product cli -m hadronmemory.com::platform-specs
  hadron spec lint --all -m micromentor.org::platform-specs --strict`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			if len(args) == 1 && (product != "" || module != "" || all) {
				return exitcode.Newf(exitcode.Usage, "a <citation> argument cannot be combined with --product/--module/--all")
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
				if len(nodes) == 0 {
					hint := ""
					if product == "" {
						hint = " — in a product-rooted memory, scope with --product <ppp> [--module <mmm>]"
					}
					return exitcode.Newf(exitcode.NotFound, "no specs found under %q%s", prefix, hint)
				}
				corpus = true
			case all:
				nodes, err = scanAllSpecsDetail(cmd, client, memURN)
				if err != nil {
					return err
				}
				corpus = true
			default:
				return exitcode.Newf(exitcode.Usage, "specify a <citation>, --product <ppp>, --module <mmm>, or --all")
			}

			var findings []lintFindingDTO
			if corpus {
				findings = lintCorpus(nodes, scopeRoot)
			} else {
				for _, n := range nodes {
					findings = append(findings, lintNode(n)...)
				}
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&product, "product", "", "lint every spec under this product")
	cmd.Flags().StringVar(&module, "module", "", "lint every spec under this module (optionally within --product)")
	cmd.Flags().BoolVar(&all, "all", false, "lint every spec in the memory")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// lintNode runs the per-node rules and returns findings tagged with the
// node's citation. Header nodes (module/feature, level < 3) only get the
// universal checks; the rubric proper applies to rules and flows.
func lintNode(n specNode) []lintFindingDTO {
	var fs []lintFindingDTO
	add := func(rule, sev, msg string) {
		fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: rule, Severity: sev, Message: msg})
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

	if err != nil || c.Level() < 3 {
		return fs
	}

	// Rubric proper. Top-level specs (rules) are the compliance-loadable
	// retrieval surface, so a missing abstract or invalidation is an error;
	// flows are pulled on demand, so the same gaps are advisory there.
	rubricSev := sevError
	if c.Level() == 4 {
		rubricSev = sevWarning
	}
	if !hasTag(n.Tags, "spec") {
		add("tag-spec", sevError, `missing "spec" tag`)
	}
	if !abstractPresent(n.Abstract) {
		add("abstract", rubricSev, "missing abstract — the vector-search retrieval surface (or still a placeholder)")
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
func lintCorpus(nodes []specNode, scopeRoot string) []lintFindingDTO {
	var fs []lintFindingDTO
	locCount := map[string]int{}
	contracts := map[string]bool{}
	productCodes := map[string]bool{}
	flatCodes := map[string]bool{}
	for _, n := range nodes {
		fs = append(fs, lintNode(n)...)
		locCount[n.Loc]++
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
				fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "inheritance-edge", Severity: sevWarning, Message: "no inheritance edge to general-provisions contract " + cl.Format()})
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
// full detail for linting. The prefix is a product, a module, or a
// product-qualified module (e.g. "cli", "msg", or "cli:cha"). The scan pages
// to exhaustion so a subtree larger than one server page is linted whole (#23).
func scanPrefixDetail(cmd *cobra.Command, client graphql.Client, memURN, prefix string) ([]specNode, error) {
	all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix, nil)
	if err != nil {
		return nil, err
	}
	var nodes []*gen.NodesNodesNode
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

// scanAllSpecsDetail reads every spec-tagged node in the memory with full
// detail; non-citation nodes (e.g. the register) are skipped. The scan pages
// to exhaustion so a corpus larger than one server page is linted whole (#23).
func scanAllSpecsDetail(cmd *cobra.Command, client graphql.Client, memURN string) ([]specNode, error) {
	all, err := scanAllNodes(cmd.Context(), client, &memURN, nil, []string{"spec"})
	if err != nil {
		return nil, err
	}
	var nodes []*gen.NodesNodesNode
	for _, n := range all {
		if n == nil {
			continue
		}
		if _, err := ParseCitation(n.Loc); err == nil {
			nodes = append(nodes, n)
		}
	}
	return fetchDetails(cmd, client, nodes)
}

func fetchDetails(cmd *cobra.Command, client graphql.Client, list []*gen.NodesNodesNode) ([]specNode, error) {
	out := make([]specNode, 0, len(list))
	for _, n := range list {
		if n == nil {
			continue
		}
		resp, err := gen.GetNodeById(cmd.Context(), client, n.Id)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.NodeById != nil {
			out = append(out, nodeFromGQL(resp.NodeById))
		}
	}
	return out, nil
}
