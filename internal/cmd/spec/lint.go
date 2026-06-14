package spec

import (
	"fmt"
	"io"
	"regexp"
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
	var memory, module string
	var all, strict bool
	cmd := &cobra.Command{
		Use:     "lint [<citation>]",
		Aliases: []string{"check", "validate"},
		Short:   "Validate specs against the rubric and stability rules",
		Long: `Validate one spec, a module, or the whole corpus against the
loc-as-citation rubric and stability rules.

Scope is one of: a single <citation> argument, --module <mmm>, or --all.
Errors (rubric/stability violations) exit with code 5; --strict promotes
warnings to errors too.`,
		Example: `  hadron spec lint msg:010:02 -m micromentor.org::platform-specs
  hadron spec lint --module msg -m micromentor.org::platform-specs
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

			var nodes []specNode
			var corpus bool
			switch {
			case len(args) == 1:
				n, err := fetchSpecNode(cmd, client, memURN, args[0])
				if err != nil {
					return err
				}
				nodes = []specNode{nodeFromGQL(n)}
			case module != "":
				if _, err := ParseCitation(module); err != nil {
					return err
				}
				nodes, err = scanModuleDetail(cmd, client, memURN, module)
				if err != nil {
					return err
				}
				corpus = true
			case all:
				nodes, err = scanAllSpecsDetail(cmd, client, memURN)
				if err != nil {
					return err
				}
				corpus = true
			default:
				return exitcode.Newf(exitcode.Usage, "specify a <citation>, --module <mmm>, or --all")
			}

			var findings []lintFindingDTO
			if corpus {
				findings = lintCorpus(nodes)
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
	cmd.Flags().StringVar(&module, "module", "", "lint every spec under this module")
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
	if countMatching(n.Tags, rePLevel) != 1 {
		add("one-plevel", sevError, "must carry exactly one read-priority tag (p0..p3)")
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
// checks (collisions, parent existence, inheritance edges).
func lintCorpus(nodes []specNode) []lintFindingDTO {
	var fs []lintFindingDTO
	locCount := map[string]int{}
	contracts := map[string]bool{}
	for _, n := range nodes {
		fs = append(fs, lintNode(n)...)
		locCount[n.Loc]++
		if c, err := ParseCitation(n.Loc); err == nil && c.IsContract() {
			contracts[c.Format()] = true
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
		if p, ok := c.Parent(); ok && locCount[p.Format()] == 0 {
			fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "parent-exists", Severity: sevError, Message: "parent " + p.Format() + " does not exist"})
		}
		if c.Level() == 3 && !c.IsContract() {
			if cl, ok := c.ContractLoc(); ok && contracts[cl.Format()] && !hasOutEdgeTo(n, cl.Format()) {
				fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "inheritance-edge", Severity: sevWarning, Message: "no inheritance edge to general-provisions contract " + cl.Format()})
			}
		}
	}
	return fs
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

func countMatching(tags []string, re *regexp.Regexp) int {
	n := 0
	for _, t := range tags {
		if re.MatchString(t) {
			n++
		}
	}
	return n
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

// ---- corpus scans (one Nodes query + per-node detail reads) ----

// scanModuleDetail reads every node under a module (headers + specs) with
// full detail for linting.
func scanModuleDetail(cmd *cobra.Command, client graphql.Client, memURN, module string) ([]specNode, error) {
	prefix := module
	resp, err := gen.Nodes(cmd.Context(), client, &memURN, &prefix, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, api.MapError(err)
	}
	var nodes []*gen.NodesNodesNode
	for _, n := range resp.Nodes {
		if c, err := ParseCitation(n.Loc); err == nil && c.Module == module {
			nodes = append(nodes, n)
		}
	}
	return fetchDetails(cmd, client, nodes)
}

// scanAllSpecsDetail reads every spec-tagged node in the memory with full
// detail; non-citation nodes (e.g. the register) are skipped.
func scanAllSpecsDetail(cmd *cobra.Command, client graphql.Client, memURN string) ([]specNode, error) {
	resp, err := gen.Nodes(cmd.Context(), client, &memURN, nil, nil, []string{"spec"}, nil, nil, nil)
	if err != nil {
		return nil, api.MapError(err)
	}
	var nodes []*gen.NodesNodesNode
	for _, n := range resp.Nodes {
		if _, err := ParseCitation(n.Loc); err == nil {
			nodes = append(nodes, n)
		}
	}
	return fetchDetails(cmd, client, nodes)
}

func fetchDetails(cmd *cobra.Command, client graphql.Client, list []*gen.NodesNodesNode) ([]specNode, error) {
	out := make([]specNode, 0, len(list))
	for _, n := range list {
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
