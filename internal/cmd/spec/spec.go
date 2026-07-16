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
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
	cmd.AddCommand(newCmdGrep(f))
	cmd.AddCommand(newCmdReplace(f))
	cmd.AddCommand(newCmdNew(f))
	cmd.AddCommand(newCmdEdit(f))
	cmd.AddCommand(newCmdExtract(f))
	cmd.AddCommand(newCmdLink(f))
	cmd.AddCommand(newCmdLint(f))
	cmd.AddCommand(newCmdCheckTools(f))
	cmd.AddCommand(newCmdSupersede(f))
	cmd.AddCommand(newCmdImport(f))
	cmd.AddCommand(newCmdUse(f))
	return cmd
}

// withFlagAliases makes a command accept extra spellings of its flags via the
// flagset normalizer: an alias passed on the command line resolves to its
// canonical flag. The aliases map is alias→canonical. This reconciles small
// vocabulary drifts across spec subcommands (e.g. body vs content) without
// adding a second help-listed flag or renaming the documented --json/flag
// contract (issue #99 item 5). Any existing normalizer (e.g. an inherited
// underscore→dash mapping) is chained first, so the alias lookup composes with
// it rather than clobbering it.
func withFlagAliases(cmd *cobra.Command, aliases map[string]string) {
	prev := cmd.Flags().GetNormalizeFunc()
	cmd.Flags().SetNormalizeFunc(func(fs *pflag.FlagSet, name string) pflag.NormalizedName {
		if prev != nil {
			name = string(prev(fs, name))
		}
		if canon, ok := aliases[name]; ok {
			name = canon
		}
		return pflag.NormalizedName(name)
	})
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
	Name      string `json:"name"`
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
	Data      *json.RawMessage `json:"data,omitempty"`
	Edges     []specEdgeDTO    `json:"edges"`
	Lint      []lintFindingDTO `json:"lint"`
	UpdatedAt string           `json:"updatedAt"`
}

