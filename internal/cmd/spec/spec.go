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
	Citation  string           `json:"citation"`
	MemoryID  string           `json:"memoryId"`
	Name      string           `json:"name"`
	NodeType  string           `json:"nodeType"`
	Tags      []string         `json:"tags"`
	Abstract  *string          `json:"abstract"`
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

// Citation is a parsed loc grammar <module>[:<feature>[:<rule>[:<flow>]]].
type Citation struct {
	Module  string // 3 lowercase letters
	Feature string // 3 digits, "" if absent
	Rule    string // 2 digits, "" if absent
	Flow    string // 2 digits, "" if absent
}

// ParseCitation parses and validates a citation, returning a Usage error
// for any malformed segment.
func ParseCitation(s string) (Citation, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Citation{}, exitcode.Newf(exitcode.Usage, "empty citation")
	}
	parts := strings.Split(s, ":")
	if len(parts) > 4 {
		return Citation{}, exitcode.Newf(exitcode.Usage,
			"citation %q has too many segments — want <module>[:<feature>[:<rule>[:<flow>]]]", s)
	}
	var c Citation
	c.Module = parts[0]
	if !reModule.MatchString(c.Module) {
		return Citation{}, exitcode.Newf(exitcode.Usage, "module %q must be 3 lowercase letters", parts[0])
	}
	if len(parts) > 1 {
		if c.Feature = parts[1]; !reFeature.MatchString(c.Feature) {
			return Citation{}, exitcode.Newf(exitcode.Usage, "feature %q must be 3 digits", parts[1])
		}
	}
	if len(parts) > 2 {
		if c.Rule = parts[2]; !re2digit.MatchString(c.Rule) {
			return Citation{}, exitcode.Newf(exitcode.Usage, "rule %q must be 2 digits", parts[2])
		}
	}
	if len(parts) > 3 {
		if c.Flow = parts[3]; !re2digit.MatchString(c.Flow) {
			return Citation{}, exitcode.Newf(exitcode.Usage, "flow %q must be 2 digits", parts[3])
		}
	}
	return c, nil
}

// Level is 1 (module) .. 4 (flow).
func (c Citation) Level() int {
	switch {
	case c.Flow != "":
		return 4
	case c.Rule != "":
		return 3
	case c.Feature != "":
		return 2
	default:
		return 1
	}
}

// Format re-emits the colon form.
func (c Citation) Format() string {
	parts := []string{c.Module}
	for _, s := range []string{c.Feature, c.Rule, c.Flow} {
		if s == "" {
			break
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ":")
}

func (c Citation) String() string { return c.Format() }

// Parent returns the citation one level up, or false at module level.
func (c Citation) Parent() (Citation, bool) {
	p := c
	switch c.Level() {
	case 4:
		p.Flow = ""
	case 3:
		p.Rule = ""
	case 2:
		p.Feature = ""
	default:
		return Citation{}, false
	}
	return p, true
}

// IsContract reports whether this is a feature's `:00` general-provisions
// node (the shared contract its sibling rules inherit).
func (c Citation) IsContract() bool { return c.Level() == 3 && c.Rule == "00" }

// ContractLoc returns the `:00` general-provisions citation for this
// citation's feature; ok is false when there is no feature.
func (c Citation) ContractLoc() (Citation, bool) {
	if c.Feature == "" {
		return Citation{}, false
	}
	return Citation{Module: c.Module, Feature: c.Feature, Rule: "00"}, true
}

// defaultPLevel is the read-priority level a freshly scaffolded spec gets
// from its citation level: module roots p0, features/rules/contracts p1,
// flows p2.
func defaultPLevel(c Citation) int {
	switch c.Level() {
	case 1:
		return 0
	case 4:
		return 2
	default:
		return 1
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
