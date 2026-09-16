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
	"strings"
)

// Host limits for a Claude Code skill, per the skill spec (verified against the
// official skill-creator validator, quick_validate.py). The host does not
// REFUSE an over-long description — it truncates it in the skill listing, so
// the trigger phrases past the cut silently stop firing. That is why the
// description rule is an error rather than a warning.
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
	headerRE = regexp.MustCompile(`(?m)^<!--\s*hadron-skill\s+([^>]*?)\s*-->`)
	headerKV = regexp.MustCompile(`(\w+)=(\S+)`)
	// legacyHeaderRE reads the pre-#580 provenance comment written by the
	// hand-run export procedure: `<!-- Generated from <urn> -->`. It carries a
	// source but no hash, so a file with only this line classifies as unhashed.
	legacyHeaderRE = regexp.MustCompile(`(?m)^<!--\s*Generated from\s+(\S+)\s*-->`)
	// frontmatterRE splits a skill file into its YAML header and body.
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

// Declared reads a node's skill declaration out of its decoded properties.
// A declaration is an object under one of the two keys; anything else — the
// key absent, or holding a non-object — is "not declared". The new key wins
// when both are present, so a migrated node whose legacy key was left behind
// behaves as migrated.
func Declared(props map[string]any) (*Declaration, bool) {
	for _, key := range []string{"skill", "claudeSkill"} {
		raw, ok := props[key]
		if !ok {
			continue
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		d := &Declaration{Key: key}
		if s, ok := obj["description"].(string); ok {
			d.Description = s
		}
		if s, ok := obj["name"].(string); ok {
			d.Name = s
		}
		return d, true
	}
	return nil, false
}

// Malformed reports whether a node carries a declaration key whose value is
// not an object — `"skill": "yes"`, `"claudeSkill": true`. Such a node is
// selected by the server-side discovery predicate (the key EXISTS) yet reads
// as "not declared" to Declared, so without this it would be silently skipped
// and counted in nobody's total — an all-clear wider than the read that
// produced it (review:a-claim-must-not-outrun-its-evidence). It returns the
// offending key so the finding can name it.
func Malformed(props map[string]any) (string, bool) {
	for _, key := range []string{"skill", "claudeSkill"} {
		raw, ok := props[key]
		if !ok {
			continue
		}
		if _, isObj := raw.(map[string]any); !isObj {
			return key, true
		}
	}
	return "", false
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
	return len(name) <= MaxNameLen && nameRE.MatchString(name)
}

// Hash is the provenance fingerprint a generated file records: the first 16
// hex characters of SHA-256 over the three inputs the file is made from —
// name, description and body — NUL-separated so a boundary shift cannot
// collide. It is deliberately not nodedoc.ContentHash (content only): the
// description is the trigger, so an edit to it must read as stale. Every
// input is present in the file itself, which is what lets a reader recompute
// the hash from the file alone and detect a hand edit without the server.
//
// The body is hashed with trailing newlines trimmed — the form Render writes
// and ParseFile reads back — so a node whose content ends in "\n" and the file
// built from it fingerprint identically. Without that, every export would
// classify as stale against its own header.
func Hash(name, description, content string) string {
	content = strings.TrimRight(content, "\n")
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

// Finding is one lint result against one node.
type Finding struct {
	URN      string
	Rule     string
	Severity string
	Message  string
}

// Lint applies the per-node corpus rules to a declaring node under the export
// prefix its memory resolves to (empty when the org has none — the command
// reports that separately, and the name rules then run on the bare slug so
// the rest of the report is still useful). A node with no declaration yields
// no findings: not declared is not a defect, it is the opt-in working.
func Lint(n Node, prefix string) []Finding {
	var out []Finding
	add := func(rule, sev, msg string) {
		out = append(out, Finding{URN: n.URN, Rule: rule, Severity: sev, Message: msg})
	}
	decl, ok := Declared(n.Properties)
	if !ok {
		if key, bad := Malformed(n.Properties); bad {
			add("skill-declaration-malformed", SevError,
				fmt.Sprintf("properties.%s is present but is not an object — a declaration is {\"description\": …}; fix it or remove the key", key))
		}
		return out
	}

	if decl.Key == "claudeSkill" {
		add("skill-legacy-key", SevWarning,
			"declared under properties.claudeSkill — move it to properties.skill (the provider-neutral key); claudeSkill is read as an alias during transition only")
	}

	desc := strings.TrimSpace(decl.Description)
	switch {
	case desc == "":
		add("skill-description-missing", SevError,
			fmt.Sprintf("properties.%s.description is missing or empty — it is the trigger text the host matches against; the node cannot be exported without it", decl.Key))
	case len(desc) > MaxDescriptionLen:
		add("skill-description-too-long", SevError,
			fmt.Sprintf("description is %d characters; the host caps it at %d and TRUNCATES the rest in the skill listing, so trigger phrases past the cut never fire — shorten by %d",
				len(desc), MaxDescriptionLen, len(desc)-MaxDescriptionLen))
	}
	if desc != "" && !triggerRE.MatchString(desc) {
		add("skill-description-no-trigger", SevWarning,
			"description never says when to use the skill (\"Use when …\") — the host matches descriptions against what the user says, and one with no trigger phrasing rarely fires")
	}

	name := DeriveName(prefix, n.Loc)
	if !ValidName(name) {
		add("skill-name-invalid", SevError,
			fmt.Sprintf("derived skill name %q is not a valid skill name (kebab-case, ≤%d chars) — it is derived from the loc, so the loc is what to change", name, MaxNameLen))
	}
	if decl.Name != "" && decl.Name != name {
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
	case strings.HasPrefix(body, "---\n"):
		add("skill-content-has-frontmatter", SevError,
			"content starts with a `---` frontmatter block — the node body is the skill BODY; frontmatter is composed at export from the declaration and would be doubled")
	}
	if templateRE.MatchString(n.Content) {
		add("skill-content-has-template", SevWarning,
			"content contains a {{…}} placeholder — export is verbatim, so it ships as literal text; keep it only if the skill is meant to carry a template")
	}
	return out
}

// LintCollisions applies the one corpus-level rule: two declaring nodes may
// not derive the same skill name, because the second export would silently
// win. prefixes maps a node's MemoryURN to its resolved prefix. Both members
// of a collision are reported, each naming the other, so whichever the reader
// opens explains itself.
//
// A node whose memory has NO resolved prefix (empty) is skipped: its name
// cannot be derived, so it cannot collide — and comparing bare slugs would
// report two prefix-less ORGS as colliding on `create-release-tag` when the
// prefixes they have yet to choose are exactly what keeps them apart
// (measured on the first live `--all` run: marketrailz vs micromentor.org).
// Those nodes already carry the prefix-missing finding.
func LintCollisions(nodes []Node, prefixes map[string]string) []Finding {
	byName := map[string][]Node{}
	for _, n := range nodes {
		if _, ok := Declared(n.Properties); !ok {
			continue
		}
		prefix := prefixes[n.MemoryURN]
		if prefix == "" {
			continue
		}
		name := DeriveName(prefix, n.Loc)
		byName[name] = append(byName[name], n)
	}
	var out []Finding
	for name, group := range byName {
		if len(group) < 2 {
			continue
		}
		for _, n := range group {
			others := make([]string, 0, len(group)-1)
			for _, o := range group {
				if o.URN != n.URN {
					others = append(others, o.URN)
				}
			}
			out = append(out, Finding{
				URN: n.URN, Rule: "skill-name-collision", Severity: SevError,
				Message: fmt.Sprintf("derives skill name %q, and so does %s — one name, one task; supersede the fork or rename a loc", name, strings.Join(others, ", ")),
			})
		}
	}
	return out
}

// Render composes the skill file (§4.3): YAML frontmatter with the two keys a
// host reads, the machine-parseable provenance line, the human note, and the
// node body verbatim. The frontmatter is emitted by hand rather than through
// nodedoc's codec because that codec's header is the node round-trip shape
// (loc, memory, tags, edges…), and a host reading unknown keys is a risk this
// artifact does not need.
func Render(name, source, description, content string) string {
	hash := Hash(name, description, content)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	b.WriteString("description: " + yamlScalar(description) + "\n")
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "<!-- hadron-skill source=%s hash=%s -->\n", source, hash)
	b.WriteString("<!-- Generated by `hadron skill export`. Edit the source node and re-export; do not edit this file. -->\n\n")
	b.WriteString(strings.TrimRight(content, "\n"))
	b.WriteString("\n")
	return b.String()
}

// yamlScalar renders a description as a YAML plain scalar when it is safe as
// one, and as a double-quoted scalar otherwise. Descriptions are one line of
// prose; the characters that would make a plain scalar mis-parse (a leading
// indicator, a `: ` or ` #` inside, a newline) are the ones quoted.
func yamlScalar(s string) string {
	safe := s != "" &&
		!strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@` ") &&
		!strings.Contains(s, ": ") && !strings.Contains(s, " #") &&
		!strings.ContainsAny(s, "\n\r\t")
	if safe {
		return s
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
	return `"` + q + `"`
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

// ParseFile reads a skill file. A file with no frontmatter is an error; a file
// with no provenance header parses with Source == "" — it is somebody else's
// skill, and every `hadron skill` command leaves it alone. The legacy
// `Generated from` header yields a Source and no Hash.
func ParseFile(data []byte) (*File, error) {
	m := frontmatterRE.FindSubmatch(data)
	if m == nil {
		return nil, fmt.Errorf("no frontmatter")
	}
	f := &File{}
	for _, line := range strings.Split(string(m[1]), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			f.Name = v
		case "description":
			f.Description = unquoteYAML(v)
		}
	}
	rest := string(m[2])
	if h := headerRE.FindStringSubmatch(rest); h != nil {
		for _, kv := range headerKV.FindAllStringSubmatch(h[1], -1) {
			switch kv[1] {
			case "source":
				f.Source = kv[2]
			case "hash":
				f.Hash = kv[2]
			}
		}
	} else if h := legacyHeaderRE.FindStringSubmatch(rest); h != nil {
		f.Source = h[1]
	}
	f.Body = bodyAfterHeader(rest)
	return f, nil
}

// bodyAfterHeader strips the provenance comment lines and surrounding blank
// lines so Body is exactly what Render was given as content — which is what
// makes Hash(file.Name, file.Description, file.Body) comparable to the header.
func bodyAfterHeader(rest string) string {
	lines := strings.Split(rest, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || (strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->") &&
			(strings.Contains(t, "hadron-skill") || strings.Contains(t, "Generated from") || strings.Contains(t, "Generated by") || strings.Contains(t, "Edit the source node"))) {
			i++
			continue
		}
		break
	}
	return strings.TrimRight(strings.Join(lines[i:], "\n"), "\n")
}

// unquoteYAML undoes yamlScalar's double-quoting; a plain scalar is returned
// as-is. Only the escapes yamlScalar emits are interpreted.
func unquoteYAML(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(inner)
	}
	return v
}
