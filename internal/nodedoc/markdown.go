package nodedoc

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// frontmatter is the YAML header for a node file. Field order matches
// hadron-server's buildNodeFrontmatter so the diff against a server push stays
// readable; omitempty encodes the omit-on-default rules. loc/memory are the two
// self-describing keys a standalone single-node file carries (a tree export
// encodes loc in the file path and memory in the sync target, so it omits
// them); they sit right after id and are ignored by the server's tree importer.
type frontmatter struct {
	Name               string      `yaml:"name"`
	ID                 string      `yaml:"id"`
	Loc                string      `yaml:"loc,omitempty"`
	Memory             string      `yaml:"memory,omitempty"`
	Alias              string      `yaml:"alias,omitempty"`
	Type               string      `yaml:"type,omitempty"`
	Description        string      `yaml:"description,omitempty"`
	Summary            string      `yaml:"summary,omitempty"`
	Abstract           string      `yaml:"abstract,omitempty"`
	AbstractOriginHash string      `yaml:"abstractOriginHash,omitempty"`
	ContentHash        string      `yaml:"contentHash,omitempty"`
	Tags               []string    `yaml:"tags,omitempty"`
	Seq                *int        `yaml:"seq,omitempty"`
	Data               any         `yaml:"data,omitempty"`
	Properties         any         `yaml:"properties,omitempty"`
	Nodes              []edgeEntry `yaml:"nodes,omitempty"`
}

// edgeEntry is one outgoing edge inside the frontmatter `nodes:` array. The
// importer keys off `id` and reads `rel` as the label; `loc` is carried for
// readability. condition/priority round-trip the edge's gating and order.
type edgeEntry struct {
	ID        string `yaml:"id"`
	Loc       string `yaml:"loc,omitempty"`
	Rel       string `yaml:"rel"`
	Condition any    `yaml:"condition,omitempty"`
	Priority  int    `yaml:"priority,omitempty"`
}

// frontmatterRE splits a node file into its YAML header and body, mirroring
// hadron-server's loadFrontmatterNode (`^---\n(...)\n---\n?(...)$`). The header
// match is non-greedy so the first `\n---\n` closes it; the body is whatever
// follows (trimmed by the caller).
var frontmatterRE = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?(.*)\z`)

// RenderMarkdown produces the full node file: YAML frontmatter, then the node
// content as the body, framed as `---\n<fm>\n---\n\n<body>\n` (hadron-server's
// pushMemoryToGit file shape). When standalone is true the file carries its own
// loc/memory keys so a lone file is self-describing and re-importable without
// flags; a tree export passes false (loc lives in the path, memory in the sync
// target).
func RenderMarkdown(doc *Document, standalone bool) (string, error) {
	fmYAML, err := MarshalYAML(buildFrontmatter(doc, standalone))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("---\n%s\n---\n\n%s\n", fmYAML, doc.Content), nil
}

// ParseMarkdown is the inverse of RenderMarkdown: it splits the frontmatter from
// the body, parses the YAML header, applies the `description ?? summary` legacy
// fallback, and trims the body. It accepts both a standalone single-node file
// (with loc/memory keys) and a tree-export file (without). A file with no `---`
// frontmatter is rejected.
func ParseMarkdown(data []byte) (*Document, error) {
	m := frontmatterRE.FindSubmatch(data)
	if m == nil {
		return nil, fmt.Errorf("not a node file: missing `---` frontmatter")
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	desc := fm.Description
	if desc == "" {
		desc = fm.Summary
	}
	return &Document{
		ID:                 fm.ID,
		MemoryURN:          fm.Memory,
		Loc:                fm.Loc,
		Name:               fm.Name,
		Type:               fm.Type,
		Alias:              fm.Alias,
		Description:        desc,
		Abstract:           fm.Abstract,
		AbstractOriginHash: fm.AbstractOriginHash,
		ContentHash:        fm.ContentHash,
		Tags:               fm.Tags,
		Seq:                fm.Seq,
		Data:               fm.Data,
		Properties:         fm.Properties,
		Content:            strings.TrimSpace(string(m[2])),
		Edges:              edgesFromEntries(fm.Nodes),
	}, nil
}

// buildFrontmatter projects a Document into the YAML header. type omits the
// server default `info` (and ""); contentHash is recomputed from content.
func buildFrontmatter(doc *Document, standalone bool) frontmatter {
	fm := frontmatter{
		Name:               doc.Name,
		ID:                 doc.ID,
		Alias:              doc.Alias,
		Description:        doc.Description,
		Abstract:           doc.Abstract,
		AbstractOriginHash: doc.AbstractOriginHash,
		ContentHash:        ContentHash(doc.Content),
		Tags:               doc.Tags,
		Seq:                doc.Seq,
		Data:               doc.Data,
		Properties:         doc.Properties,
		Nodes:              buildEdgeEntries(doc.Edges),
	}
	if standalone {
		fm.Loc = doc.Loc
		fm.Memory = doc.MemoryURN
	}
	if doc.Type != "" && doc.Type != "info" {
		fm.Type = doc.Type
	}
	return fm
}

// buildEdgeEntries projects edges into the inline `nodes:` array, matching the
// server's buildEdgeFrontmatter: id always, loc when set, rel (the label, empty
// string when blank), condition when present, priority when non-zero. An edge
// with no target id can't be addressed and is skipped. Returns nil when there
// are no entries so the `nodes:` key is omitted entirely.
func buildEdgeEntries(edges []Edge) []edgeEntry {
	out := make([]edgeEntry, 0, len(edges))
	for _, e := range edges {
		if e.TargetID == "" {
			continue
		}
		entry := edgeEntry{ID: e.TargetID, Loc: e.TargetLoc, Rel: e.Label, Condition: e.Condition}
		if e.Priority != 0 {
			entry.Priority = e.Priority
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// edgesFromEntries is the inverse of buildEdgeEntries. It always returns a
// non-nil slice so a Document's Edges field is stable ([] not null) across the
// codecs.
func edgesFromEntries(entries []edgeEntry) []Edge {
	out := make([]Edge, 0, len(entries))
	for _, e := range entries {
		out = append(out, Edge{
			TargetID:  e.ID,
			TargetLoc: e.Loc,
			Label:     e.Rel,
			Condition: e.Condition,
			Priority:  e.Priority,
		})
	}
	return out
}

// MarshalYAML encodes v as YAML with 2-space indents (matching the server's
// yaml writer) and trims the trailing newline so the caller controls the
// document framing.
func MarshalYAML(v any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
