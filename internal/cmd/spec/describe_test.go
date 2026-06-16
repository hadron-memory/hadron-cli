package spec

import "testing"

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
