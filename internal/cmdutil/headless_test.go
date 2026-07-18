package cmdutil

import "testing"

func TestCanonicalNodeURNRejectsIncompletePrefixedNodeURN(t *testing.T) {
	if got, err := CanonicalNodeURN("hrn:node:acme.com"); err == nil {
		t.Fatalf("CanonicalNodeURN() = %q, want usage error", got)
	}
}

// A fully-qualified node URN passes through verbatim whether it is the legacy v1
// canonical form or the flat grammar-v2 form the server now emits (#697) — the
// entry-node URN is stored as given and resolved at run time.
func TestCanonicalNodeURNAcceptsV1AndV2(t *testing.T) {
	for _, in := range []string{
		"hrn:node:acme.com::kb::findings:x", // v1 canonical
		"hrn:node:acme.com:kb:findings:x",   // grammar-v2 flat (server-emitted)
	} {
		got, err := CanonicalNodeURN(in)
		if err != nil {
			t.Errorf("CanonicalNodeURN(%q) errored: %v", in, err)
		}
		if got != in {
			t.Errorf("CanonicalNodeURN(%q) = %q, want passthrough", in, got)
		}
	}
	// A bare v1 <org>::<memory>::<loc> still gets the canonical hrn:node: prefix.
	if got, err := CanonicalNodeURN("acme.com::kb::findings:x"); err != nil || got != "hrn:node:acme.com::kb::findings:x" {
		t.Errorf("CanonicalNodeURN(bare) = %q, err %v", got, err)
	}
}
