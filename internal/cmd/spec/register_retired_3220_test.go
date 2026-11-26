package spec

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// cli#3220 item 2: the register's human `rules:` list for a withdrawn and a rescinded
// rule, end to end: the replacement state a listing reads, the `retirement` word the
// DTO carries (ruleLabels reads it back through kindOfWord), the JSON the DTO marshals,
// and the label a reader of that JSON-decoded ledger sees. A kind dropped on either
// side turns "(withdrawn)" or "(rescinded)" into the neutral "(retired)".
func TestRegisterLabelsWithdrawnAndRescindedRulesEndToEnd3220(t *testing.T) {
	locs := []string{"cli", "cli:cha", "cli:cha:010", "cli:cha:010:01", "cli:cha:010:02", "cli:cha:010:03", "cli:cha:010:04", "cli:cha:010:05", "cli:cha:010:06"}
	dto := buildLedgerDTO("m", locs, registerLedger{modules: map[string]bool{}, retired: map[string][]int{}})
	successor := "cli:cha:010:02"
	cands := []replacedCandidate{
		{ID: "n1", Loc: "cli:cha:010:01"}, {ID: "n2", Loc: "cli:cha:010:02"}, {ID: "n3", Loc: "cli:cha:010:03"},
		{ID: "n4", Loc: "cli:cha:010:04"}, {ID: "n5", Loc: "cli:cha:010:05"}, {ID: "n6", Loc: "cli:cha:010:06"},
	}
	repl := map[string]replacement{
		"n1": {Replaced: true, By: &successor},
		"n2": {Replaced: true, Kind: rescindKind("WITHDRAWN (2026-11-20); no longer needed")},
		"n3": {Replaced: true, Kind: rescindKind("RESCINDED (2026-11-21); wrong rule")},
		"n4": {Replaced: true},
		// cli#3250 item 1: a rule with BOTH a readable successor and a rescind prefix reads as
		// replaced, through applyReplacements, the JSON round trip and ruleLabels.
		"n6": {Replaced: true, By: &successor, Kind: rescindKind("WITHDRAWN (2026-11-22); also withdrawn")},
	}
	applyReplacements(&dto, cands, repl)

	want := []string{"01 (replaced by 02)", "02 (withdrawn)", "03 (rescinded)", "04 (retired)", "05", "06 (replaced by 02)"}
	labels := func(d ledgerDTO) []string {
		for _, m := range d.Modules {
			for _, fe := range m.Features {
				if fe.Feature == "010" {
					return ruleLabels(m.Module+":"+fe.Feature, fe)
				}
			}
		}
		t.Fatalf("no cha:010 feature in %+v", d.Modules)
		return nil
	}
	if got := labels(dto); !slices.Equal(got, want) {
		t.Errorf("human rules = %v, want %v", got, want)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var round ledgerDTO
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if got := labels(round); !slices.Equal(got, want) {
		t.Errorf("human rules after a JSON round trip = %v, want %v", got, want)
	}
	words := map[string]string{}
	for _, m := range round.Modules {
		for _, fe := range m.Features {
			for _, r := range fe.ReplacedRules {
				words[r.Rule] = r.Retirement
			}
		}
	}
	if !reflect.DeepEqual(words, map[string]string{"01": "replaced", "02": "withdrawn", "03": "rescinded", "04": "retired", "06": "replaced"}) {
		t.Errorf("retirement words = %v", words)
	}
}

// A spec that was not replaced has no retirement word, whatever else the value holds;
// a readable successor wins over a rescind prefix (cli#3220 items 3 and 4).
func TestReplacementWordIsEmptyUnlessReplaced3220(t *testing.T) {
	by := "cli:cha:010:02"
	for _, tc := range []struct {
		name string
		r    replacement
		want string
	}{
		{"live", replacement{}, ""},
		{"live with stray kind", replacement{Kind: retirementWithdrawn}, ""},
		{"live with stray successor", replacement{By: &by}, ""},
		{"replaced", replacement{Replaced: true, By: &by}, "replaced"},
		{"successor beats a rescind prefix", replacement{Replaced: true, By: &by, Kind: retirementRescinded}, "replaced"},
		{"withdrawn", replacement{Replaced: true, Kind: retirementWithdrawn}, "withdrawn"},
		{"rescinded", replacement{Replaced: true, Kind: retirementRescinded}, "rescinded"},
		{"retired", replacement{Replaced: true}, "retired"},
	} {
		if got := tc.r.word(); got != tc.want {
			t.Errorf("%s: word() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
