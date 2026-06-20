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
		{"rule", Citation{Module: "msg", Feature: "010", Rule: "02"}, []string{"## Definition", "## Rule & examples", "What invalidates this spec"}},
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

// A freshly scaffolded spec must pass its own structural lint at every tier:
// the scaffold owns loc-shape, name prefix, nodeType, the "spec" tag,
// data.version, and — where lint requires it (rule-tier, incl. the feature
// `:00` contract) — the "what invalidates" statement. The placeholder abstract
// is deliberately flagged at the rule tier (the author must replace it) and the
// table-of-contents edge is wired by the command, not the body, so neither is
// asserted here.
func TestScaffoldPassesStructuralLint(t *testing.T) {
	structural := map[string]bool{
		"loc-shape": true, "name-prefix": true, "nodetype-info": true,
		"invalidates": true, "tag-spec": true, "data-version": true,
	}
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
			for _, f := range lintNode(scaffoldNode(c, "Title")) {
				if structural[f.Rule] {
					t.Errorf("scaffold tripped structural lint %q: %s", f.Rule, f.Message)
				}
			}
		})
	}
}

// tierAbstract is tier-worded but always carries the placeholder marker so lint
// keeps reminding the author to replace it where it's load-bearing.
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