// specBodyDTO is the --json shape for `spec get --body-only`: just the
// citation and the raw markdown body, for a clean
// `… | hadron node update --content -` round-trip.
type specBodyDTO struct {
	Citation string `json:"citation"`
	Content  string `json:"content"`
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
	// rePLevel matches a legacy read-priority tag (p0..p3); used by
	// supersede's semanticTags to drop it when carrying tags over from a
	// pre-migration node.
	rePLevel = regexp.MustCompile(`^p[0-3]$`)
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

// Seq derives the sibling sort order from the citation's deepest numeric
// segment (feature/rule/flow), so a spec node sorts among its siblings by
// citation and the portal can offer a "Next" (#40). ok is false for a module or
// bare product root, whose leaf is alphabetic and carries no numeric order.
func (c Citation) Seq() (int, bool) {
	var leaf string
	switch {
	case c.Flow != "":
		leaf = c.Flow
	case c.Rule != "":
		leaf = c.Rule
	case c.Feature != "":
		leaf = c.Feature
	default:
		return 0, false
	}
	n, err := strconv.Atoi(leaf)
	if err != nil {
		return 0, false
	}
	return n, true
}

// specSeq returns the sibling sort order for a spec citation as a *int ready for
// CreateNodeInput.Seq, or nil when the citation has no numeric leaf.
func specSeq(c Citation) *int {
	if n, ok := c.Seq(); ok {
		return &n
	}
	return nil
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

// Leaf returns the deepest non-empty segment of the citation (the flow, rule,
// feature, module, or product). Used as a placeholder title for an ancestor
// node scaffolded by --new-path, until the author renames it.
func (c Citation) Leaf() string {
	switch {
	case c.Flow != "":
		return c.Flow
	case c.Rule != "":
		return c.Rule
	case c.Feature != "":
		return c.Feature
	case c.Module != "":
		return c.Module
	default:
		return c.Product
	}
}

// ChildContract returns the reserved general-provisions contract one tier
// below this root — the "zero" sibling its children inherit: a product root's
// modules inherit <product>:gen, a module root's features inherit <module>:000,
// and a feature root's rules inherit <feature>:00. ok is false for a rule, a
// flow, or a citation that is itself a contract (those have no root children to
// provision). It is the dual of InheritedContractLoc, walked downward.
func (c Citation) ChildContract() (Citation, bool) {
	if c.IsContract() {
		return Citation{}, false
	}
	switch c.Level() {
	case 0:
		if c.Product == "" {
			return Citation{}, false
		}
		return Citation{Product: c.Product, Module: productContractCode}, true
	case 1:
		return Citation{Product: c.Product, Module: c.Module, Feature: moduleContractFeature}, true
	case 2:
		return Citation{Product: c.Product, Module: c.Module, Feature: c.Feature, Rule: "00"}, true
	default:
		return Citation{}, false
	}
}

// ---- memory / node-reference helpers ----

// stripMemoryScheme trims an hrn:/urn: memory scheme prefix; the rest is
// untouched.
func stripMemoryScheme(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"hrn:memory:", "urn:memory:"} {
		if strings.HasPrefix(s, p) {
			return strings.TrimPrefix(s, p)
		}
	}
	return s
}

// canonicalMemoryURN returns the canonical fully-qualified <org>::<memory>
// form: scheme stripped and the org/memory separator normalized to "::"
// (the memory list may report a memory's own urn with a single colon). A bare PK
// (no colon) is returned unchanged.
func canonicalMemoryURN(s string) string {
	return stripMemoryScheme(cmdutil.CanonicalMemoryRef(s))
}

// memoryURNFromFlag normalizes the -m/--memory value to the canonical
// fully-qualified <org>::<memory> form. It does NOT resolve a PK or a memory
// name to its org::memory — use resolveSpecMemoryURN for that. The server
// requires the fully-qualified form for node-ref FQNs and the nodes filter.
func memoryURNFromFlag(m string) (string, error) {
	if strings.TrimSpace(m) == "" {
		return "", exitcode.Newf(exitcode.Usage, "a memory is required: pass -m/--memory <org::memory>")
	}
	canon := cmdutil.CanonicalMemoryRef(m)
	if !isFullyQualifiedMemoryURN(canon) {
		return "", exitcode.Newf(exitcode.Usage, "invalid memory ref %q: use <org::memory>, <org:memory>, or hrn:memory:<org::memory>", m)
	}
	return stripMemoryScheme(canon), nil
}

func isFullyQualifiedMemoryURN(ref string) bool {
	norm := stripMemoryScheme(ref)
	org, memory, ok := strings.Cut(norm, "::")
	if !ok || org == "" || memory == "" {
		return false
	}
	if strings.Contains(org, ":") || strings.HasPrefix(memory, ":") || strings.Contains(memory, "::") {
		return false
	}
	return true
}

// resolveSpecMemoryURN resolves the -m/--memory value to the canonical
// <org>::<memory> form that node-ref FQNs and the nodes filter require. A ref
// already in <org>::<memory> (or an hrn:/urn: URN) shape is normalized without
// a round-trip; a PK or a memory name is resolved via the memories list. This is the
// one resolver every spec subcommand shares, so a PK no longer leaks into edge
// targets as "<pk>::<loc>" (issue #91).
func resolveSpecMemoryURN(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	canon := cmdutil.CanonicalMemoryRef(ref)
	norm := stripMemoryScheme(canon)
	if norm == "" {
		return "", exitcode.Newf(exitcode.Usage, "a memory is required: pass -m/--memory <org::memory>")
	}
	if strings.Contains(norm, ":") {
		if !isFullyQualifiedMemoryURN(canon) {
			return "", exitcode.Newf(exitcode.Usage, "invalid memory ref %q: use <org::memory>, <org:memory>, or hrn:memory:<org::memory>", ref)
		}
		return norm, nil // already fully-qualified — no lookup needed
	}
	_, memURN, err := lookupSpecMemory(cmd, client, ref)
	return memURN, err
}

// resolveSpecMemoryID resolves the -m/--memory value to the memory's PK id and
// its canonical <org>::<memory> urn. Query.memory / updateMemory take a PK, so
// this always consults the memories list. Accepts a PK, an hrn:/urn: URN, a bare
// <org>::<memory>, or a memory name (issue #91 — describe previously accepted
// none of these).
func resolveSpecMemoryID(cmd *cobra.Command, client graphql.Client, ref string) (id, memURN string, err error) {
	return lookupSpecMemory(cmd, client, ref)
}

// specMemoryDefault resolves the memory ref for a spec command from, in order:
// the -m/--memory flag, the spec-specific default (HADRON_SPEC_MEMORY env or the
// spec_memory config key, set via `hadron spec use`), then the global active
// memory (`hadron memory set-active`). It returns the ref and a human note
// naming the source when a default was used (empty note for the flag or when
// nothing is configured). The spec corpus is usually a fixed memory distinct
// from the global active memory, hence its own default tier.
func specMemoryDefault(f *cmdutil.Factory, flagVal string) (ref, note string, err error) {
	if strings.TrimSpace(flagVal) != "" {
		return flagVal, "", nil
	}
	cfg, cerr := f.Config()
	if cerr != nil {
		return "", "", cerr
	}
	if m := cfg.SpecMemory(); m != "" {
		return m, "note: using spec memory " + m + " (hadron spec use / HADRON_SPEC_MEMORY)", nil
	}
	if m := cfg.Memory(); m != "" {
		return m, "note: using active memory " + m + " (hadron memory set-active)", nil
	}
	return "", "", nil
}

// effectiveSpecMemory resolves the memory ref for a spec command that REQUIRES
// one, printing a note to stderr when it falls back to a default and returning a
// usage error when nothing is configured.
func effectiveSpecMemory(f *cmdutil.Factory, flagVal string) (string, error) {
	ref, note, err := specMemoryDefault(f, flagVal)
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", exitcode.Newf(exitcode.Usage,
			"a memory is required: pass -m/--memory, or set a spec default with `hadron spec use <org::memory>`")
	}
	if note != "" {
		fmt.Fprintln(f.IOStreams.ErrOut, note)
	}
	return ref, nil
}

