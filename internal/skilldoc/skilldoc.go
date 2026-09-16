// Package skilldoc is the pure half of `hadron skill` (hadron-cli#580): the
// contract between a Hadron task node and the skill file exported from it.
// It knows how a skill's NAME is derived, how its provenance HASH is computed,
// what the generated file looks like, how to read one back, and which corpus
// rules a declaring node must satisfy. It touches neither the network nor the
// disk, so every rule here is table-testable; the command package wires it to
// GraphQL and the filesystem.
//
// The design is in docs/plans/skill-command-group.md. The two load-bearing
// decisions: the node is the source and the file is a build artifact (D2), and
// staleness is content-addressed — the file records a hash of the exact inputs
// it was built from, so drift is detected by recomputation and never by clock
// (D5).
package skilldoc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"

	"github.com/hadron-memory/hadron-cli/internal/nodedoc"
)

// Host limits for a Claude Code skill, per the skill spec (verified against the
// official skill-creator validator, quick_validate.py), counted in CHARACTERS
// — the validator is Python and counts code points, so an em dash is one, not
// three. The host does not REFUSE an over-long description — it truncates it
// in the skill listing, so the trigger phrases past the cut silently stop
// firing. That is why the description rule is an error rather than a warning.
const (
	MaxNameLen        = 64
	MaxDescriptionLen = 1024
)

// DefaultPrefix is the export namespace of a task in a USER-owned memory
// (Holger, 2026-09-15): a personal task has no org to name, so it takes the
// platform's. An org-owned task uses its org's `Organization.skillPrefix`
// (hadron-server#1164) and never this.
const DefaultPrefix = "hadron-"

// Finding severities — the same strings `spec lint` renders, so a caller
// scripting both sees one vocabulary.
const (
	SevError   = "error"
	SevWarning = "warning"
)

// declarationKeys are the property keys a node may declare a skill under, in
// precedence order: the provider-neutral key (D10) wins over the legacy one.
// ONE list, read by one scan (classify), so a key added later cannot be
// recognised by the declaration reader and missed by the malformed check.
var declarationKeys = []string{"skill", "claudeSkill"}

