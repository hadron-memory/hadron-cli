package access

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The suggested example must compose with the separator the caller used, so a
// single-colon shorthand isn't handed back a "::"-shaped v1 prefix.
func TestMemoryURNExampleMatchesSeparator(t *testing.T) {
	cases := []struct{ in, want string }{
		{"acme.com:kb", "hrn:mem:acme.com:kb"},      // v2 short form → canonical prefix
		{"acme.com::kb", "hrn:memory:acme.com::kb"}, // v1 short form → legacy prefix
		{"acme.com::kb::start-here", "hrn:memory:acme.com::kb::start-here"},
	}
	for _, tc := range cases {
		if got := memoryURNExample(tc.in); got != tc.want {
			t.Errorf("memoryURNExample(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeResourceRef(t *testing.T) {
	// Any scheme-prefixed ref passes through verbatim — the server dispatches
	// on its type, and it accepts v2 and legacy spellings alike.
	for _, ref := range []string{
		"hrn:mem:acme.com:kb",
		"hrn:memory:acme.com::kb",
		"hrn:node:acme.com:kb:start-here",
		"hrn:node:acme.com::kb::start-here",
		"hrn:app:acme.com::support",
		"urn:memory:acme.com::kb",
	} {
		got, err := normalizeResourceRef(ref)
		if err != nil {
			t.Errorf("%q should pass through, got error %v", ref, err)
		}
		if got != ref {
			t.Errorf("%q was rewritten to %q", ref, got)
		}
	}

	// A colon-free id is an AiServiceConfig id.
	if got, err := normalizeResourceRef("cfg_123"); err != nil || got != "cfg_123" {
		t.Errorf("bare id: got %q, %v", got, err)
	}

	// An under-qualified shorthand is rejected with guidance that composes.
	for _, ref := range []string{"acme.com:kb", "acme.com::kb"} {
		_, err := normalizeResourceRef(ref)
		if err == nil {
			t.Fatalf("%q should be rejected", ref)
		}
		if got := exitcode.FromError(err); got != exitcode.Usage {
			t.Errorf("%q should be a usage error, got %d", ref, got)
		}
		if !strings.Contains(err.Error(), memoryURNExample(ref)) {
			t.Errorf("%q: the error should suggest %q, got %q", ref, memoryURNExample(ref), err.Error())
		}
	}

	if _, err := normalizeResourceRef("   "); err == nil {
		t.Error("an empty ref should be rejected")
	}
}
