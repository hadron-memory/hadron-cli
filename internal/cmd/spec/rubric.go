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
	headingScenarios   = "Scenarios / user stories"
	headingRule        = "Rule & examples"
	headingDurable     = "Durable vs tunable"
	headingInvalidates = "What invalidates this spec"
	headingAcceptance  = "Acceptance criteria"
)

// abstractPlaceholder marks an un-filled abstract; lint flags any abstract
// that still contains it.
const abstractPlaceholder = "TODO(abstract):"

// specDataVersion is the schema version stamped into a new spec's data.
const specDataVersion = "0.0.1"

// placeholderAbstract is the stand-in abstract a scaffolded spec carries
// until the author writes a real one. The marker keeps lint reminding the
// author to replace it at the rule tier, where the abstract is the
// load-bearing vector-search retrieval surface.
func placeholderAbstract(c Citation, title string) string {
	return fmt.Sprintf("%s one paragraph stating what %s — %s governs and the durable contract a reader searches for; this is the vector-search retrieval surface. Replace before publishing.",
		abstractPlaceholder, c.Format(), title)
}

// tierAbstract returns a tier-worded placeholder abstract: an orientation
// stub for product/module roots, the feature's load-bearing point, the shared
// provisions for a contract, and the rule rubric's retrieval-surface stub for
// a rule or flow.
func tierAbstract(c Citation, title string) string {
	switch {
	case c.IsContract():
		parentStr := ""
		if p, ok := c.Parent(); ok {
			parentStr = " in " + p.Format()
		}
		return fmt.Sprintf("%s one paragraph stating the general provisions %s sets for every %s%s — the shared definitions and defaults a reader searches for. Replace before publishing.",
			abstractPlaceholder, c.Format(), tierChildWord(c), parentStr)
	case c.Level() == 0:
		return fmt.Sprintf("%s one paragraph orienting a reader to the %s product — the modules it spans and what to look for here. Replace before publishing.",
			abstractPlaceholder, c.Format())
	case c.Level() == 1:
		return fmt.Sprintf("%s one paragraph orienting a reader to the %s module — what it covers and where its features live. Replace before publishing.",
			abstractPlaceholder, c.Format())
	case c.Level() == 2:
		return fmt.Sprintf("%s one paragraph stating what feature %s — %s governs and its load-bearing point; this is the vector-search retrieval surface. Replace before publishing.",
			abstractPlaceholder, c.Format(), title)
	default: // rule / flow
		return placeholderAbstract(c, title)
	}
}

// tierBody returns the scaffolded body whose shape matches the citation's
// tier: an index for product/module roots, a child-list for a feature root, a
// general-provisions skeleton for a contract, and the full rubric for a rule
// or flow. Header tiers (level < 3) are exempt from the rubric in lint, so
// their skeletons are free-form; the feature `:00` contract is a rule-tier
// node and so keeps the mandatory "what invalidates" statement.
func tierBody(c Citation, title string) string {
	switch {
	case c.IsContract():
		return contractBody(c, title)
	case c.Level() == 0:
		return indexBody(c, title, "Modules",
			"One entry per module in this product; keep this index in sync as modules are added. Module codes are 3 lowercase letters, frozen once created.")
	case c.Level() == 1:
		return indexBody(c, title, "Features",
			"One entry per feature in this module; keep this index in sync. Features are numbered in tens (`010`, `020`, …), each a child node under this root.")
	case c.Level() == 2:
		return featureRootBody(c, title)
	default: // rule (3) / flow (4)
		return rubricBody(c, title)
	}
}

