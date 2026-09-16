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

	urnlib "github.com/hadron-memory/urn-lib-go"

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
	legacyHeaderRE = regexp.MustCompile(`(?m)^<!--\s*Generated from\s+(\S+)\s*-->$`)
	// hashRE is the provenance hash's exact shape: 16 lowercase hex characters.
	hashRE = regexp.MustCompile(`^[0-9a-f]{16}$`)
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
	// NameSet records that a `name` key was present, so an explicitly EMPTY
	// stored name is judged against the derived one rather than read as
	// absent (Copilot on #589, round 2).
	NameSet bool
}

// classify is the ONE scan of a node's declaration keys. It returns the
// declaration under the highest-precedence key that holds an object, and
// every key PATH that is present but of the wrong shape — a declaration
// key that is not an object, or a `description`/`name` inside one that is
// not a string. A malformed key is reported even when a valid declaration
// sits beside it, because the node is mid-migration and the broken key is
// the one that will survive the legacy key's retirement; a non-string
// `name` is reported rather than ignored, or a stored name the contract
// says must equal the derived one would escape the rule by being a number.
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
		for _, field := range []string{"description", "name"} {
			if v, present := obj[field]; present {
				if _, isStr := v.(string); !isStr {
					malformed = append(malformed, key+"."+field)
				}
			}
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
			decl.NameSet = true
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

// NormalizeBody is the node content as exported: CRLF folded to LF (a body
// authored on Windows is the same body), then leading and trailing newlines
// trimmed. Render writes this, Hash fingerprints this, every body rule reads
// this, and ParseFile's header-stripping is its exact inverse — so a body
// that begins with a blank line, or carries \r\n, does not make a fresh
// export read as locally edited or slip past a rule written for LF.
func NormalizeBody(s string) string {
	return strings.Trim(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

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
			fmt.Sprintf("properties.%s is present but has the wrong shape — a declaration is {\"description\": \"…\"} with string fields; fix it or remove the key", key))
	}
	if decl == nil {
		return out
	}

	switch _, legacy := n.Properties["claudeSkill"].(map[string]any); {
	case decl.Key == "claudeSkill":
		add("skill-legacy-key", SevWarning,
			"declared under properties.claudeSkill — move it to properties.skill (the provider-neutral key); claudeSkill is read as an alias during transition only")
	case legacy:
		// Migrated, but the alias was left behind: the export reads the new
		// key, so the old one is dead weight that the retirement will strand.
		add("skill-legacy-key", SevWarning,
			"properties.claudeSkill is still present beside properties.skill — remove the legacy key; the export reads only the new one")
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
	if decl.NameSet && prefix.Known && decl.Name != name {
		add("skill-name-hand-set", SevError,
			fmt.Sprintf("properties.%s.name is %q but the name is derived from the loc as %q — remove the hand-set name (it is retired) or make the loc say what the name should", decl.Key, decl.Name, name))
	}

	if !n.IsRunnable {
		add("skill-not-runnable", SevError,
			"node declares a skill but isRunnable is not true — a skill is a runnable task; set it (`hadron node update <urn> --runnable`) or drop the declaration")
	}

	body := NormalizeBody(n.Content)
	switch {
	case strings.TrimSpace(body) == "":
		add("skill-content-empty", SevError,
			"node has no content — the body IS the skill; nothing to export")
	case frontmatterRE.MatchString(body + "\n"):
		// Inspected as it will be EXPORTED (not further trimmed): an indented
		// `  ---` is content on disk, so it is content here.
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
// node body NORMALIZED (NormalizeBody: CRLF folded, surrounding newlines
// trimmed — the same form Hash fingerprints and ParseFile reads back; inner
// content is untouched). The frontmatter goes through the real YAML encoder
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
	Source      string // the flat v2 source node URN as written, or "" when the file is not Hadron-generated
	Hash        string // "" for a legacy (pre-#580) header
	Body        string
	// Extra holds every frontmatter key other than name/description. Render
	// writes none, so on a generated file a non-empty Extra IS a local edit
	// — one the header hash cannot see, since the hash covers the three
	// rendered inputs — and status/export must treat it as `locally-edited`
	// rather than overwrite it (Codex on #589, round 11).
	Extra map[string]any
}

// ParseFile reads a skill file with the same YAML parser a host uses, so a
// header this reader accepts is one the host accepts. A file with no
// frontmatter is an error; a file with no provenance header parses with
// Source == "" — it is somebody else's skill, and every `hadron skill` command
// leaves it alone. The legacy `Generated from` header yields a Source and no
// Hash.
func ParseFile(data []byte) (*File, error) {
	data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
	m := frontmatterRE.FindSubmatch(data)
	if m == nil {
		return nil, fmt.Errorf("no frontmatter")
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	f := &File{Name: fm.Name, Description: NormalizeDescription(fm.Description)}
	var all map[string]any
	if err := yaml.Unmarshal(m[1], &all); err == nil {
		for k, v := range all {
			if k != "name" && k != "description" {
				if f.Extra == nil {
					f.Extra = map[string]any{}
				}
				f.Extra[k] = v
			}
		}
	}
	preamble, body := splitPreamble(string(m[2]))
	for _, line := range strings.Split(preamble, "\n") { // raw, like splitPreamble
		if src, hash, ok := machineHeader(line); ok {
			f.Source, f.Hash = src, hash
			break
		}
		if src, ok := legacyHeader(line); ok {
			f.Source = src
			break
		}
	}
	f.Body = body
	return f, nil
}

// splitPreamble divides what follows the frontmatter into the PREAMBLE —
// what Render writes before the body: a blank line, at most ONE machine (or
// legacy) provenance line, at most ONE human line, a blank line — and the
// body, normalized. Provenance is recognised in the preamble ONLY, and each
// kind of line at most once, so a body that legitimately begins with the
// exact human line (or with a whitespace-only line, which NormalizeBody
// keeps) is body and round-trips hash-equal to its header (Copilot on #589,
// round 3). Only truly EMPTY lines are skipped as framing.
func splitPreamble(rest string) (preamble, body string) {
	lines := strings.Split(rest, "\n")
	i := 0
	var sawMachine, sawHuman bool
	for i < len(lines) {
		line := lines[i] // RAW: Render never indents, so an indented lookalike is body
		switch {
		case line == "":
		case !sawMachine && (isMachineHeader(line) || isLegacyHeader(line)):
			sawMachine = true
		case sawMachine && !sawHuman && (line == humanLine || line == legacyHumanLine):
			// The human line is renderer preamble only AFTER a provenance
			// line; a foreign body that happens to start with it keeps it.
			sawHuman = true
		default:
			return strings.Join(lines[:i], "\n"), NormalizeBody(strings.Join(lines[i:], "\n"))
		}
		i++
	}
	return strings.Join(lines[:i], "\n"), ""
}

// isNodeURN reports whether tok is a flat v2 NODE URN — `hrn:node:<root>:<slug>:<loc>`,
// the one form node reads emit and every export has written. Provenance may
// only ever name a node in that form: a header naming another entity kind, a
// legacy `::` or `urn:` spelling, or a bare id is not ours (Holger,
// 2026-09-16: no v1 support in this surface).
func isNodeURN(tok string) bool {
	return strings.HasPrefix(tok, "hrn:node:") && !strings.Contains(tok, "::") &&
		urnlib.AssertFullyQualifiedUrn(tok, "node") == nil
}

// machineHeader parses the generated machine line and returns its source
// and hash — or ok=false unless the line has the `hadron-skill` grammar, a
// `source` that is a node URN AND a `hash` of exactly 16 hex characters
// (extra keys allowed). A comment that merely looks like one —
// `<!-- hadron-skill example=yes -->`, `source=not-a-node`, a truncated
// hash — is body, not provenance (Copilot on #589, rounds 2 and 3).
func machineHeader(t string) (source, hash string, ok bool) {
	h := headerRE.FindStringSubmatch(t)
	if h == nil {
		return "", "", false
	}
	for _, kv := range headerKV.FindAllStringSubmatch(h[1], -1) {
		switch kv[1] {
		case "source":
			source = kv[2]
		case "hash":
			hash = kv[2]
		}
	}
	if !isNodeURN(source) || !hashRE.MatchString(hash) {
		return "", "", false
	}
	return source, hash, true
}

// legacyHeader parses the pre-#580 `<!-- Generated from <urn> -->` line;
// ok=false unless the token is a flat v2 node URN (which every hand-run
// export wrote).
func legacyHeader(t string) (source string, ok bool) {
	h := legacyHeaderRE.FindStringSubmatch(t)
	if h == nil || !isNodeURN(h[1]) {
		return "", false
	}
	return h[1], true
}

func isMachineHeader(t string) bool { _, _, ok := machineHeader(t); return ok }
func isLegacyHeader(t string) bool  { _, ok := legacyHeader(t); return ok }
