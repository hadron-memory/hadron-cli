package coding

import (
	"strings"
	"testing"
)

// A cross-memory route cannot use a bare wikilink: it would resolve against the
// ROUTER's memory and find nothing (cor:urn:020:01). The line must name the
// target's memory instead.
func TestCrossMemoryLinkForm(t *testing.T) {
	if got := wikiLink("findings:x"); got != "[[findings:x]]" {
		t.Errorf("wikiLink = %q", got)
	}
	got := crossMemoryLink("tasks:create-platform-spec", "hrn:mem:acme.com:specs")
	want := "`tasks:create-platform-spec` in `hrn:mem:acme.com:specs`"
	if got != want {
		t.Errorf("crossMemoryLink = %q, want %q", got, want)
	}
	line := routingLineTo("To author a spec", got, "How to place a citation")
	if !strings.Contains(line, want) || !strings.Contains(line, `- **"To author a spec"** →`) {
		t.Errorf("routingLineTo = %q", line)
	}
}

// The memory label comes from the ref the caller typed — the projection carries
// an opaque id, which tells a reader nothing — and is emitted as grammar v2.
func TestTargetMemoryLabel(t *testing.T) {
	cases := []struct{ name, ref, want string }{
		{"v2 node urn", "hrn:node:acme.com:specs:tasks:create-platform-spec", "hrn:mem:acme.com:specs"},
		{"v2 deep loc", "hrn:node:acme.com:dev:ops:incidents:20260517-x", "hrn:mem:acme.com:dev"},
		{"case-insensitive prefix", "HRN:NODE:acme.com:specs:tasks:x", "hrn:mem:acme.com:specs"},
		{"legacy v1 normalizes to v2", "acme.com::specs::tasks:x", "hrn:mem:acme.com:specs"},
		{"unparseable falls back to the id", "0199aabbccdd", "mem_id_fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := targetMemoryLabel("mem_id_fallback", tc.ref); got != tc.want {
				t.Errorf("targetMemoryLabel(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}
