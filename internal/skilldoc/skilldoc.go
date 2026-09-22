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

// Finding severities — the same strings `spec lint` renders, so a caller
// scripting both sees one vocabulary.
const (
	SevError   = "error"
	SevWarning = "warning"
)

// ExportsKey is the property holding a node's per-host export declarations
// (D12): an OBJECT keyed by host, so "one export per host" is structural and
// discovery stays a key check rather than a containment match.
const ExportsKey = "exports"

// HostClaudeSkill is the only host with a specified renderer (D10/D12). Other
// keys under `exports` are recognised as declarations but not validated here —
// a second host's limits arrive with its renderer, not before it.
const HostClaudeSkill = "claudeSkill"

// legacyTopLevelKeys are the pre-D12 declaration properties, read as aliases
// for `exports.claudeSkill` so nothing has to be migrated to keep working
// (Holger, 2026-09-19: there is no data migration). ONE list, read by one scan
// (classify), so a key added later cannot be recognised by the declaration
// reader and missed by the malformed check.
var legacyTopLevelKeys = []string{"skill", "claudeSkill"}

var (
	// nameRE is the skill-name grammar: kebab-case, lowercase letters and
	// digits, single hyphens between segments.
	nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
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
	// The header group is optional so an EMPTY block (`---\n---\n…`) is still
	// frontmatter — to the host, and therefore to the frontmatter rule.
	frontmatterRE = regexp.MustCompile(`(?s)\A---\n(?:(.*?)\n)?---\n?(.*)\z`)
	// triggerRE is the trigger-shaped phrasing a description is expected to
	// carry — the host matches descriptions against what the user says, so
	// a description that never says when to use the skill rarely fires.
	triggerRE = regexp.MustCompile(`(?i)\buse (it |this )?when\b`)
	// templateRE finds Mustache placeholders in a body. Export is verbatim, so
	// a placeholder ships as literal text; that may be intended (a briefing
	// template) or a leak, which is why it is a warning and not an error.
	templateRE = regexp.MustCompile(`\{\{[^}]*\}\}`)
)

// Declaration is a node's opt-in to export for ONE host: an object at `properties.
// exports.<host>` (D12), or one of the retired top-level keys read as an alias
// for the claudeSkill host. Key is the property PATH it was found at, so a
// message can name what the author actually wrote.
//
// Name is now AUTHORITATIVE and stored: D12 retired derivation, so there is no
// value to fall back to and a missing name is a finding rather than a default.
//
// Enable is the per-host on/off switch and it DEFAULTS TO OFF (Holger,
// 2026-09-19). Publishing a skill is the side-effecting act, so it takes an
// explicit `enable: true`; a declaration that merely exists does not publish.
// The reason is the corpus: it holds many runnable nodes that are automation and
// were never meant to be skills at all, so the safe default is the one where
// nothing ships unless somebody said to ship it.
//
// EnableSet records whether the key was present, which lets a report distinguish
// "switched off" from "never switched on". It is reporting detail, not a gate.
type Declaration struct {
	Key         string
	Host        string
	Description string
	Name        string
	// NameSet records that a `name` key was present, so an explicitly EMPTY
	// stored name is judged rather than read as absent (Copilot on #589,
	// round 2 — the rule it was found on is gone, the trap is not).
	NameSet   bool
	Enable    bool
	EnableSet bool
}

