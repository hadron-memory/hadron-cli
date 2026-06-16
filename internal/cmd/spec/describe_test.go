package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDescribeSchemeFlat(t *testing.T) {
	locs := []string{"msg", "msg:010", "msg:010:00", "msg:010:01", "mat", "mat:010", "mat:010:01"}
	d := describeScheme("micromentor.org::platform-specs", locs)
	if d.Scheme != "flat" {
		t.Errorf("scheme = %q, want flat", d.Scheme)
	}
	if !equalStrings(d.Modules, []string{"mat", "msg"}) {
		t.Errorf("modules = %v, want [mat msg]", d.Modules)
	}
	if len(d.Products) != 0 {
		t.Errorf("flat corpus has no products, got %v", d.Products)
	}
	if d.Contracts.Product != "" || d.Contracts.Module != "000" || d.Contracts.Feature != "00" {
		t.Errorf("contracts = %+v", d.Contracts)
	}
	if d.Counts.Contracts != 1 { // msg:010:00 only
		t.Errorf("contracts count = %d, want 1", d.Counts.Contracts)
	}
}

func TestDescribeSchemeProduct(t *testing.T) {
	locs := []string{"cli", "cli:gen", "cli:cha", "cli:cha:000", "cli:cha:010", "cli:cha:010:01", "srv", "srv:gql"}
	d := describeScheme("hadronmemory.com::platform-specs", locs)
	if d.Scheme != "product" {
		t.Errorf("scheme = %q, want product", d.Scheme)
	}
	if !equalStrings(d.Products, []string{"cli", "srv"}) {
		t.Errorf("products = %v, want [cli srv]", d.Products)
	}
	if !equalStrings(d.Modules, []string{"cli:cha", "srv:gql"}) {
		t.Errorf("modules = %v, want [cli:cha srv:gql] (cli:gen is a contract, not a module)", d.Modules)
	}
	if d.Contracts.Product != "gen" {
		t.Errorf("product contract = %q, want gen", d.Contracts.Product)
	}
	if d.Counts.Contracts != 2 { // cli:gen (product) + cli:cha:000 (module)
		t.Errorf("contracts count = %d, want 2", d.Counts.Contracts)
	}
}

func TestApplyDeclared(t *testing.T) {
	// An empty corpus + a declaration: the declaration wins, no drift, and the
	// product contract code is surfaced.
	d := describeScheme("m", nil)
	applyDeclared(&d, "product")
	if d.Scheme != "product" || d.Source != "declared" || d.Declared != "product" {
		t.Errorf("declared overlay = %+v", d)
	}
	if d.Contracts.Product != "gen" {
		t.Errorf("declared product should surface the product contract code, got %q", d.Contracts.Product)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("empty corpus + declaration = no drift, got %v", d.Warnings)
	}
	// Declared product but the live nodes look flat → drift warning.
	flat := describeScheme("m", []string{"msg:010", "msg:010:01"})
	applyDeclared(&flat, "product")
	if flat.Scheme != "product" {
		t.Errorf("declaration should win, got %q", flat.Scheme)
	}
	var drift bool
	for _, w := range flat.Warnings {
		if strings.Contains(w, "declared") {
			drift = true
		}
	}
	if !drift {
		t.Errorf("expected a drift warning, got %v", flat.Warnings)
	}
}

func TestSchemeData(t *testing.T) {
	if s := schemeFromData(nil); s != "" {
		t.Errorf("nil data scheme = %q", s)
	}
	raw := json.RawMessage(`{"spec":{"scheme":"product"},"other":1}`)
	if s := schemeFromData(&raw); s != "product" {
		t.Errorf("scheme = %q, want product", s)
	}
	// Merging a new scheme updates it but preserves other keys.
	merged, err := withScheme(&raw, "flat")
	if err != nil {
		t.Fatalf("withScheme: %v", err)
	}
	if schemeFromData(&merged) != "flat" {
		t.Errorf("merged scheme not updated: %s", merged)
	}
	if !strings.Contains(string(merged), `"other"`) {
		t.Errorf("merge dropped sibling keys: %s", merged)
	}
}

func TestDescribeSchemeMixedAndEmpty(t *testing.T) {
	if d := describeScheme("m", nil); d.Scheme != "empty" {
		t.Errorf("empty scheme = %q", d.Scheme)
	}
	mixed := describeScheme("m", []string{"msg:010", "cli:cha"})
	if mixed.Scheme != "mixed" {
		t.Errorf("mixed scheme = %q", mixed.Scheme)
	}
	if len(mixed.Warnings) == 0 {
		t.Error("mixed corpus should carry a warning")
	}
}
