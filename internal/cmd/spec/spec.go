// Package spec implements `hadron spec ...` — an opinionated layer over
// the generic node/edge commands for maintaining product-spec nodes that
// follow the loc-as-citation convention: a spec's loc IS its citation
// number, <module>:<feature>:<rule>:<flow> (e.g. msg:010:02:03), and each
// colon level is a real parent/child node. The scheme, the frozen module
// codes, and the append-only number ledger are governed by a `register`
// node in the target memory. This package is general — it works on any
// memory following the convention, addressed by -m/--memory.
package spec

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdSpec returns the `hadron spec` command group.
func NewCmdSpec(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "spec <command>",
		Aliases: []string{"specs"},
		Short:   "Maintain product specs (loc-as-citation nodes)",
		Long: `Maintain product specs in a Hadron memory.

A spec's loc IS its citation number — a legal-code-style address
<module>:<feature>:<rule>:<flow> (e.g. msg:010:02:03) where each colon
level is a real parent/child node. A ` + "`register`" + ` node in the memory
holds the frozen 3-letter module codes and the append-only number ledger.

Specs follow a fixed rubric (a mandatory abstract + a "What invalidates
this spec" section) and numbers are never renumbered — to replace a spec
you supersede it. These commands encode that discipline on top of the
generic node/edge primitives. Every subcommand takes -m/--memory.`,
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdDescribe(f))
	cmd.AddCommand(newCmdRegister(f))
	cmd.AddCommand(newCmdFind(f))
	cmd.AddCommand(newCmdNew(f))
	cmd.AddCommand(newCmdLint(f))
	cmd.AddCommand(newCmdSupersede(f))
	cmd.AddCommand(newCmdImport(f))
	return cmd
}

// ---- stable --json DTOs (never genqlient structs; see output package) ----

// specDTO is the --json shape for a spec in list output.
type specDTO struct {
	Citation  string   `json:"citation"`
	MemoryID  string   `json:"memoryId"`
	Name      string   `json:"name"`
	NodeType  string   `json:"nodeType"`
	Tags      []string `json:"tags"`
	UpdatedAt string   `json:"updatedAt"`
}

// specEdgeDTO is one edge in `spec get` output. Loc/MemoryID name the
// other endpoint (cross-memory edges carry a different memoryId).
type specEdgeDTO struct {
	Direction string `json:"direction"` // "out" | "in"
	Label     string `json:"label"`
	Loc       string `json:"loc"`
	MemoryID  string `json:"memoryId"`
}

// specDetailDTO is the --json shape for `spec get`.
type specDetailDTO struct {
	Citation string   `json:"citation"`
	MemoryID string   `json:"memoryId"`
	Name     string   `json:"name"`
	NodeType string   `json:"nodeType"`
	Tags     []string `json:"tags"`
	Abstract *string  `json:"abstract"`
	// Content is omitempty by design: --abstract-only drops the body, so the
	// field's absence means "body not included" rather than "empty body".
	Content   *string          `json:"content,omitempty"`
	Edges     []specEdgeDTO    `json:"edges"`
	Lint      []lintFindingDTO `json:"lint"`
	UpdatedAt string           `json:"updatedAt"`
}

