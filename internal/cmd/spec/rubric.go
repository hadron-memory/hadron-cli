package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Canonical rubric section headings. Shared with the lint engine so a
// freshly scaffolded spec passes its own structural checks.
const (
	headingDefinition  = "Definition"
	headingRule        = "Rule & examples"
	headingDurable     = "Durable vs tunable"
	headingInvalidates = "What invalidates this spec"
)

// abstractPlaceholder marks an un-filled abstract; lint flags any abstract
// that still contains it.
const abstractPlaceholder = "TODO(abstract):"

// specDataVersion is the schema version stamped into a new spec's data.
const specDataVersion = "0.0.1"

// placeholderAbstract is the stand-in abstract a scaffolded spec carries
// until the author writes a real one.
func placeholderAbstract(c Citation, title string) string {
	return fmt.Sprintf("%s one paragraph stating what %s — %s governs and the durable contract a reader searches for; this is the vector-search retrieval surface. Replace before publishing.",
		abstractPlaceholder, c.Format(), title)
}

// rubricBody returns the scaffolded spec body: the title H1 plus the four
// mandatory sections, ready for the author to fill in.
func rubricBody(c Citation, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", c.Format(), title)
	fmt.Fprintf(&b, "## %s\n\nOne-line definition of what this spec governs.\n\n", headingDefinition)
	fmt.Fprintf(&b, "## %s\n\nState the rule precisely. Give concrete examples and edge cases.\n\n", headingRule)
	fmt.Fprintf(&b, "## %s\n\n**Durable:** the parts that, if changed, mean a different spec.\n**Tunable:** the parts that can change without invalidating this spec.\n\n", headingDurable)
	fmt.Fprintf(&b, "## %s\n\nThe specific changes that repeal or supersede this spec. (Mandatory.)\n", headingInvalidates)
	return b.String()
}

// specName builds the canonical node name "<citation> — <title>".
func specName(c Citation, title string) string {
	return c.Format() + " — " + title
}

// specTags returns the tag set for a spec: "spec", the p-level, then any
// extra semantic tags (deduped, order-preserving).
func specTags(plevel int, extra []string) []string {
	plevelTag := fmt.Sprintf("p%d", plevel)
	tags := []string{"spec", plevelTag}
	seen := map[string]bool{"spec": true, plevelTag: true}
	for _, t := range extra {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	return tags
}

// specDataRaw returns the data JSON for a new spec ({"version":"0.0.1"}).
func specDataRaw() *json.RawMessage {
	raw := json.RawMessage(fmt.Sprintf(`{"version":%q}`, specDataVersion))
	return &raw
}
