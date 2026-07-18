package cmdutil

import "testing"

func TestCanonicalMemoryRef(t *testing.T) {
	cases := map[string]string{
		"acme.com:kb":             "hrn:mem:acme.com:kb", // single colon → canonical v2
		"acme.com::kb":            "hrn:mem:acme.com:kb", // legacy double colon → canonical v2
		"  acme.com:kb  ":         "hrn:mem:acme.com:kb", // trimmed
		"hrn:memory:acme.com::kb": "hrn:mem:acme.com:kb", // v1 URN → canonical v2
		"urn:memory:acme.com::kb": "hrn:mem:acme.com:kb", // legacy prefix normalizes
		"hrn:mem:acme.com:kb":     "hrn:mem:acme.com:kb", // already canonical v2
		"019f01ebcafef00dcafe":    "019f01ebcafef00dcafe", // raw id (no colon)
		"":                        "",
		// A node-URN-shaped ref (3+ parts) is NOT a memory ref — leave it alone.
		"acme.com:kb:findings": "acme.com:kb:findings",
		// Malformed 3+ colon separator must NOT collapse onto the real memory.
		"acme.com:::kb": "acme.com:::kb",
	}
	for in, want := range cases {
		if got := CanonicalMemoryRef(in); got != want {
			t.Errorf("CanonicalMemoryRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeURN(t *testing.T) {
	cases := []struct {
		memory, loc, want string
	}{
		{"acme.com:kb", "findings:x", "hrn:node:acme.com:kb:findings:x"},  // single-colon memory
		{"acme.com::kb", "findings:x", "hrn:node:acme.com:kb:findings:x"}, // legacy double-colon memory
		{"hrn:memory:acme.com::kb", "x", "hrn:node:acme.com:kb:x"},        // v1 prefixed memory
		{"hrn:mem:acme.com:kb", "x", "hrn:node:acme.com:kb:x"},            // v2 prefixed memory
		{"rawmemid", "x", ""},      // raw id → can't compose
		{"acme.com::kb", "", ""},   // empty loc → guarded
		{"foo::bar::baz", "x", ""}, // malformed multi-part → rejected
	}
	for _, tc := range cases {
		if got := NodeURN(tc.memory, tc.loc); got != tc.want {
			t.Errorf("NodeURN(%q, %q) = %q, want %q", tc.memory, tc.loc, got, tc.want)
		}
	}
}

func TestMemoryParts(t *testing.T) {
	type parts struct {
		root, slug string
		ok         bool
	}
	cases := map[string]parts{
		"acme.com:kb":             {"acme.com", "kb", true},
		"acme.com::kb":            {"acme.com", "kb", true}, // legacy "::" collapses
		"hrn:memory:acme.com::kb": {"acme.com", "kb", true},
		"urn:memory:acme.com:kb":  {"acme.com", "kb", true},
		"hrn:mem:acme.com:kb":     {"acme.com", "kb", true}, // canonical v2
		"rawid":                   {"", "", false},          // no separator
		"acme.com:kb:findings":    {"", "", false},          // 3 atoms → not a memory pair
		"acme.com:::kb":           {"", "", false},          // malformed separator
		"hrn:node:acme.com:kb:x":  {"", "", false},          // a non-memory URN
		"":                        {"", "", false},
	}
	for in, want := range cases {
		root, slug, ok := MemoryParts(in)
		if root != want.root || slug != want.slug || ok != want.ok {
			t.Errorf("MemoryParts(%q) = (%q, %q, %v), want (%q, %q, %v)", in, root, slug, ok, want.root, want.slug, want.ok)
		}
	}
}
