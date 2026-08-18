package cmdutil

import (
	"strings"
	"testing"
)

func TestValidateURNSlug(t *testing.T) {
	valid := []string{
		"acme.com",        // dots allowed in the interior
		"personal-holger", // hyphens
		"flow-lab",
		"a", // single alphanumeric
		"kb_2024",
		"x1",
	}
	for _, s := range valid {
		if err := ValidateURNSlug("--urn", s); err != nil {
			t.Errorf("ValidateURNSlug(%q) = %v, want nil", s, err)
		}
	}

	invalid := []string{
		"",         // empty
		"Flow Lab", // space — the issue #189 case
		"Flow-Lab", // create-time slugs are lowercase-canonical
		"-lead",    // must start alphanumeric
		"trail-",   // must end alphanumeric
		".dot",     // leading dot
		"a:b",      // colon is not a slug char (that's a path)
		"emoji😀",   // non-ASCII
		"has/slash",
		"system", // reserved role marker
	}
	for _, s := range invalid {
		if err := ValidateURNSlug("--urn", s); err == nil {
			t.Errorf("ValidateURNSlug(%q) = nil, want error", s)
		}
	}

	// 64 chars ok, 65 rejected.
	sixtyFour := ""
	for i := 0; i < 64; i++ {
		sixtyFour += "a"
	}
	if err := ValidateURNSlug("--urn", sixtyFour); err != nil {
		t.Errorf("64-char slug rejected: %v", err)
	}
	if err := ValidateURNSlug("--urn", sixtyFour+"a"); err == nil {
		t.Error("65-char slug accepted, want rejected")
	}
}

func TestValidateURNPath(t *testing.T) {
	valid := []string{
		"findings:flaky-ci",        // multi-atom loc
		"services:secureid:user-x", // deep loc
		"single",
		"author-org:agent-slug", // agent slug with an author-org atom
	}
	for _, p := range valid {
		if err := ValidateURNPath("--loc", p); err != nil {
			t.Errorf("ValidateURNPath(%q) = %v, want nil", p, err)
		}
	}

	invalid := []string{
		"",           // empty
		"Flow Lab",   // space
		"a::b",       // doubled colon → empty atom
		":lead",      // leading colon
		"trail:",     // trailing colon
		"ok:bad seg", // a later atom has a space
	}
	for _, p := range invalid {
		if err := ValidateURNPath("--loc", p); err == nil {
			t.Errorf("ValidateURNPath(%q) = nil, want error", p)
		}
	}
}

func TestValidateAgentURNPathAcceptsUserAuthorContext(t *testing.T) {
	valid := []string{
		"triage",
		"hadronmemory.com:triage",
		"@holger:triage",
		"agent:@holger:triage",
	}
	for _, p := range valid {
		if err := ValidateAgentURNPath("--urn", p); err != nil {
			t.Errorf("ValidateAgentURNPath(%q) = %v, want nil", p, err)
		}
	}

	invalid := []string{
		"@holger",      // handle namespace requires a following slug
		"@foo:bad seg", // following slug still uses normal atom grammar
		"system::leaf", // hierarchy separator is not part of an agent slug segment
	}
	for _, p := range invalid {
		if err := ValidateAgentURNPath("--urn", p); err == nil {
			t.Errorf("ValidateAgentURNPath(%q) = nil, want error", p)
		}
	}
}

func TestValidateURNPathStillRejectsHandleNamespaceInNodeLoc(t *testing.T) {
	if err := ValidateURNPath("--loc", "@foo:bar"); err == nil {
		t.Fatal("ValidateURNPath accepted @handle in a node loc, want rejection")
	}
}

func TestCanonicalizeURNSpec047GoldenSet(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "hrn:app:@holger::gmail-app",
			want:  "hrn:app:@holger::gmail-app",
		},
		{
			input: "hrn:agent:@holger::inbox-triage",
			want:  "hrn:agent:@holger::inbox-triage",
		},
		{
			input: "hrn:memory:@holger::inbox-triage",
			want:  "hrn:memory:@holger::inbox-triage",
		},
		{
			input: "hrn:node:@holger::gmail-app::inbox-triage::system::review:sort-imports",
			want:  "hrn:node:@holger::gmail-app::inbox-triage::system::review:sort-imports",
		},
		{
			input: "hrn:node:micromentor.org::coding-app::@holger:triage::system::review:foo",
			want:  "hrn:node:micromentor.org::coding-app::@holger:triage::system::review:foo",
		},
		{
			input: "hrn:agent:micromentor.org::app:gmail::agent:@holger:triage",
			want:  "hrn:agent:micromentor.org::gmail::@holger:triage",
		},
		{
			input: "hrn:agent:@holger::@holger:triage",
			want:  "hrn:agent:@holger::triage",
		},
		// Grammar-v2 flat forms (#480). The golden set had only v1 spellings,
		// so it could not tell "we track urn-lib-go" from "we track the copy we
		// happen to be pinned to" — and the pin was a month stale, predating the
		// only tagged release. These pin the v2 side of the parity claim.
		{
			input: "hrn:mem:acme.com:kb",
			want:  "hrn:mem:acme.com:kb",
		},
		{
			input: "hrn:node:acme.com:kb:findings:flaky-ci",
			want:  "hrn:node:acme.com:kb:findings:flaky-ci",
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := CanonicalizeURN("--urn", tt.input)
			if err != nil {
				t.Fatalf("CanonicalizeURN() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalizeURN() = %q, want %q", got, tt.want)
			}
		})
	}
}

// v2-ONLY types are rejected here ON PURPOSE, and that is a boundary worth
// pinning rather than leaving incidental (#480).
//
// CanonicalizeURN routes through ToParserCanonical -> ParseUrn, whose
// v2ToV1Type map covers only the types that had a v1 equivalent (mem->memory,
// org, user, agent, app, node, edge, asset, secret). A type that never existed
// in the v1 surface — worker, apprun, noderev, appkey — deliberately keeps the
// v1 unknown-type error rather than gaining a partial parse. urn-lib-go says so
// in that map's own comment.
//
// So this is NOT a bug to fix by widening the map locally, and NOT evidence the
// dependency is stale: v0.0.13 knows `worker` perfectly well (V2URNTypes,
// ComposeWorkerUrnV2) and still refuses it here. If the CLI ever needs to
// validate a worker URN — #1008 has us publishing them everywhere — the answer
// is a v2 entry point, not this one. Asserted so the next bump surfaces a
// change in that contract instead of hiding it.
func TestCanonicalizeURNRejectsV2OnlyTypes(t *testing.T) {
	for _, input := range []string{
		"hrn:worker:hadronmemory.com:hadron-dev-team:jonas",
		"hrn:apprun:acme.com:app:run1",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := CanonicalizeURN("--urn", input)
			if err == nil {
				t.Fatalf("CanonicalizeURN() = %q, want the deliberate v2-only refusal", got)
			}
			if !strings.Contains(err.Error(), "unknown-type") {
				t.Fatalf("want the unknown-type refusal, got %v", err)
			}
		})
	}
}

func TestCanonicalizeURNRejectsIllegalHandleNamespace(t *testing.T) {
	tests := []string{
		"hrn:user:@holger",
		"hrn:app:hadronmemory.com::@foo",
		"hrn:app:@::gmail-app",
		"hrn:node:hadronmemory.com::gmail-app::inbox-triage::system::@foo:bar",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if got, err := CanonicalizeURN("--urn", input); err == nil {
				t.Fatalf("CanonicalizeURN() = %q, want error", got)
			}
		})
	}
}
