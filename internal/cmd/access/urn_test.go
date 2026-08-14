package access

import "testing"

// #423: `access check` printed a resource URN its own argument parser then
// refused — bare for a memory/agent/app, prefixed for a node, inside one
// command's output. The emitted value must be canonical v2 for every kind, and
// must round-trip back in as input.
func TestCanonicalResourceURN(t *testing.T) {
	cases := []struct {
		name, kind, raw, want string
	}{
		// The reported cases: the server hands back the raw stored column.
		{"memory gains its prefix", "memory", "hadronmemory.com:dev", "hrn:mem:hadronmemory.com:dev"},
		{"agent gains its prefix", "agent", "hadronmemory.com:ada", "hrn:agent:hadronmemory.com:ada"},
		{"app gains its prefix", "app", "acme.com:support", "hrn:app:acme.com:support"},
		// Idempotent: the node branch already emits prefixed, and every kind
		// will once the server catches up — this must be a no-op then.
		{"already-prefixed node is untouched", "node",
			"hrn:node:hadronmemory.com:dev:preflight", "hrn:node:hadronmemory.com:dev:preflight"},
		{"already-prefixed memory is untouched", "memory",
			"hrn:mem:acme.com:kb", "hrn:mem:acme.com:kb"},
		// A urn: scheme normalizes to hrn:, matching the rest of the CLI.
		{"urn scheme normalizes", "memory", "urn:mem:acme.com:kb", "hrn:mem:acme.com:kb"},
		// A multi-atom slug (a compound per-user memory) keeps every atom.
		{"compound memory slug survives", "memory",
			"acme.com:ada:app-user:u1", "hrn:mem:acme.com:ada:app-user:u1"},
		// aiServiceConfig has NO URN — the field carries its id, which must not
		// be dressed up as one.
		{"config id is left alone", "aiServiceConfig", "cfg_123", "cfg_123"},
		// Defensive: never invent a URN out of something that cannot compose.
		{"unknown kind passes through", "organization", "acme.com", "acme.com"},
		{"single atom passes through", "memory", "dev", "dev"},
		{"empty passes through", "memory", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalResourceURN(tc.kind, tc.raw); got != tc.want {
				t.Errorf("canonicalResourceURN(%q, %q) = %q, want %q", tc.kind, tc.raw, got, tc.want)
			}
		})
	}
}

// The point of the issue: whatever `access check` prints must be accepted back.
func TestEmittedResourceURNRoundTripsAsInput(t *testing.T) {
	for _, tc := range []struct{ kind, raw string }{
		{"memory", "hadronmemory.com:dev"},
		{"agent", "hadronmemory.com:ada"},
		{"app", "acme.com:support"},
		{"node", "hrn:node:hadronmemory.com:dev:preflight"},
	} {
		emitted := canonicalResourceURN(tc.kind, tc.raw)
		if _, err := normalizeResourceRef(emitted); err != nil {
			t.Errorf("%s: emitted %q is not accepted as input: %v", tc.kind, emitted, err)
		}
	}
}