// classify is the ONE scan of a node's declaration properties. It returns the
// declaration for the claudeSkill host — the only host with a specified
// renderer — and every property PATH that is present but of the wrong shape.
//
// Precedence: `exports.claudeSkill` wins over the retired top-level `skill`,
// which wins over `claudeSkill`, so a node that has been given the new shape
// behaves as migrated even with an alias left beside it.
//
// Malformed covers `exports` not being an object, a host entry not being an
// object, a non-string `description`/`name`, and a non-boolean `enable`. A
// malformed path is reported even when a valid declaration sits beside it: the
// node is mid-migration and the broken key is the one that outlives the alias.
// A wrongly-TYPED field is reported rather than ignored, or a value the
// contract requires would escape its rule by being a number.
func classify(props map[string]any) (decl *Declaration, malformed []string) {
	read := func(path, host string, obj map[string]any) *Declaration {
		for _, field := range []string{"description", "name"} {
			if v, present := obj[field]; present {
				if _, isStr := v.(string); !isStr {
					malformed = append(malformed, path+"."+field)
				}
			}
		}
		if v, present := obj["enable"]; present {
			if _, isBool := v.(bool); !isBool {
				malformed = append(malformed, path+".enable")
			}
		}
		// Enable defaults to FALSE: publishing takes an explicit opt-in.
		d := &Declaration{Key: path, Host: host}
		if s, ok := obj["description"].(string); ok {
			d.Description = s
		}
		if s, ok := obj["name"].(string); ok {
			d.Name, d.NameSet = s, true
		}
		if b, ok := obj["enable"].(bool); ok {
			d.Enable, d.EnableSet = b, true
		}
		return d
	}

	// The new shape first: every host entry is shape-checked, and the
	// claudeSkill one becomes the declaration.
	if raw, ok := props[ExportsKey]; ok {
		exports, isObj := raw.(map[string]any)
		if !isObj {
			malformed = append(malformed, ExportsKey)
		} else {
			hosts := make([]string, 0, len(exports))
			for host := range exports {
				hosts = append(hosts, host)
			}
			sort.Strings(hosts) // deterministic malformed order
			for _, host := range hosts {
				path := ExportsKey + "." + host
				obj, isObj := exports[host].(map[string]any)
				if !isObj {
					malformed = append(malformed, path)
					continue
				}
				d := read(path, host, obj)
				if host == HostClaudeSkill {
					decl = d
				}
			}
		}
	}

	// Then the retired top-level keys, as aliases for the same host.
	for _, key := range legacyTopLevelKeys {
		raw, ok := props[key]
		if !ok {
			continue
		}
		obj, isObj := raw.(map[string]any)
		if !isObj {
			malformed = append(malformed, key)
			continue
		}
		d := read(key, HostClaudeSkill, obj)
		if decl == nil {
			decl = d
		}
	}
	return decl, malformed
}

// Declared reads a node's claudeSkill declaration out of its decoded
// properties. A declaration is an object at `exports.claudeSkill` or under a
// retired top-level alias; anything else — absent, or holding a non-object —
// is "not declared".
//
// Declared is NOT the same question as "will it export": `enable` defaults to
// off, so most declarations are declared and not enabled. Both states are
// returned here on purpose — lint judges a declaration whether or not it is
// enabled, because a broken name is worth reporting BEFORE somebody turns it on,
// and `status` has to be able to name a file whose declaration is switched off.
func Declared(props map[string]any) (*Declaration, bool) {
	decl, _ := classify(props)
	return decl, decl != nil
}

