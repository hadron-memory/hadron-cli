package cmdutil

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// BatchNodeRef is what makes a multi-ref read ONE call: it produces a ref
// nodeBatch(refs:) resolves server-side (hadron-server#813) instead of costing
// a resolveUrn round trip per ref. It must accept exactly what ResolveNodeRef
// accepts, so `node get <ref>` and `node get <ref> <ref>` never disagree about
// which refs are valid.
func TestBatchNodeRefComposes(t *testing.T) {
	cases := []struct {
		name   string
		memory string
		ref    string
		want   string
	}{
		{"bare loc + memory", "acme.com::kb", "alpha", "hrn:node:acme.com:kb:alpha"},
		{"single-colon memory", "acme.com:kb", "alpha", "hrn:node:acme.com:kb:alpha"},
		{"colon-bearing loc", "acme.com::kb", "findings:a", "hrn:node:acme.com:kb:findings:a"},
		{"full URN, no memory", "", "acme.com::kb::alpha", "hrn:node:acme.com::kb::alpha"},
		{"already prefixed", "", "hrn:node:acme.com:kb:alpha", "hrn:node:acme.com:kb:alpha"},
		{"legacy prefix passes through", "", "urn:node:acme.com::kb::alpha", "urn:node:acme.com::kb::alpha"},
		{"trimmed", "", "  acme.com::kb::alpha  ", "hrn:node:acme.com::kb::alpha"},
		// A COMPOUND app-mem memory can't be a fixed-arity flat v2 node URN, so
		// it joins into the legacy <memory>::<loc> form (accepted forever, #239).
		{"compound memory", "acme.com::juno:app-mem:ops", "alpha", "hrn:node:acme.com::juno:app-mem:ops::alpha"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BatchNodeRef(tt.memory, tt.ref)
			if err != nil {
				t.Fatalf("BatchNodeRef(%q, %q): %v", tt.memory, tt.ref, err)
			}
			if got != tt.want {
				t.Errorf("BatchNodeRef(%q, %q) = %q, want %q", tt.memory, tt.ref, got, tt.want)
			}
		})
	}
}

// A ref the server would reject loudly must be caught locally as a USAGE error
// — that is the classification `node get` maps onto its unavailable list, so
// one mistyped ref costs only itself rather than the whole batch.
func TestBatchNodeRefRejectsUnqualified(t *testing.T) {
	cases := []struct {
		name   string
		memory string
		ref    string
	}{
		{"bare loc without memory", "", "alpha"},
		{"single-colon ambiguous form", "", "acme.com:kb:alpha"},
		{"memory only", "", "acme.com::kb"},
		{"empty loc with memory", "acme.com::kb", "   "},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BatchNodeRef(tt.memory, tt.ref)
			if err == nil {
				t.Fatalf("BatchNodeRef(%q, %q) = %q, want a usage error", tt.memory, tt.ref, got)
			}
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Errorf("exit code = %d, want %d (usage)", code, exitcode.Usage)
			}
		})
	}
}