var (
	// nameRE is the skill-name grammar: kebab-case, lowercase letters and
	// digits, single hyphens between segments.
	nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// prefixRE mirrors the server's validation of Organization.skillPrefix: the
	// stored value IS the literal prefix, trailing hyphen included.
	prefixRE = regexp.MustCompile(`^[a-z][a-z0-9]*-$`)
	// headerRE reads the machine line a generated file carries (§4.3 of the
	// plan): `<!-- hadron-skill source=<urn> hash=<hex> -->`. Keys are
	// order-independent so a later host can add its own without breaking
	// readers of this one.
	headerRE = regexp.MustCompile(`(?m)^<!--\s*hadron-skill((?:\s+\w+=\S+)+)\s*-->$`)
	headerKV = regexp.MustCompile(`(\w+)=(\S+)`)
	// legacyHeaderRE reads the pre-#580 provenance comment written by the
	// hand-run export procedure: `<!-- Generated from <urn> -->`. It carries a
	// source but no hash, so a file with only this line classifies as unhashed.
	// The token must LOOK like a node URN (a scheme, or the `::` grammar): a
	// body opening with `<!-- Generated from a template -->` is a body.
	legacyHeaderRE = regexp.MustCompile(`(?m)^<!--\s*Generated from\s+((?:hrn|urn):\S+|\S+::\S+::\S+)\s*-->$`)
	// frontmatterRE splits a skill file into its YAML header and body — the
	// same framing nodedoc reads for node files (`---\n…\n---\n`).
	frontmatterRE = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?(.*)\z`)
	// triggerRE is the trigger-shaped phrasing a description is expected to
	// carry — the host matches descriptions against what the user says, so
	// a description that never says when to use the skill rarely fires.
	triggerRE = regexp.MustCompile(`(?i)\buse (it |this )?when\b`)
	// templateRE finds Mustache placeholders in a body. Export is verbatim, so
	// a placeholder ships as literal text; that may be intended (a briefing
	// template) or a leak, which is why it is a warning and not an error.
	templateRE = regexp.MustCompile(`\{\{[^}]*\}\}`)
)

// Prefix is a memory's resolved export prefix. Known is false when the memory
// is org-owned and the org has chosen no `Organization.skillPrefix`: Value is
// then empty, and the rules that need a prefix say so rather than deriving a
// name from nothing. Carrying the pair keeps "unknown" distinct from "empty"
// — a server that ever coerced a null prefix to "" would otherwise read as
// resolved (this repo's recurring nil-guard class).
type Prefix struct {
	Value string
	Known bool
}

// Declaration is a node's opt-in to export: `properties.skill` (D10, the
// provider-neutral key) or the legacy `properties.claudeSkill`, whichever is
// present. Key records which one, so lint can steer a legacy declaration to
// the new key. Name is the pre-#580 hand-set skill name; it is retired in
// favor of derivation (D8) and accepted during transition only when it equals
// the derived value.
type Declaration struct {
	Key         string
	Description string
	Name        string
}

// classify is the ONE scan of a node's declaration keys. It returns the
// declaration under the highest-precedence key that holds an object, and
// every key that is present but does NOT hold an object — a malformed
// declaration is reported even when a valid one sits beside it, because the
// node is mid-migration and the broken key is the one that will survive the
// legacy key's retirement.
func classify(props map[string]any) (decl *Declaration, malformed []string) {
	for _, key := range declarationKeys {
		raw, ok := props[key]
		if !ok {
			continue
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			malformed = append(malformed, key)
			continue
		}
		if decl != nil {
			continue
		}
		decl = &Declaration{Key: key}
		if s, ok := obj["description"].(string); ok {
			decl.Description = s
		}
		if s, ok := obj["name"].(string); ok {
			decl.Name = s
		}
	}
	return decl, malformed
}

// Declared reads a node's skill declaration out of its decoded properties.
// A declaration is an object under one of the declaration keys; anything
// else — the key absent, or holding a non-object — is "not declared". The
// new key wins when both are present, so a migrated node whose legacy key
// was left behind behaves as migrated.
func Declared(props map[string]any) (*Declaration, bool) {
	decl, _ := classify(props)
	return decl, decl != nil
}

// Malformed reports the declaration keys a node carries whose value is not an
// object — `"skill": "yes"`, `"claudeSkill": true`. Such a node is selected by
// the server-side discovery predicate (the key EXISTS), so without a finding
// it would be silently skipped and counted in nobody's total — an all-clear
// wider than the read that produced it.
func Malformed(props map[string]any) []string {
	_, bad := classify(props)
	return bad
}

// ValidPrefix reports whether p is a legal export prefix — the server's own
// rule for Organization.skillPrefix, applied to a `--prefix` override so an
// override cannot mint a name the org field could never hold.
func ValidPrefix(p string) bool { return prefixRE.MatchString(p) }

// DeriveName composes a skill's name from its export prefix and its node loc
// (D8): the leading `tasks` segment is structure rather than name and is
// dropped when the loc has more than one segment; the remaining segments are
// joined with hyphens. `tasks:create-release-tag` → `<prefix>create-release-tag`;
// a root-level `export-task-as-claude-skill` → `<prefix>export-task-as-claude-skill`;
// a nested `tasks:review:run` → `<prefix>review-run`. The result is not
// validated here — see ValidName — so a caller can report the derived value
// that failed rather than an empty string.
func DeriveName(prefix, loc string) string {
	segs := strings.Split(loc, ":")
	if len(segs) > 1 && segs[0] == "tasks" {
		segs = segs[1:]
	}
	return prefix + strings.Join(segs, "-")
}

// ValidName reports whether name satisfies the host's grammar and length.
func ValidName(name string) bool {
	return utf8.RuneCountInString(name) <= MaxNameLen && nameRE.MatchString(name)
}

// NormalizeDescription is the description as exported: surrounding
// whitespace trimmed. Lint measures this and Render writes this, so the
// length lint certifies is the length the host sees — a description with a
// trailing space is not 1025 characters on disk.
func NormalizeDescription(s string) string { return strings.TrimSpace(s) }

// NormalizeBody is the node content as exported: leading and trailing
// newlines trimmed. Render writes this, Hash fingerprints this, and
// ParseFile's header-stripping is its exact inverse, so a body that begins
// with a blank line does not make a fresh export read as locally edited.
func NormalizeBody(s string) string { return strings.Trim(s, "\n") }

// Hash is the provenance fingerprint a generated file records: the first 16
// hex characters of SHA-256 over the three inputs the file is made from —
// name, description and body — NUL-separated so a boundary shift cannot
// collide. It is deliberately not nodedoc.ContentHash (content only): the
// description is the trigger, so an edit to it must read as stale. Every
// input is present in the file itself, which is what lets a reader recompute
// the hash from the file alone and detect a hand edit without the server.
//
// Inputs are hashed in their exported form (NormalizeDescription,
// NormalizeBody) — what Render writes and ParseFile reads back — so a node
// whose content is wrapped in blank lines and the file built from it
// fingerprint identically. Without that, every export would classify as
// stale against its own header.
func Hash(name, description, content string) string {
	description = NormalizeDescription(description)
	content = NormalizeBody(content)
	sum := sha256.Sum256([]byte(name + "\x00" + description + "\x00" + content))
	return hex.EncodeToString(sum[:])[:16]
}

// Node is the slice of a Hadron node the skill contract reads. The command
// package fills it from a NodeBatch projection — the batched read, because a
// single-ref read compiles Mustache and would blank `{{name}}`-style
// placeholders out of a verbatim export (D6).
type Node struct {
	URN        string
	Loc        string
	MemoryURN  string
	IsRunnable bool
	Content    string
	Properties map[string]any
}

// Finding is one lint result. URN names the node; for a memory-level finding
// (a prefix the org has not chosen) URN is the memory URN and Memory equals
// it. Memory is carried on every finding so a renderer never has to look it
// back up from the node it came from.
type Finding struct {
	URN      string
	Memory   string
	Rule     string
	Severity string
	Message  string
}

// Lint applies the per-node corpus rules to a node under the export prefix
// its memory resolves to. When the prefix is not Known the name rules run on
// the bare slug — a loc that derives to an invalid name is invalid under any
// prefix, and the missing prefix is LintPrefixes' finding, not this one's. A
// node with no declaration and no malformed key yields nothing: not declared
// is not a defect, it is the opt-in working.
func Lint(n Node, prefix Prefix) []Finding {
	var out []Finding
	add := func(rule, sev, msg string) {
		out = append(out, Finding{URN: n.URN, Memory: n.MemoryURN, Rule: rule, Severity: sev, Message: msg})
	}
	decl, malformed := classify(n.Properties)
	for _, key := range malformed {
		add("skill-declaration-malformed", SevError,
			fmt.Sprintf("properties.%s is present but is not an object — a declaration is {\"description\": …}; fix it or remove the key", key))
	}
	if decl == nil {
		return out
	}

	if decl.Key == "claudeSkill" {
		add("skill-legacy-key", SevWarning,
			"declared under properties.claudeSkill — move it to properties.skill (the provider-neutral key); claudeSkill is read as an alias during transition only")
	}

	desc := NormalizeDescription(decl.Description)
	switch n := utf8.RuneCountInString(desc); {
	case desc == "":
		add("skill-description-missing", SevError,
			fmt.Sprintf("properties.%s.description is missing or empty — it is the trigger text the host matches against; the node cannot be exported without it", decl.Key))
	case n > MaxDescriptionLen:
		add("skill-description-too-long", SevError,
			fmt.Sprintf("description is %d characters; the host caps it at %d and TRUNCATES the rest in the skill listing, so trigger phrases past the cut never fire — shorten by %d",
				n, MaxDescriptionLen, n-MaxDescriptionLen))
	}
	if desc != "" && !triggerRE.MatchString(desc) {
		add("skill-description-no-trigger", SevWarning,
			"description never says when to use the skill (\"Use when …\") — the host matches descriptions against what the user says, and one with no trigger phrasing rarely fires")
	}

	name := DeriveName(prefix.Value, n.Loc)
	if !ValidName(name) {
		add("skill-name-invalid", SevError,
			fmt.Sprintf("derived skill name %q is not a valid skill name (kebab-case, ≤%d chars) — it is derived from the loc, so the loc is what to change", name, MaxNameLen))
	}
	if decl.Name != "" && prefix.Known && decl.Name != name {
		add("skill-name-hand-set", SevError,
			fmt.Sprintf("properties.%s.name is %q but the name is derived from the loc as %q — remove the hand-set name (it is retired) or make the loc say what the name should", decl.Key, decl.Name, name))
	}

	if !n.IsRunnable {
		add("skill-not-runnable", SevError,
			"node declares a skill but isRunnable is not true — a skill is a runnable task; set it (`hadron node update <urn> --runnable`) or drop the declaration")
	}

	body := strings.TrimSpace(n.Content)
	switch {
	case body == "":
		add("skill-content-empty", SevError,
			"node has no content — the body IS the skill; nothing to export")
	case frontmatterRE.MatchString(body + "\n"):
		// A closing `---` is what makes it frontmatter; a body that merely
		// opens with a horizontal rule is a body.
		add("skill-content-has-frontmatter", SevError,
			"content starts with a `---` frontmatter block — the node body is the skill BODY; frontmatter is composed at export from the declaration and would be doubled")
	}
	if templateRE.MatchString(n.Content) {
		add("skill-content-has-template", SevWarning,
			"content contains a {{…}} placeholder — export is verbatim, so it ships as literal text; keep it only if the skill is meant to carry a template")
	}
	return out
}

// LintPrefixes applies the memory-level rule: a memory that holds at least
// one declaring node must have a Known prefix, or no name can be derived for
// its tasks. A memory with nothing to export is silent — reporting it anyway
// made `--all` red on 46 memories in the first live run, the report nobody
// reads. One finding per memory, keyed on the memory URN, in URN order.
func LintPrefixes(nodes []Node, prefixes map[string]Prefix) []Finding {
	declaring := map[string]bool{}
	for _, n := range nodes {
		if _, ok := Declared(n.Properties); ok {
			declaring[n.MemoryURN] = true
		}
	}
	mems := make([]string, 0, len(declaring))
	for m := range declaring {
		if !prefixes[m].Known {
			mems = append(mems, m)
		}
	}
	sort.Strings(mems)
	out := make([]Finding, 0, len(mems))
	for _, m := range mems {
		out = append(out, Finding{
			URN: m, Memory: m, Rule: "skill-prefix-missing", Severity: SevError,
			Message: "the owning org has chosen no Organization.skillPrefix, so no skill name can be derived for this memory's tasks — set it as an org admin or pass --prefix; the name rules ran on the bare slug",
		})
	}
	return out
}

// LintCollisions applies the corpus-level rule: two declaring nodes may not
// derive the same skill name, because the second export would silently win.
// Both members of a collision are reported, each naming the other, so
// whichever the reader opens explains itself.
//
// A node whose memory has no Known prefix is skipped: its name cannot be
// derived, so it cannot collide — and comparing bare slugs would report two
// prefix-less ORGS as colliding on `create-release-tag` when the prefixes they
// have yet to choose are exactly what keeps them apart (measured on the first
// live `--all` run: marketrailz vs micromentor.org). Those nodes already
// carry LintPrefixes' finding.
func LintCollisions(nodes []Node, prefixes map[string]Prefix) []Finding {
	byName := map[string][]Node{}
	for _, n := range nodes {
		if _, ok := Declared(n.Properties); !ok {
			continue
		}
		p := prefixes[n.MemoryURN]
		if !p.Known {
			continue
		}
		name := DeriveName(p.Value, n.Loc)
		byName[name] = append(byName[name], n)
	}
	names := make([]string, 0, len(byName))
	for name, group := range byName {
		if len(group) > 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []Finding
	for _, name := range names {
		group := byName[name]
		for _, n := range group {
			others := make([]string, 0, len(group)-1)
			for _, o := range group {
				if o.URN != n.URN {
					others = append(others, o.URN)
				}
			}
			out = append(out, Finding{
				URN: n.URN, Memory: n.MemoryURN, Rule: "skill-name-collision", Severity: SevError,
				Message: fmt.Sprintf("derives skill name %q, and so does %s — one name, one task; supersede the fork or rename a loc", name, strings.Join(others, ", ")),
			})
		}
	}
	return out
}

// The two human-readable provenance lines, verbatim: the one Render writes
// and the one the pre-#580 procedure wrote. The parser matches these EXACTLY
// (and the machine line by its full grammar), never by substring — a body
// that opens with `<!-- Generated from a template -->` is content, and
// eating it would make a fresh export read as locally edited.
const (
	humanLine       = "<!-- Generated by `hadron skill export`. Edit the source node and re-export; do not edit this file. -->"
	legacyHumanLine = "<!-- Edit the source node and re-export; do not edit this file directly. -->"
)

// frontmatter is the header a skill host reads — exactly the two keys, in
// this order, so the file looks like every hand-written skill on disk.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Render composes the skill file (§4.3): YAML frontmatter with the two keys a
// host reads, the machine-parseable provenance line, the human note, and the
// node body verbatim. The frontmatter goes through the real YAML encoder
// (nodedoc.MarshalYAML, the library nodedoc already depends on) rather than
// hand-quoting: a description ending in a colon, or in a space, is a plain
// scalar a hand check would pass and a real parser would reject or trim, and
// the host reading this file IS a real parser.
func Render(name, source, description, content string) (string, error) {
	description = NormalizeDescription(description)
	content = NormalizeBody(content)
	fm, err := nodedoc.MarshalYAML(frontmatter{Name: name, Description: description})
	if err != nil {
		return "", fmt.Errorf("rendering frontmatter: %w", err)
	}
	hash := Hash(name, description, content)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fm)
	b.WriteString("\n---\n\n")
	fmt.Fprintf(&b, "<!-- hadron-skill source=%s hash=%s -->\n", source, hash)
	b.WriteString(humanLine + "\n\n")
	b.WriteString(content)
	b.WriteString("\n")
	return b.String(), nil
}

// File is what ParseFile reads back from a skill file on disk: the frontmatter
// keys a host reads, the provenance the header carries, and the body — enough
// to recompute the hash and pair the file to its source node without the
// server.
type File struct {
	Name        string
	Description string
	Source      string // canonical source URN, or "" when the file is not Hadron-generated
	Hash        string // "" for a legacy (pre-#580) header
	Body        string
}

// ParseFile reads a skill file with the same YAML parser a host uses, so a
// header this reader accepts is one the host accepts. A file with no
// frontmatter is an error; a file with no provenance header parses with
// Source == "" — it is somebody else's skill, and every `hadron skill` command
// leaves it alone. The legacy `Generated from` header yields a Source and no
// Hash.
func ParseFile(data []byte) (*File, error) {
	m := frontmatterRE.FindSubmatch(data)
	if m == nil {
		return nil, fmt.Errorf("no frontmatter")
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	f := &File{Name: fm.Name, Description: fm.Description}
	preamble, body := splitPreamble(string(m[2]))
	if h := headerRE.FindStringSubmatch(preamble); h != nil {
		for _, kv := range headerKV.FindAllStringSubmatch(h[1], -1) {
			switch kv[1] {
			case "source":
				f.Source = kv[2]
			case "hash":
				f.Hash = kv[2]
			}
		}
	} else if h := legacyHeaderRE.FindStringSubmatch(preamble); h != nil {
		f.Source = h[1]
	}
	f.Body = body
	return f, nil
}

// splitPreamble divides what follows the frontmatter into the PREAMBLE — the
// leading run of blank lines and single-line HTML comments, which is where
// Render puts the provenance — and the body, normalized. Provenance is
// recognised in the preamble ONLY: a hand-written skill that quotes an
// example `<!-- hadron-skill … -->` line in its body must not read as
// Hadron-generated, or `export` could one day overwrite it as an owned
// artifact (Codex on #589). The preamble's comment lines are what Render
// wrote, so dropping them from the body is the exact inverse of Render.
func splitPreamble(rest string) (preamble, body string) {
	lines := strings.Split(rest, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || isProvenanceComment(t) {
			i++
			continue
		}
		break
	}
	return strings.Join(lines[:i], "\n"), NormalizeBody(strings.Join(lines[i:], "\n"))
}

// isProvenanceComment recognises exactly the comment lines Render writes (and
// the pre-#580 procedure wrote) — the machine line by its full grammar, the
// human lines verbatim. Only those are preamble: a body that begins with an
// HTML comment of its own, however similar, keeps it, or a fresh export would
// parse to a different body than it hashed (Codex on #589, rounds 3 and 4).
func isProvenanceComment(t string) bool {
	return t == humanLine || t == legacyHumanLine ||
		headerRE.MatchString(t) || legacyHeaderRE.MatchString(t)
}
