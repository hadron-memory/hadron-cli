package cmdutil

import "testing"

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
