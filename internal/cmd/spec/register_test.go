package spec

import "testing"

func TestBuildLedgerProductAware(t *testing.T) {
	locs := []string{"cli", "cli:gen", "cli:cha", "cli:cha:000", "cli:cha:010", "cli:cha:010:00", "cli:cha:010:01"}
	dto := buildLedgerDTO("m", locs, registerLedger{modules: map[string]bool{}, retired: map[string][]int{}})

	var mods []string
	for _, m := range dto.Modules {
		mods = append(mods, m.Module)
	}
	// cli:gen is the product contract, not a module; the bare product root cli
	// is skipped too. Only cli:cha is a module.
	if !equalStrings(mods, []string{"cli:cha"}) {
		t.Fatalf("ledger modules = %v, want [cli:cha]", mods)
	}
	// 010 is used and 000 is the reserved module contract, so the next free
	// feature is 020.
	if dto.Modules[0].NextFeature != "020" {
		t.Errorf("next feature = %q, want 020", dto.Modules[0].NextFeature)
	}
}
