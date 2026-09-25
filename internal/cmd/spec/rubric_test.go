package spec

import (
	"strings"
	"testing"
)

// scaffoldNode assembles a specNode from the tier-aware templates exactly as
// `spec new` would, so the test can lint precisely what the command writes.
func scaffoldNode(c Citation, title string) specNode {
	abs := tierAbstract(c, title)
	body := tierBody(c, title)
	return specNode{
		Loc:         c.Format(),
		Name:        specName(c, title),
		NodeType:    "info",
		Tags:        specTags(nil),
		Abstract:    &abs,
		Content:     &body,
		DataVersion: specDataVersion,
	}
}

// #69 item 2: each tier scaffolds its own house shape, not one generic rubric.
func TestTierBodyShapes(t *testing.T) {
	cases := []struct {
		name string
		c    Citation
		want []string
	}{
		{"product-root", Citation{Product: "cli"}, []string{"# cli — T", "## Modules"}},
		{"module-root-flat", Citation{Module: "msg"}, []string{"## Features"}},
		{"module-root-product", Citation{Product: "cor", Module: "brd"}, []string{"## Features"}},
		{"product-contract", Citation{Product: "cli", Module: "gen"}, []string{"General provisions", "every module in `cli`", "What invalidates"}},
		{"module-contract", Citation{Module: "msg", Feature: "000"}, []string{"General provisions", "every feature in `msg`", "What invalidates"}},
		{"feature-root", Citation{Module: "msg", Feature: "010"}, []string{"load-bearing point", "## Rules"}},
		{"feature-contract", Citation{Module: "msg", Feature: "010", Rule: "00"}, []string{"General provisions", "every rule in `msg:010`", "What invalidates"}},
		{"rule", Citation{Module: "msg", Feature: "010", Rule: "02"}, []string{"## Definition", "## Scenarios / user stories", "As a <actor>", "## Rule & examples", "What invalidates this spec", "## Acceptance criteria"}},
		{"flow", Citation{Module: "msg", Feature: "010", Rule: "02", Flow: "03"}, []string{"## Definition"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tierBody(tc.c, "T")
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("%s body missing %q:\n%s", tc.name, w, body)
				}
			}
		})
	}
}

// The optional Scenarios/Acceptance sections (issue #217) appear only on the
// rule tier, placed after the definition and before the rule; the terser flow
// tier omits them. Being un-linted, they must not disturb the mandatory rubric a
// scaffold already passes.
func TestOptionalSectionsRuleTierOnly(t *testing.T) {
	rule := tierBody(Citation{Module: "msg", Feature: "010", Rule: "02"}, "T")
	defIdx := strings.Index(rule, "## "+headingDefinition)
	scnIdx := strings.Index(rule, "## "+headingScenarios)
	ruleIdx := strings.Index(rule, "## "+headingRule)
	if scnIdx < 0 || defIdx < 0 || ruleIdx < 0 {
		t.Fatalf("rule body missing a required heading:\n%s", rule)
	}
	if defIdx >= scnIdx || scnIdx >= ruleIdx {
		t.Errorf("scenarios must sit after definition, before rule (def=%d scn=%d rule=%d)", defIdx, scnIdx, ruleIdx)
	}
	if !strings.Contains(rule, "## "+headingAcceptance) {
		t.Errorf("rule body missing optional acceptance-criteria section:\n%s", rule)
	}

	flow := tierBody(Citation{Module: "msg", Feature: "010", Rule: "02", Flow: "03"}, "T")
	if strings.Contains(flow, headingScenarios) || strings.Contains(flow, headingAcceptance) {
		t.Errorf("flow body should stay terse — no optional sections:\n%s", flow)
	}
}

// A freshly scaffolded spec must pass its own structural lint at every tier:
// the scaffold owns the name prefix, nodeType and the "spec" tag. (Lint has no
// content rubric since #708, so there is nothing about sections to pass.)
func TestScaffoldPassesStructuralLint(t *testing.T) {
	structural := map[string]bool{"name-prefix": true, "nodetype-info": true, "tag-spec": true}
	for _, c := range []Citation{
		{Product: "cli"},
		{Module: "msg"},
		{Product: "cor", Module: "brd"},
		{Product: "cli", Module: "gen"},
		{Module: "msg", Feature: "000"},
		{Module: "msg", Feature: "010"},
		{Module: "msg", Feature: "010", Rule: "00"},
		{Module: "msg", Feature: "010", Rule: "02"},
	} {
		t.Run(c.Format(), func(t *testing.T) {
			for _, f := range lintNode(scaffoldNode(c, "Title"), "") {
				if structural[f.Rule] {
					t.Errorf("scaffold tripped structural lint %q: %s", f.Rule, f.Message)
				}
			}
		})
	}
}

// tierAbstract is tier-worded but always carries the placeholder marker, so a
// reader (and supersede's abstract copy) can tell it was never written.
func TestTierAbstractCarriesMarker(t *testing.T) {
	for _, c := range []Citation{
		{Product: "cli"},
		{Module: "msg"},
		{Module: "msg", Feature: "010"},
		{Module: "msg", Feature: "010", Rule: "00"},
		{Module: "msg", Feature: "010", Rule: "02"},
	} {
		if !strings.Contains(tierAbstract(c, "T"), abstractPlaceholder) {
			t.Errorf("%s abstract should carry the %q marker", c.Format(), abstractPlaceholder)
		}
	}
}
