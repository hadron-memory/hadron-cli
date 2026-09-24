package spec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// #709: describe is a neutral inventory. Nothing is classified by depth, a
// corpus that mixes the old flat and product-rooted numberings is not
// flagged, and specs outside the legacy numbering are counted, not dropped.
func TestDescribeInventory(t *testing.T) {
	locs := []string{
		"msg", "msg:010", "msg:010:02", // legacy flat
		"cli:cha:010:01:02",                     // legacy product-rooted, the old maximum depth
		"onboarding:mentor",                     // outside the numbering
		"app:onb:010:02:screens:settings:empty", // outside, deeper than the old ceiling
	}
	got := describeInventory("hrn:mem:x:specs", locs)
	want := describeDTO{
		Memory:           "hrn:mem:x:specs",
		Specs:            6,
		Roots:            []string{"app", "cli", "msg", "onboarding"},
		MaxDepth:         7,
		LegacyNumbered:   4,
		OutsideNumbering: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inventory =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDescribeInventoryEmptyRendersRootsAsList(t *testing.T) {
	got := describeInventory("m", nil)
	b, _ := json.Marshal(got)
	if string(b) != `{"memory":"m","specs":0,"roots":[],"maxDepth":0,"legacyNumbered":0,"outsideNumbering":0}` {
		t.Errorf("empty inventory JSON = %s", b)
	}
}

// The retired declaration is read only to disclose it, leniently.
func TestSchemeFromDataDisclosesOnly(t *testing.T) {
	raw := func(s string) *json.RawMessage { r := json.RawMessage(s); return &r }
	for _, c := range []struct {
		data *json.RawMessage
		want string
	}{
		{nil, ""},
		{raw(`null`), ""},
		{raw(`{"spec":{"scheme":"product"},"other":1}`), "product"},
		{raw(`not json`), ""},
	} {
		if got := schemeFromData(c.data); got != c.want {
			t.Errorf("schemeFromData(%v) = %q, want %q", c.data, got, c.want)
		}
	}
}
