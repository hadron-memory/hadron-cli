package intakecontract

import (
	"errors"
	"strings"
	"testing"
)

// The bounds are the published contract: changing one is a contract change
// (hadron-cli#1977 out of scope), so the literals are pinned here.
func TestBoundsArePinned(t *testing.T) {
	for name, c := range map[string][2]int{
		"ContractVersion":     {ContractVersion, 1},
		"MaxOutputBytes":      {MaxOutputBytes, 1 << 20},
		"MaxStderrBytes":      {MaxStderrBytes, 4096},
		"MaxItemsPerPage":     {MaxItemsPerPage, 100},
		"MaxPages":            {MaxPages, 50},
		"ServerExternalIDMax": {ServerExternalIDMax, 512},
		"MaxExternalIDLen":    {MaxExternalIDLen, 2048},
		"MaxCursorLen":        {MaxCursorLen, 4096},
		"MaxTextLen":          {MaxTextLen, 1024},
	} {
		if c[0] != c[1] {
			t.Errorf("%s = %d, pinned %d", name, c[0], c[1])
		}
	}
}

func TestCheckDescribe(t *testing.T) {
	ok := Describe{Name: "a", ContractVersion: 1}
	if err := CheckDescribe(ok); err != nil {
		t.Fatal(err)
	}
	var skew *SkewError
	if err := CheckDescribe(Describe{Name: "a", ContractVersion: 2}); !errors.As(err, &skew) || skew.Got != 2 {
		t.Fatalf("want SkewError{2}, got %v", err)
	}
	if err := CheckDescribe(Describe{Name: "a"}); !errors.As(err, &skew) {
		t.Fatalf("missing version is skew: %v", err)
	}
	if CheckDescribe(Describe{Name: "  ", ContractVersion: 1}) == nil {
		t.Error("blank name accepted")
	}
	if CheckDescribe(Describe{Name: strings.Repeat("a", MaxTextLen), ContractVersion: 1}) != nil {
		t.Error("name at the bound refused")
	}
	if CheckDescribe(Describe{Name: strings.Repeat("a", MaxTextLen+1), ContractVersion: 1}) == nil {
		t.Error("name over the bound accepted")
	}
}

func TestCheckPageBounds(t *testing.T) {
	item := func(id, title string) []Item { return []Item{{ExternalID: id, Title: title}} }
	cur := func(n int) *string { s := strings.Repeat("c", n); return &s }
	items := func(n int) []Item {
		out := make([]Item, n)
		for i := range out {
			out[i] = Item{ExternalID: "u" + strings.Repeat("x", i%7) + string(rune('a'+i%26)), Title: "t"}
		}
		return out
	}
	cases := []struct {
		name string
		page ListAnswer
		ok   bool
	}{
		{"empty page", ListAnswer{}, true},
		{"100 items", ListAnswer{Items: items(100)}, true},
		{"101 items", ListAnswer{Items: items(101)}, false},
		{"no externalId", ListAnswer{Items: item("", "t")}, false},
		{"id 512 code points", ListAnswer{Items: item(strings.Repeat("é", 512), "t")}, true},
		{"id 513 code points", ListAnswer{Items: item(strings.Repeat("é", 513), "t")}, false},
		{"id 2049 bytes", ListAnswer{Items: item(strings.Repeat("a", MaxExternalIDLen+1), "t")}, false},
		{"title at bound", ListAnswer{Items: item("u", strings.Repeat("t", MaxTextLen))}, true},
		{"title over bound", ListAnswer{Items: item("u", strings.Repeat("t", MaxTextLen+1))}, false},
		{"cursor at bound", ListAnswer{NextCursor: cur(MaxCursorLen)}, true},
		{"cursor over bound", ListAnswer{NextCursor: cur(MaxCursorLen + 1)}, false},
	}
	for _, c := range cases {
		if err := CheckPage(c.page); (err == nil) != c.ok {
			t.Errorf("%s: ok=%v, err=%v", c.name, c.ok, err)
		}
	}
}

func TestDecodeAnswer(t *testing.T) {
	var a ListAnswer
	for _, bad := range []string{"", "hello", "[]", `{"items":[]}{"items":[]}`, `{"items":[]} junk`, `{"items":[{"externalId":"u"`, "{\"items\":[{\"externalId\":\"\xff\"}]}"} {
		if DecodeAnswer([]byte(bad), &a) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if DecodeAnswer([]byte("  {\"items\":[]}\n"), &a) != nil {
		t.Error("refused one object with surrounding whitespace")
	}
	if DecodeAnswer([]byte("{\"items\":[],\"x\":\""+strings.Repeat("a", MaxOutputBytes)+"\"}"), &a) == nil {
		t.Error("accepted stdout over the cap")
	}
}