// rubricBody returns the scaffolded spec body: the title H1 plus the four
// mandatory sections, ready for the author to fill in. Used for rules and
// flows — the compliance-loadable tiers. Rule-tier scaffolds also carry two
// optional, un-linted sections — "Scenarios / user stories" (right after the
// definition, framing intent) and a trailing "Acceptance criteria" — that an
// author fills in where they clarify the contract and deletes otherwise (issue
// #217). Flows stay terse: they inherit their rule's scenarios and are pulled on
// demand, so they get only the mandatory rubric.
func rubricBody(c Citation, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", c.Format(), title)
	fmt.Fprintf(&b, "## %s\n\nOne-line definition of what this spec governs.\n\n", headingDefinition)
	if c.Level() == 3 {
		fmt.Fprintf(&b, "## %s *(optional — delete if it adds nothing)*\n\n3–7 short scenarios that explain who needs this and why. Prefer\n`As a <actor>, I want <capability>, so that <outcome>.`, or plain\n`Scenarios:` bullets for lower-level, multi-actor, or failure/recovery\nbehavior. Cover the happy path, key alternates, and identity/permission\nboundaries — not filler.\n\n", headingScenarios)
	}
	fmt.Fprintf(&b, "## %s\n\nState the rule precisely. Give concrete examples and edge cases.\n\n", headingRule)
	fmt.Fprintf(&b, "## %s\n\n**Durable:** the parts that, if changed, mean a different spec.\n**Tunable:** the parts that can change without invalidating this spec.\n\n", headingDurable)
	fmt.Fprintf(&b, "## %s\n\nThe specific changes that repeal or supersede this spec. (Mandatory.)\n", headingInvalidates)
	if c.Level() == 3 {
		fmt.Fprintf(&b, "\n## %s *(optional — include when the behavior must be testable)*\n\nConcrete, checkable statements engineering or QA can verify (one bullet\neach).\n", headingAcceptance)
	}
	return b.String()
}

// indexBody is the skeleton for a product or module root — a header H1 plus a
// table-of-contents section the author keeps in sync as children are added.
func indexBody(c Citation, title, heading, blurb string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", c.Format(), title)
	fmt.Fprintf(&b, "## %s\n\n%s\n", heading, blurb)
	return b.String()
}

// featureRootBody is the skeleton for a feature root: a one-line "load-bearing
// point" the rest of the feature turns on, plus a child-rule list.
func featureRootBody(c Citation, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", c.Format(), title)
	fmt.Fprintf(&b, "The load-bearing point of this feature in one or two sentences — what it governs and why it matters.\n\n")
	fmt.Fprintf(&b, "## Rules\n\nOne entry per rule (`:01`, `:02`, …) under this feature; keep this list in sync.\n")
	return b.String()
}

// contractBody is the skeleton for a reserved general-provisions contract
// (product `:gen`, module `:000`, feature `:00`). It names the tier whose
// siblings inherit it and keeps the "what invalidates" statement so the
// feature-`:00` contract — a rule-tier node — passes its own lint.
func contractBody(c Citation, title string) string {
	parentStr := ""
	if p, ok := c.Parent(); ok {
		parentStr = fmt.Sprintf(" in `%s`", p.Format())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", c.Format(), title)
	fmt.Fprintf(&b, "General provisions inherited by every %s%s. State the shared definitions, defaults, and rules here; a sibling overrides one only by saying so explicitly.\n\n", tierChildWord(c), parentStr)
	fmt.Fprintf(&b, "## Provisions\n\nState the shared rules and defaults.\n\n")
	fmt.Fprintf(&b, "## %s\n\nThe changes that repeal or supersede these general provisions. (Mandatory.)\n", headingInvalidates)
	return b.String()
}

// tierChildWord names the children that inherit a contract at this tier: a
// product `:gen` is inherited by modules, a module `:000` by features, a
// feature `:00` by rules.
func tierChildWord(c Citation) string {
	switch c.Level() {
	case 1:
		return "module"
	case 2:
		return "feature"
	default:
		return "rule"
	}
}

// specName builds the canonical node name "<citation> — <title>".
func specName(c Citation, title string) string {
	return c.Format() + " — " + title
}

// specTags returns the tag set for a spec: "spec" then any extra semantic
// tags (deduped, order-preserving).
func specTags(extra []string) []string {
	tags := []string{"spec"}
	seen := map[string]bool{"spec": true}
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