// effectiveSpecMemoryOptional is effectiveSpecMemory for list/search commands
// (ls, find) where memory is an optional scope: it returns "" (no error) when
// nothing is configured, so the command runs unscoped. A configured default is
// still honored (and noted), so `hadron spec use` scopes a bare `ls`/`find`.
func effectiveSpecMemoryOptional(f *cmdutil.Factory, flagVal string) (string, error) {
	ref, note, err := specMemoryDefault(f, flagVal)
	if err != nil {
		return "", err
	}
	if ref != "" && note != "" {
		fmt.Fprintln(f.IOStreams.ErrOut, note)
	}
	return ref, nil
}

// specMemoryURN is resolveSpecMemoryURN with the spec-memory default chain
// applied to an empty flag value (see effectiveSpecMemory).
func specMemoryURN(f *cmdutil.Factory, cmd *cobra.Command, client graphql.Client, flagVal string) (string, error) {
	ref, err := effectiveSpecMemory(f, flagVal)
	if err != nil {
		return "", err
	}
	return resolveSpecMemoryURN(cmd, client, ref)
}

// specMemoryID is resolveSpecMemoryID with the spec-memory default chain applied.
func specMemoryID(f *cmdutil.Factory, cmd *cobra.Command, client graphql.Client, flagVal string) (id, memURN string, err error) {
	ref, err := effectiveSpecMemory(f, flagVal)
	if err != nil {
		return "", "", err
	}
	return resolveSpecMemoryID(cmd, client, ref)
}

