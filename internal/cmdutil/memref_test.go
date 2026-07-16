package cmdutil

import "testing"

func TestCanonicalMemoryRef(t *testing.T) {
	cases := map[string]string{
		"acme.com:kb":             "hrn:memory:acme.com::kb", // single colon → canonical
		"acme.com::kb":            "hrn:memory:acme.com::kb", // double colon → canonical
		"  acme.com:kb  ":         "hrn:memory:acme.com::kb", // trimmed
		"hrn:memory:acme.com::kb": "hrn:memory:acme.com::kb", // already canonical
		"urn:memory:acme.com::kb": "hrn:memory:acme.com::kb", // legacy prefix normalizes
		"019f01ebcafef00dcafe":    "019f01ebcafef00dcafe",    // raw id (no colon)
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
		{"acme.com:kb", "findings:x", "hrn:node:acme.com::kb::findings:x"},  // single-colon memory
		{"acme.com::kb", "findings:x", "hrn:node:acme.com::kb::findings:x"}, // double-colon memory
		{"hrn:memory:acme.com::kb", "x", "hrn:node:acme.com::kb::x"},        // prefixed memory
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

func TestCanonicalOrgMemory(t *testing.T) {
	cases := map[string]string{
		"acme.com:kb":             "acme.com::kb",
		"acme.com::kb":            "acme.com::kb",
		"hrn:memory:acme.com::kb": "acme.com::kb",
		"urn:memory:acme.com:kb":  "acme.com::kb",
		"rawid":                   "rawid", // no colon → unchanged
	}
	for in, want := range cases {
		if got := canonicalOrgMemory(in); got != want {
			t.Errorf("canonicalOrgMemory(%q) = %q, want %q", in, got, want)
		}
	}
}