// Malformed reports the declaration property paths a node carries whose value
// is the wrong shape — `"exports": "yes"`, `"exports": {"claudeSkill": true}`,
// a non-string description, a non-boolean enable. Such a node is selected by
// the server-side discovery predicate (the key EXISTS), so without a finding
// it would be silently skipped and counted in nobody's total — an all-clear
// wider than the read that produced it.
func Malformed(props map[string]any) []string {
	_, bad := classify(props)
	return bad
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
// hex characters of SHA-256 over the five inputs the file is made from — the
// node ID, source node URN, name, description and body, in that order —
// NUL-separated so a boundary shift cannot collide. It is deliberately not nodedoc.ContentHash (content
// only): the description is the trigger, so an edit to it must read as
// stale; and the source is included so a hand-edited provenance line that
// names a different node reads as a local edit rather than pairing the file
// with a node it was never rendered from (Codex on #589, round 12). Every
// input is present in the file itself, which is what lets a reader recompute
// the hash from the file alone and detect a hand edit without the server.
//
// Inputs are hashed in their exported form (NormalizeDescription,
// NormalizeBody) — what Render writes and ParseFile reads back — so a node
// whose content is wrapped in blank lines and the file built from it
// fingerprint identically. Without that, every export would classify as
// stale against its own header.
// The node ID is hashed FIRST, and including it is the point of §4a
// (hadron-server#1235). Left out, editing ONLY the header id keeps
// hash(file) == headerHash, so the file reads as untouched while pairing to a
// DIFFERENT node — `stale` rather than `locally-edited`, and therefore
// overwritten despite A1. Pairing by id is what survives a `loc` rename:
// moveNode keeps the id while the URN is computed from the loc, so URN-only
// pairing turns a rename into orphaned + never-exported — two directories with
// near-identical trigger text, both firing.
//
// An EMPTY id is SKIPPED — it contributes no field and no separator, so the
// digest is byte-identical to the pre-§4a formula (Holger, 2026-09-22).
//
// This replaces "an empty id hashes as the empty string", which @Dara measured
// as doing the OPPOSITE of the rationale we both wrote for it. The rationale
// was right: a pre-§4a file must stay self-consistent, so that reading one back
// recomputes to its own stored header hash. Hashing "" defeats it, because
// prefixing `"" + \0` is not a no-op — a legacy file then mismatched, and
// `locally-edited` OUTRANKS every other class, so `export` would have REFUSED
// to touch exactly the files §4b's rollout has to rewrite. A1 would have been
// protecting work nobody did.
//
// Skipping delivers what the rationale promised: a legacy file recomputes to
// its own header, falls through to `nodeHash != headerHash`, classifies
// `stale`, and is rewritten by an ordinary export — gaining `id=` with no
// --force and no human in the loop. The server must do the same; the two
// implementations are gated against each other by the A2 parity fixtures.
func Hash(id, source, name, description, content string) string {
	description = NormalizeDescription(description)
	content = NormalizeBody(content)
	// An empty id is SKIPPED — it contributes neither a field nor a separator,
	// so the digest is byte-identical to the pre-§4a formula. That equality is
	// the whole point and it is asserted directly in
	// TestAnEmptyIdReproducesThePreSection4aDigest.
	payload := source + "\x00" + name + "\x00" + description + "\x00" + content
	if id != "" {
		payload = id + "\x00" + payload
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:16]
}

// Node is the slice of a Hadron node the skill contract reads. The command
// package fills it from a NodeBatch projection — the batched read, because a
// single-ref read compiles Mustache and would blank `{{name}}`-style
// placeholders out of a verbatim export (D6).
type Node struct {
	// ID is the node's stable id — the §4a pairing key, which survives the
	// `loc` rename that changes URN.
	ID         string
	URN        string
	Loc        string
	MemoryURN  string
	IsRunnable bool
	Content    string
	Properties map[string]any
}

// Finding is one lint result. URN names the node and Memory the memory holding
// it, carried on every finding so a renderer never has to look it back up from
// the node it came from.
//
// Every finding is now node-level. The memory-level case this used to describe
// was `skill-prefix-missing`, which D12 retired along with the org prefix — the
// command layer still synthesises one node-level finding of its own for an
// unreadable node (`skill-node-unavailable`), keyed on the ref it could not read.
type Finding struct {
	URN      string
	Memory   string
	Rule     string
	Severity string
	Message  string
}

// Lint applies the per-node corpus rules. A node with no declaration and no
// malformed key yields nothing: not declared is not a defect, it is the opt-in
// working. Since D12 the name is STORED rather than derived, so the rules judge
// what the author wrote — and note what can no longer be checked: with no
// prefix source, a name's PREFIX is unverifiable, so `hadon-foo` lints clean.
// Shape is checkable, correctness is not.
func Lint(n Node) []Finding {
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

	// D12 moved the declaration to `exports.<host>`; the top-level keys are read
	// as aliases so nothing needs migrating, and this steers them across.
	migrated := decl.Key == ExportsKey+"."+HostClaudeSkill
	var strays []string
	for _, key := range legacyTopLevelKeys {
		if _, ok := n.Properties[key].(map[string]any); ok {
			strays = append(strays, "properties."+key)
		}
	}
	switch {
	case !migrated:
		add("skill-legacy-key", SevWarning,
			fmt.Sprintf("declared under %s — move it to properties.%s.%s (D12: an object keyed by host, carrying {name, description, enable}); the old key is read as an alias only",
				"properties."+decl.Key, ExportsKey, HostClaudeSkill))
	case len(strays) > 0:
		// Migrated, but an alias was left behind: the export reads the new
		// shape, so the old key is dead weight the retirement will strand.
		add("skill-legacy-key", SevWarning,
			fmt.Sprintf("%s is still present beside properties.%s.%s — remove the retired key; the export reads only the new shape",
				strings.Join(strays, " and "), ExportsKey, HostClaudeSkill))
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

	// D12: the name is stored, so a missing one has nothing to fall back to.
	switch {
	case strings.TrimSpace(decl.Name) == "":
		add("skill-name-missing", SevError,
			fmt.Sprintf("properties.%s.name is missing or empty — since D12 the name is stored rather than derived, so there is nothing to fall back to; it is the skill's directory name and the host's identifier", decl.Key))
	case !ValidName(decl.Name):
		add("skill-name-invalid", SevError,
			fmt.Sprintf("skill name %q is not a valid skill name (kebab-case, ≤%d chars) — it is stored at properties.%s.name, so that is what to change", decl.Name, MaxNameLen, decl.Key))
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

// LintCollisions reports two declaring nodes that store the SAME skill name.
// Since D12 the name is stored rather than derived, so a collision is two
// authors having typed one name — there is no prefix left to keep two orgs'
// identically-named tasks apart, which is the cost D12 accepts. A node with no
// stored name is skipped: skill-name-missing is its finding.
func LintCollisions(nodes []Node) []Finding {
	byName := map[string][]Node{}
	for _, n := range nodes {
		decl, ok := Declared(n.Properties)
		if !ok || strings.TrimSpace(decl.Name) == "" {
			// A node with no stored name cannot collide with anything; its own
			// finding is skill-name-missing, not this.
			continue
		}
		byName[decl.Name] = append(byName[decl.Name], n)
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
				Message: fmt.Sprintf("stores skill name %q, and so does %s — one name, one skill directory; rename one or supersede the fork", name, strings.Join(others, ", ")),
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

// frontmatter is the header Render writes — exactly the two keys, in this
// order, so the file looks like every hand-written skill on disk. ParseFile
// decodes into a map instead, so it can refuse a non-string value and keep
// extra keys.
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
func Render(id, name, source, description, content string) (string, error) {
	description = NormalizeDescription(description)
	content = NormalizeBody(content)
	fm, err := nodedoc.MarshalYAML(frontmatter{Name: name, Description: description})
	if err != nil {
		return "", fmt.Errorf("rendering frontmatter: %w", err)
	}
	hash := Hash(id, source, name, description, content)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fm)
	b.WriteString("\n---\n\n")
	// `id=` is OMITTED when empty rather than written blank: the header
	// grammar requires a non-space value (`\w+=\S+`), so `id= ` would make the
	// line unparseable and demote a generated file to "somebody else's skill".
	// A reader then sees id == "", which is exactly what Hash fingerprinted.
	if id != "" {
		fmt.Fprintf(&b, "<!-- hadron-skill id=%s source=%s hash=%s -->\n", id, source, hash)
	} else {
		fmt.Fprintf(&b, "<!-- hadron-skill source=%s hash=%s -->\n", source, hash)
	}
	b.WriteString(humanLine + "\n\n")
	b.WriteString(content)
	b.WriteString("\n")
	return b.String(), nil
}

// File is what ParseFile reads back from a skill file on disk: the frontmatter
// keys a host reads, the provenance the header carries, and the body — enough
// to recompute the hash (Hash(ID, Source, Name, Description, Body)) and pair
// the file to its source node without the server.
//
// ID comes FIRST, matching Hash's argument order. The canonical description
// is Hash's own doc comment; this one restates the formula for a reader
// holding a File, so the two must agree — a stale copy here yields a hash that
// never matches, and every generated file then reads as locally edited, which
// is the opposite of what A1 is for. ID was added (§4a, server#1235) precisely
// so file-only recomputation remains possible.
type File struct {
	Name        string
	Description string
	// ID is the node's stable id from the header's `id=` key, or "" on a file
	// written before §4a. It is what pairs a file to its node across a `loc`
	// rename, and it is a Hash input — so a reader needs it to recompute the
	// fingerprint from the file alone.
	ID     string
	Source string // the flat v2 source node URN as written, or "" when the file is not Hadron-generated
	Hash   string // "" for a legacy (pre-#580) header
	Body   string
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
	// Decode into a map first so a non-string name/description is REFUSED
	// rather than coerced (yaml would happily read `name: 123` as "123",
	// and a hash over the coerced text would not be the host's view of the
	// file — Codex on #589, round 12).
	var all map[string]any
	if err := yaml.Unmarshal(m[1], &all); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	f := &File{}
	for k, v := range all {
		switch k {
		case "name", "description":
			str, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("frontmatter %s is not a string (%T)", k, v)
			}
			if k == "name" {
				f.Name = str
			} else {
				f.Description = NormalizeDescription(str)
			}
		default:
			if f.Extra == nil {
				f.Extra = map[string]any{}
			}
			f.Extra[k] = v
		}
	}
	preamble, body := splitPreamble(string(m[2]))
	f.ID, f.Source, f.Hash, _ = scanProvenance(preamble)
	f.Body = body
	return f, nil
}

// scanProvenance finds the provenance line in a preamble. ONE recognizer,
// shared by ParseFile and ParseProvenance, so a header spelling cannot be
// accepted by one reader and missed by the other.
func scanProvenance(preamble string) (id, source, hash string, ok bool) {
	for _, line := range strings.Split(preamble, "\n") { // raw, like splitPreamble
		if id, src, h, ok := machineHeader(line); ok {
			return id, src, h, true
		}
		if src, ok := legacyHeader(line); ok {
			return "", src, "", true
		}
	}
	return "", "", "", false
}

// ParseProvenance recovers a file's provenance WITHOUT requiring its
// frontmatter to be valid YAML — the one thing still knowable about a file
// that does not parse.
//
// It exists because the pairing key lives INSIDE the file that will not parse,
// and a parse failure the client cannot attribute is reported as an ORPHAN:
// a claim that a file belongs to no declared node, which is false and which is
// what `export --prune` deletes. Measured against the live corpus, three real
// skills (an export wrote an unquoted description containing ": ") took
// exactly that path. The frontmatter DELIMITERS still match when the YAML
// inside them does not, so the header below them is still readable.
//
// It answers only "whose file is this", never "is it current": no hash can be
// recomputed from a file whose frontmatter is unreadable, so a caller sends
// this alongside parseFailed and lets the server decline to classify it.
func ParseProvenance(data []byte) (id, source, hash string, ok bool) {
	data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
	m := frontmatterRE.FindSubmatch(data)
	if m == nil {
		return "", "", "", false
	}
	preamble, _ := splitPreamble(string(m[2]))
	return scanProvenance(preamble)
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
// `id` is OPTIONAL and unvalidated beyond being present: a file written before
// §4a has none, and rejecting a header for a missing id would orphan every
// skill exported to date. It is returned so a reader can recompute the hash
// from the FILE ALONE — the property the whole design rests on — which is no
// longer possible without it.
func machineHeader(t string) (id, source, hash string, ok bool) {
	h := headerRE.FindStringSubmatch(t)
	if h == nil {
		return "", "", "", false
	}
	for _, kv := range headerKV.FindAllStringSubmatch(h[1], -1) {
		switch kv[1] {
		case "id":
			id = kv[2]
		case "source":
			source = kv[2]
		case "hash":
			hash = kv[2]
		}
	}
	if !isNodeURN(source) || !hashRE.MatchString(hash) {
		return "", "", "", false
	}
	return id, source, hash, true
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

func isMachineHeader(t string) bool { _, _, _, ok := machineHeader(t); return ok }
func isLegacyHeader(t string) bool  { _, ok := legacyHeader(t); return ok }
