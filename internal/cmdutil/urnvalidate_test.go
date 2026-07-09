package cmdutil

import "testing"

func TestValidateURNSlug(t *testing.T) {
	valid := []string{
		"acme.com",        // dots allowed in the interior
		"personal-holger", // hyphens
		"flow-lab",
		"a",        // single alphanumeric
		"Flow-Lab", // uppercase is allowed (mirrors the server today)
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
		"-lead",    // must start alphanumeric
		"trail-",   // must end alphanumeric
		".dot",     // leading dot
		"a:b",      // colon is not a slug char (that's a path)
		"emoji😀",   // non-ASCII
		"has/slash",
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