// lookupSpecMemory matches a memory ref against the memories list by PK, urn (in any
// scheme/colon form), or name, returning the memory's id and canonical
// <org>::<memory> urn. A ref reachable as a node filter without an id lookup is
// served by resolveSpecMemoryURN's fast path before this is ever called.
func lookupSpecMemory(cmd *cobra.Command, client graphql.Client, ref string) (id, memURN string, err error) {
	ref = strings.TrimSpace(ref)
	norm := stripMemoryScheme(ref)
	if norm == "" {
		// Empty, or a bare scheme prefix like "hrn:memory:" — nothing to match
		// (and an empty want must not collide with an empty-urn memory).
		return "", "", exitcode.Newf(exitcode.Usage, "a memory is required: pass -m/--memory <org::memory>")
	}
	want := collapseColons(norm)
	// Every class, system included (memories() hides system by default), paged
	// to exhaustion — the name/urn match and the "did you mean" suggestions
	// both need the caller's whole memory list (hadron-server#473).
	filter := &gen.MemoryFilter{MemoryClasses: gen.AllMemoryClass}
	items, err := api.CollectAll(func(limit, offset int) ([]*gen.MemoriesMemoriesMemoriesPageItemsMemory, int, error) {
		resp, err := gen.Memories(cmd.Context(), client, filter, &limit, &offset)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		if resp == nil || resp.Memories == nil {
			return nil, 0, nil
		}
		return resp.Memories.Items, resp.Memories.Total, nil
	})
	if err != nil {
		return "", "", err
	}
	var available []string
	for _, m := range items {
		if m == nil {
			continue
		}
		canon := canonicalMemoryURN(m.Urn)
		if ref == m.Id || ref == m.Name || want == collapseColons(canon) {
			return m.Id, canon, nil
		}
		available = append(available, canon)
	}
	// A bare "not found" is a dead end — the author has to guess the real urn.
	// Point them at the closest match (same org, spec memories first) or list
	// what's available (issue #99 item 4).
	return "", "", exitcode.Newf(exitcode.NotFound, "memory %q not found%s", ref, memorySuggestion(norm, available))
}

// memorySuggestion turns a not-found memory ref into a "did you mean …?" /
// "available: …" tail. It prefers memories in the same org as the failed ref,
// and — when the ref names a specs memory — the spec memories within it, so a
// typo like "::platform-specs" lands on "::specs". An empty tail means there's
// nothing useful to suggest.
func memorySuggestion(norm string, available []string) string {
	if len(available) == 0 {
		return ""
	}
	sort.Strings(available)
	org := collapseColons(norm)
	if i := strings.Index(org, ":"); i >= 0 {
		org = org[:i]
	}
	pool := available
	if org != "" {
		var same []string
		for _, u := range available {
			if strings.HasPrefix(collapseColons(u), org+":") {
				same = append(same, u)
			}
		}
		if len(same) > 0 {
			pool = same
		}
	}
	if strings.Contains(strings.ToLower(norm), "spec") {
		var specs []string
		for _, u := range pool {
			if strings.Contains(strings.ToLower(u), "spec") {
				specs = append(specs, u)
			}
		}
		if len(specs) > 0 {
			pool = specs
		}
	}
	if len(pool) == 1 {
		return fmt.Sprintf(" — did you mean %q?", pool[0])
	}
	const cap = 8
	shown := pool
	suffix := ""
	if len(shown) > cap {
		shown = shown[:cap]
		suffix = ", …"
	}
	return " — available: " + strings.Join(shown, ", ") + suffix
}

