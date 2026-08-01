package cmdutil

import (
	"strings"
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

// A scheme-prefixed ref is validated, not trusted. It used to pass through
// unchecked, so a wrong-kind or malformed URN reached the server and failed
// the WHOLE nodeBatch(refs:) call — taking every valid ref with it (#305).
func TestBatchNodeRefValidatesPrefixedRefs(t *testing.T) {
	bad := []struct{ name, ref string }{
		{"wrong entity type", "hrn:mem:acme.com:kb"},
		{"memory urn, node expected", "hrn:memory:acme.com::kb"},
		{"prefixed but not qualified", "hrn:node:acme.com"},
		{"legacy scheme, wrong kind", "urn:mem:acme.com:kb"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BatchNodeRef("", tt.ref)
			if err == nil {
				t.Fatalf("BatchNodeRef(%q) = %q, want a usage error", tt.ref, got)
			}
			if !strings.Contains(err.Error(), tt.ref) {
				t.Errorf("error should name the offending ref, got %v", err)
			}
		})
	}

	// A well-formed prefixed node URN still passes through UNCHANGED — the
	// check validates, it does not rewrite. Rewriting would flatten a compound
	// app-mem memory, whose "::" marks the memory/loc boundary.
	for _, ok := range []string{
		"hrn:node:acme.com:kb:alpha",
		"hrn:node:acme.com::kb::alpha",
		"urn:node:acme.com::kb::alpha",
		"hrn:node:acme.com::juno:app-mem:ops::alpha",
	} {
		got, err := BatchNodeRef("", ok)
		if err != nil {
			t.Errorf("BatchNodeRef(%q): unexpected error %v", ok, err)
			continue
		}
		if got != ok {
			t.Errorf("BatchNodeRef(%q) = %q — a valid prefixed ref must pass through unchanged", ok, got)
		}
	}
}

// #336 — the CLI prints node ids on every --json surface, so a ref it just
// emitted has to be feedable straight back.
func TestIsNodeID(t *testing.T) {
	for _, ref := range []string{
		"019e61808abb79a38c66c4cd5a46fb14", // a real id, as printed
		"  019e61808abb79a38c66c4cd5a46fb14  ",
		"00000000000000000000000000000000",
	} {
		if !IsNodeID(ref) {
			t.Errorf("%q should be recognised as a node id", ref)
		}
	}
	// Matched by SHAPE, not by "colon-free": a bare loc typed without -m is
	// also colon-free, and must keep its usage error naming -m.
	for _, ref := range []string{
		"start-here",                        // bare loc
		"tasks:review-changes",              // bare loc with colons
		"019E61808ABB79A38C66C4CD5A46FB14",  // uppercase
		"019e61808abb79a38c66c4cd5a46fb1",   // 31 chars
		"019e61808abb79a38c66c4cd5a46fb14a", // 33 chars
		"019e61808abb79a38c66c4cd5a46fb1g",  // non-hex
		"acme.com::kb::start-here",          // fully-qualified URN
		"hrn:node:acme.com:kb:start-here",   // prefixed URN
		"",
	} {
		if IsNodeID(ref) {
			t.Errorf("%q must NOT be treated as a node id", ref)
		}
	}
}

// The batch path takes the same refs as the single path, so a bare id must
// reach nodeBatch(refs:) verbatim rather than being rejected.
func TestBatchNodeRefAcceptsBareID(t *testing.T) {
	const id = "019e61808abb79a38c66c4cd5a46fb14"
	got, err := BatchNodeRef("", id)
	if err != nil {
		t.Fatalf("a bare node id should be accepted, got %v", err)
	}
	if got != id {
		t.Errorf("the id must pass through unrewritten: got %q", got)
	}

	// A non-id colon-free ref still gets the usage error that names -m.
	if _, err := BatchNodeRef("", "start-here"); err == nil {
		t.Error("a bare loc without -m should still be rejected")
	} else if !strings.Contains(err.Error(), "-m") {
		t.Errorf("the rejection should still point at -m, got %q", err.Error())
	}
}

// An id is unambiguous by shape, so -m must not turn it into a loc. Without
// this, `node get <id> -m <memory>` composed the id into a node URN and looked
// up a node that doesn't exist.
func TestBareIDWinsOverMemoryFlag(t *testing.T) {
	const id = "019e61808abb79a38c66c4cd5a46fb14"
	for _, mem := range []string{"", "acme.com::kb", "acme.com:kb", "acme.com::agent:app-mem:notes"} {
		got, err := BatchNodeRef(mem, id)
		if err != nil {
			t.Errorf("-m %q: a bare id should still be accepted, got %v", mem, err)
			continue
		}
		if got != id {
			t.Errorf("-m %q: the id must pass through unrewritten, got %q", mem, got)
		}
	}

	// A genuine bare loc with -m still composes, unchanged.
	got, err := BatchNodeRef("acme.com::kb", "start-here")
	if err != nil {
		t.Fatalf("bare loc + -m should compose: %v", err)
	}
	if got != "hrn:node:acme.com:kb:start-here" {
		t.Errorf("composition changed: %q", got)
	}
}

// The CUID the schema also names as a PK form is deliberately NOT accepted: a
// CUID-shaped rule is indistinguishable from an ordinary loc. These are real
// locs from the sampled memories that such a rule would have swallowed.
func TestLocsThatACUIDShapedRuleWouldSwallow(t *testing.T) {
	for _, loc := range []string{
		"preflight", "instructions", "conventions", "findings",
		"discussions", "handoffs", "patterns", "register", "services",
	} {
		if IsNodeID(loc) {
			t.Errorf("%q is a real loc and must not be read as an id", loc)
		}
		if _, err := BatchNodeRef("", loc); err == nil {
			t.Errorf("%q without -m should still be a usage error", loc)
		}
	}
}