// lintFindingDTO is one lint result.
type lintFindingDTO struct {
	Citation string `json:"citation"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // error | warning | info
	Message  string `json:"message"`
}

// ledgerDTO is the --json shape for `spec register`.
type ledgerDTO struct {
	Memory  string            `json:"memory"`
	Modules []ledgerModuleDTO `json:"modules"`
	Drift   []string          `json:"drift,omitempty"`
}

type ledgerModuleDTO struct {
	Module      string             `json:"module"`
	NextFeature string             `json:"nextFeature"`
	Features    []ledgerFeatureDTO `json:"features"`
}

type ledgerFeatureDTO struct {
	Feature  string   `json:"feature"`
	Rules    []string `json:"rules"`
	NextRule string   `json:"nextRule"`
}

// ---- citation grammar ----

var (
	reModule  = regexp.MustCompile(`^[a-z]{3}$`)
	reFeature = regexp.MustCompile(`^[0-9]{3}$`)
	re2digit  = regexp.MustCompile(`^[0-9]{2}$`)
)

const (
	// productContractCode is the reserved module-position code holding a
	// product's general-provisions contract (<product>:gen), inherited by
	// every module in the product. Alpha so the grammar stays self-describing.
	productContractCode = "gen"
	// moduleContractFeature is the reserved feature-position number holding a
	// module's general-provisions contract (<module>:000), inherited by every
	// feature in the module. Naturally free — features allocate from 010 up.
	moduleContractFeature = "000"
)

// Citation is a parsed loc grammar. Flat (legacy):
// <module>[:<feature>[:<rule>[:<flow>]]] (e.g. msg:010:02). Product-rooted:
// <product>:<module>[:<feature>[:<rule>[:<flow>]]] (e.g. cli:cha:010:01).
type Citation struct {
	Product string // 3 lowercase letters, "" in a flat corpus
	Module  string // 3 lowercase letters, "" for a bare product root
	Feature string // 3 digits, "" if absent
	Rule    string // 2 digits, "" if absent
	Flow    string // 2 digits, "" if absent
}

// ParseCitation parses and validates a citation, returning a Usage error for
// any malformed segment. The rooting is inferred from segment 2's character
// class: a second alpha code ⇒ product-rooted (<product>:<module>:…); a 3-digit
// feature ⇒ flat (<module>:…). A lone alpha code parses as a flat module —
// callers that need a bare product root build the Citation directly.
func ParseCitation(s string) (Citation, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Citation{}, exitcode.Newf(exitcode.Usage, "empty citation")
	}
	parts := strings.Split(s, ":")
	if !reModule.MatchString(parts[0]) {
		return Citation{}, exitcode.Newf(exitcode.Usage, "%q must be 3 lowercase letters", parts[0])
	}
	var c Citation
	rest := parts // module-rooted segments: [module, feature?, rule?, flow?]
	if len(parts) > 1 && reModule.MatchString(parts[1]) {
		c.Product = parts[0] // product-rooted: parts[1] is the module
		rest = parts[1:]
	}
	if len(rest) > 4 {
		return Citation{}, exitcode.Newf(exitcode.Usage,
			"citation %q has too many segments — want [<product>:]<module>[:<feature>[:<rule>[:<flow>]]]", s)
	}
	c.Module = rest[0] // alpha, already validated
	if len(rest) > 1 {
		if c.Feature = rest[1]; !reFeature.MatchString(c.Feature) {
			return Citation{}, exitcode.Newf(exitcode.Usage, "feature %q must be 3 digits", rest[1])
		}
	}
	if len(rest) > 2 {
		if c.Rule = rest[2]; !re2digit.MatchString(c.Rule) {
			return Citation{}, exitcode.Newf(exitcode.Usage, "rule %q must be 2 digits", rest[2])
		}
	}
	if len(rest) > 3 {
		if c.Flow = rest[3]; !re2digit.MatchString(c.Flow) {
			return Citation{}, exitcode.Newf(exitcode.Usage, "flow %q must be 2 digits", rest[3])
		}
	}
	return c, nil
}

// Level is 0 (bare product root) .. 4 (flow); module is 1, feature 2, rule 3.
// The product field does not shift levels — a product's module root is still
// level 1 — so existing level-keyed logic is unchanged.
func (c Citation) Level() int {
	switch {
	case c.Flow != "":
		return 4
	case c.Rule != "":
		return 3
	case c.Feature != "":
		return 2
	case c.Module != "":
		return 1
	default:
		return 0 // bare product root
	}
}

// Format re-emits the colon form, including the product when present.
func (c Citation) Format() string {
	var parts []string
	if c.Product != "" {
		parts = append(parts, c.Product)
	}
	if c.Module != "" {
		parts = append(parts, c.Module)
	}
	for _, s := range []string{c.Feature, c.Rule, c.Flow} {
		if s == "" {
			break
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ":")
}

func (c Citation) String() string { return c.Format() }

// Parent returns the citation one level up: flow→rule→feature→module, and a
// product-rooted module→its product root. A flat module root and a bare
// product root have no parent.
func (c Citation) Parent() (Citation, bool) {
	p := c
	switch c.Level() {
	case 4:
		p.Flow = ""
	case 3:
		p.Rule = ""
	case 2:
		p.Feature = ""
	case 1:
		if c.Product == "" {
			return Citation{}, false // flat module root
		}
		p.Module = "" // module root → product root
	default:
		return Citation{}, false // bare product root
	}
	return p, true
}

// IsContract reports whether this citation is a reserved general-provisions
// contract — a feature's rule `00`, a module's feature `000`, or a product's
// module `gen`. Its siblings at the same tier inherit it.
func (c Citation) IsContract() bool {
	switch c.Level() {
	case 3:
		return c.Rule == "00"
	case 2:
		return c.Feature == moduleContractFeature
	case 1:
		return c.Product != "" && c.Module == productContractCode
	default:
		return false
	}
}

// InheritedContractLoc returns the general-provisions contract this citation
// inherits — the reserved "zero" sibling at its own tier: a rule inherits its
// feature's :00, a feature inherits its module's :000, and a product-rooted
// module inherits its product's :gen. ok is false for a flow, a flat module
// root, a bare product root, or a contract itself (contracts are inheritance
// sources, not sinks).
func (c Citation) InheritedContractLoc() (Citation, bool) {
	if c.IsContract() {
		return Citation{}, false
	}
	ic := c
	switch c.Level() {
	case 3:
		ic.Rule = "00"
		return ic, true
	case 2:
		ic.Feature = moduleContractFeature
		return ic, true
	case 1:
		if c.Product == "" {
			return Citation{}, false
		}
		return Citation{Product: c.Product, Module: productContractCode}, true
	default:
		return Citation{}, false
	}
}

// ---- memory / node-reference helpers ----

// memoryURNFromFlag normalizes the -m/--memory value to the canonical
// fully-qualified <org>::<memory> form (stripping an hrn:/urn: memory
// scheme prefix). The server requires this fully-qualified form.
func memoryURNFromFlag(m string) (string, error) {
	m = strings.TrimSpace(m)
	if m == "" {
		return "", exitcode.Newf(exitcode.Usage, "a memory is required: pass -m/--memory <org::memory>")
	}
	for _, p := range []string{"hrn:memory:", "urn:memory:"} {
		if strings.HasPrefix(m, p) {
			m = strings.TrimPrefix(m, p)
			break
		}
	}
	return m, nil
}

// specNodeRef builds a fully-qualified node reference (<org>::<memory>::<loc>)
// that cmdutil.ResolveNodeURN accepts.
func specNodeRef(memoryURN, loc string) string {
	return memoryURN + "::" + loc
}

// resolveSpecNode turns a citation/loc in the given memory into a node ID.
func resolveSpecNode(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (string, error) {
	return cmdutil.ResolveNodeURN(cmd, client, specNodeRef(memoryURN, loc))
}

// fetchSpecNode resolves a citation/loc and reads the full node.
func fetchSpecNode(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (*gen.GetNodeByIdNodeByIdNode, error) {
	id, err := resolveSpecNode(cmd, client, memoryURN, loc)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetNodeById(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.NodeById == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "spec %q not found", loc)
	}
	return resp.NodeById, nil
}

// fetchRegister reads the memory's `register` node (advisory; not a spec).
func fetchRegister(cmd *cobra.Command, client graphql.Client, memoryURN string) (*gen.GetNodeByIdNodeByIdNode, error) {
	return fetchSpecNode(cmd, client, memoryURN, "register")
}

// ---- exhaustive node scan (issue #23) ----

// nodesPageSize bounds one page of the paginated nodes scan. The server caps
// an unspecified limit at its default page (100) and silently drops the rest
// (issue #23), so every "whole memory/scope" command must page explicitly to
// exhaustion. 500 matches the server's largest built-in default and keeps a
// typical spec corpus to a single round-trip; the server materializes the full
// result set before slicing regardless of limit, so a larger page is cheap.
const nodesPageSize = 500

// scanAllNodes pages the nodes query to exhaustion and returns every node
// matching (memory, prefix, tags). Any command whose contract is "the whole
// memory/scope" — spec lint --all and prefix-scoped lint, register, describe,
// bare spec ls, and the new/supersede allocation scans — must use this rather
// than a single unbounded gen.Nodes call, which the server truncates to one
// default page (issue #23). For an allocation scan that truncation is not just
// under-reporting: a missed tail makes the allocator reuse a live number.
// Spec nodes are addressed by tag/prefix, never nodeType, so nodeType is left
// unset; add it back here if a caller ever needs it.
func scanAllNodes(ctx context.Context, client graphql.Client, memory, prefix *string, tags []string) ([]*gen.NodesNodesNode, error) {
	return paginateNodes(func(limit, offset int) ([]*gen.NodesNodesNode, error) {
		l, o := limit, offset
		resp, err := gen.Nodes(ctx, client, memory, prefix, nil, tags, nil, &l, &o)
		if err != nil {
			return nil, api.MapError(err)
		}
		return resp.Nodes, nil
	})
}

// paginateNodes drives the offset loop independently of the GraphQL layer so
// the termination logic is unit-testable. It requests fixed-size pages until a
// short (or empty) page signals the tail — a full page means there may be more,
// so it never stops early on a default-capped response. fetch is called with
// (limit, offset).
func paginateNodes(fetch func(limit, offset int) ([]*gen.NodesNodesNode, error)) ([]*gen.NodesNodesNode, error) {
	var all []*gen.NodesNodesNode
	for offset := 0; ; offset += nodesPageSize {
		page, err := fetch(nodesPageSize, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < nodesPageSize {
			return all, nil
		}
	}
}

// ---- the in-memory model the lint engine and renderers operate on ----

type specEdge struct {
	Label string
	Loc   string // the target's loc
}

// specNode is a lint/render-friendly projection of a node, decoupled from
// the genqlient types so the rule engine is trivially unit-testable.
type specNode struct {
	Loc                string
	Name               string
	NodeType           string
	Tags               []string
	Abstract           *string
	AbstractOriginHash *string
	Content            *string
	DataVersion        string // data.version, "" if absent/unparseable
	OutEdges           []specEdge
}

func nodeFromGQL(n *gen.GetNodeByIdNodeByIdNode) specNode {
	sn := specNode{
		Loc:                n.Loc,
		Name:               n.Name,
		NodeType:           n.NodeType,
		Tags:               n.Tags,
		Abstract:           n.Abstract,
		AbstractOriginHash: n.AbstractOriginHash,
		Content:            n.Content,
	}
	if n.Data != nil {
		var d struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(*n.Data, &d); err == nil {
			sn.DataVersion = d.Version
		}
	}
	for _, e := range n.OutgoingEdges {
		if e == nil || e.Target == nil {
			continue
		}
		sn.OutEdges = append(sn.OutEdges, specEdge{Label: e.Label, Loc: e.Target.Loc})
	}
	return sn
}

// ---- register access (advisory; the tool never writes the register) ----

// registerLedger is the best-effort parse of a `register` node body: the
// module-code table and any `retired:` annotations. It is used only for
// the --new-module guard, the retired-number overlay, and drift display —
// never as the source of truth for allocation (live nodes are).
type registerLedger struct {
	modules map[string]bool  // 3-letter codes named in the body
	retired map[string][]int // parent-prefix loc (e.g. "msg:010" or "msg") -> retired child numbers
}

var (
	reTableRow     = regexp.MustCompile("^\\|\\s*`?([a-z]{3})`?\\s*\\|")
	reModuleHead   = regexp.MustCompile("^#{2,4}\\s+`?([a-z]{3})`?")
	reFeatureItem  = regexp.MustCompile(`^\s*[-*]\s+\*\*([0-9]{3})\*\*`)
	reRetired      = regexp.MustCompile(`(?i)retired:\s*(.+)$`)
	reLedgerNumber = regexp.MustCompile(`[0-9]{2,3}`)
)

// parseLedger extracts the module-code set and retired-number overlay from
// a register node body. Tolerant by design: unknown formatting is ignored.
func parseLedger(content string) registerLedger {
	l := registerLedger{modules: map[string]bool{}, retired: map[string][]int{}}
	var curModule, curFeature string
	for _, line := range strings.Split(content, "\n") {
		if m := reTableRow.FindStringSubmatch(line); m != nil {
			l.modules[m[1]] = true
		}
		if m := reModuleHead.FindStringSubmatch(line); m != nil {
			curModule, curFeature = m[1], ""
			l.modules[curModule] = true
		}
		if m := reFeatureItem.FindStringSubmatch(line); m != nil {
			curFeature = m[1]
		}
		if m := reRetired.FindStringSubmatch(line); m != nil {
			rest := strings.TrimSpace(m[1])
			if rest == "" || strings.HasPrefix(strings.ToLower(rest), "none") {
				continue
			}
			key := curModule
			if curFeature != "" {
				key = curModule + ":" + curFeature
			}
			if key == "" {
				continue
			}
			for _, ns := range reLedgerNumber.FindAllString(rest, -1) {
				if n, err := strconv.Atoi(ns); err == nil {
					l.retired[key] = append(l.retired[key], n)
				}
			}
		}
	}
	return l
}