// collapseColons folds the "::" org/memory separator to ":" so urns written
// either way compare equal.
func collapseColons(s string) string {
	return strings.ReplaceAll(s, "::", ":")
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
func fetchSpecNode(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (*gen.GetNodeNode, error) {
	id, err := resolveSpecNode(cmd, client, memoryURN, loc)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetNode(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "spec %q not found", loc)
	}
	return resp.Node, nil
}

// fetchSpecTaggedNode resolves a citation, reads the node, and requires it to
// be part of the spec corpus. Lint and register intentionally use the generic
// fetchSpecNode path because they validate malformed or advisory nodes.
func fetchSpecTaggedNode(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (*gen.GetNodeNode, Citation, error) {
	cit, err := ParseCitation(loc)
	if err != nil {
		return nil, Citation{}, err
	}
	n, err := fetchSpecNode(cmd, client, memoryURN, cit.Format())
	if err != nil {
		return nil, Citation{}, err
	}
	if !hasTag(n.Tags, "spec") {
		return nil, Citation{}, exitcode.Newf(exitcode.Usage, "%s is not a spec (no \"spec\" tag)", n.Loc)
	}
	return n, cit, nil
}

// fetchRegister reads the memory's `register` node (advisory; not a spec).
func fetchRegister(cmd *cobra.Command, client graphql.Client, memoryURN string) (*gen.GetNodeNode, error) {
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
// than a single unbounded findNodes call, which the server truncates to one
// default page (issue #23). For an allocation scan that truncation is not just
// under-reporting: a missed tail makes the allocator reuse a live number.
// Spec nodes are addressed by tag/prefix, never nodeType, so nodeType is left
// unset; add it back here if a caller ever needs it.
func scanAllNodes(ctx context.Context, client graphql.Client, memory, prefix *string, tags []string) ([]*api.ListNode, error) {
	return paginateNodes(func(limit, offset int) ([]*api.ListNode, error) {
		l, o := limit, offset
		page, err := api.FindNodes(ctx, client, nil, nil, newNodeFilter(memory, prefix, tags), sortLoc(), &l, &o)
		if err != nil {
			return nil, api.MapError(err)
		}
		return page.Nodes, nil
	})
}

// newNodeFilter builds the structured findNodes filter from the (memory, prefix,
// tags) selectors the old positional `nodes` query took: a single memory ref
// (id or URN) becomes filter.memoryIds, prefix becomes filter.locPrefix. Returns
// nil when nothing is constrained so the wire carries no empty filter object.
func newNodeFilter(memory, prefix *string, tags []string) *gen.NodeFilter {
	var f gen.NodeFilter
	set := false
	if memory != nil && *memory != "" {
		f.MemoryIds = []string{*memory}
		set = true
	}
	if prefix != nil && *prefix != "" {
		f.LocPrefix = prefix
		set = true
	}
	if len(tags) > 0 {
		f.Tags = tags
		set = true
	}
	if !set {
		return nil
	}
	return &f
}

// sortLoc pins the deterministic loc ordering the corpus scans (and bare
// `spec ls`) rely on — findNodes defaults to relevance, which is unscored (and
// thus unordered) for a query-less filtered list.
func sortLoc() *gen.NodeSort {
	s := gen.NodeSortLoc
	return &s
}

// paginateNodes drives the offset loop independently of the GraphQL layer so
// the termination logic is unit-testable. It requests fixed-size pages until a
// short (or empty) page signals the tail — a full page means there may be more,
// so it never stops early on a default-capped response. fetch is called with
// (limit, offset).
func paginateNodes(fetch func(limit, offset int) ([]*api.ListNode, error)) ([]*api.ListNode, error) {
	var all []*api.ListNode
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
	Name string
	Loc  string // the target's loc
}

// specNode is a lint/render-friendly projection of a node, decoupled from
// the genqlient types so the rule engine is trivially unit-testable.
type specNode struct {
	Loc                string
	Unavailable        bool
	Name               string
	NodeType           string
	Tags               []string
	Abstract           *string
	AbstractOriginHash *string
	Content            *string
	DataVersion        string // data.version, "" if absent/unparseable
	OutEdges           []specEdge
}

func nodeFromGQL(n *gen.GetNodeNode) specNode {
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
		sn.OutEdges = append(sn.OutEdges, specEdge{Name: edgeNameStr(e.Name), Loc: e.Target.Loc})
	}
	return sn
}

// nodeFromBatch is nodeFromGQL for a bulk-read (nodeBatch) node — same lint
// projection, so a batch-read spec lints identically to a single-read one.
func nodeFromBatch(n *gen.NodeBatchNodeBatchNodeBatchResultNodesNode) specNode {
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
		sn.OutEdges = append(sn.OutEdges, specEdge{Name: edgeNameStr(e.Name), Loc: e.Target.Loc})
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
