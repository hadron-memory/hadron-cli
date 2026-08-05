package spec

import "testing"

func strp(s string) *string { return &s }

// A citation pointing at a retired spec is the failure this command exists for,
// and the finding has to carry the replacement — the fix, not just the verdict.
func TestJudgeCitationSuperseded(t *testing.T) {
	n := specNode{
		Loc:  "cor:api:060:01",
		Tags: []string{"spec", supersededTag},
		OutEdges: []specEdge{
			{Name: "toc", Loc: "cor:api:060"},
			{Name: supersededByLabel, Loc: "cor:api:060:02"},
		},
	}
	v, bad := judgeCitation(n, true)
	if !bad {
		t.Fatal("a superseded spec must be reported")
	}
	if v.Rule != ruleSuperseded || v.Severity != sevError {
		t.Errorf("want an error-severity %q finding, got %+v", ruleSuperseded, v)
	}
	if v.Replacement != "cor:api:060:02" {
		t.Errorf("the finding must name the replacement, got %q", v.Replacement)
	}

	// A retirement whose edge is missing (the partial-write window supersede
	// warns about) is still reported — just without a replacement to name.
	n.OutEdges = n.OutEdges[:1]
	v, bad = judgeCitation(n, true)
	if !bad || v.Rule != ruleSuperseded {
		t.Fatalf("still a superseded citation, got %+v (bad=%v)", v, bad)
	}
	if v.Replacement != "" {
		t.Errorf("no superseded-by edge means no replacement to name, got %q", v.Replacement)
	}
}

func TestJudgeCitationStaleAbstract(t *testing.T) {
	content := "# The rule\n\nBody text.\n"
	fresh := specNode{
		Loc: "cor:api:060:01", Tags: []string{"spec"},
		Abstract: strp("An abstract."), Content: &content,
		AbstractOriginHash: strp(contentHash(content)),
	}
	if _, bad := judgeCitation(fresh, true); bad {
		t.Error("an abstract authored against this content is not stale")
	}

	stale := fresh
	stale.AbstractOriginHash = strp(contentHash("something else entirely"))
	v, bad := judgeCitation(stale, true)
	if !bad {
		t.Fatal("a drifted abstract must be reported")
	}
	if v.Rule != ruleStaleAbstract || v.Severity != sevWarning {
		t.Errorf("staleness is a warning, not an error: %+v", v)
	}

	// Silent when the comparison can't be made: no hash, or no content to hash.
	noHash := fresh
	noHash.AbstractOriginHash = nil
	if _, bad := judgeCitation(noHash, true); bad {
		t.Error("a spec with no origin hash is a `spec lint` concern, not a citation defect")
	}
	noContent := fresh
	noContent.Content = nil
	if _, bad := judgeCitation(noContent, true); bad {
		t.Error("without content there is nothing to compare the hash against")
	}
}

// Retirement outranks staleness: a superseded spec's abstract drifting is
// beside the point, and reporting both would double-count one problem.
func TestJudgeCitationSupersededOutranksStale(t *testing.T) {
	content := "body"
	n := specNode{
		Loc: "cor:api:060:01", Tags: []string{"spec", supersededTag},
		Content: &content, AbstractOriginHash: strp("deadbeef"),
		OutEdges: []specEdge{{Name: supersededByLabel, Loc: "cor:api:060:02"}},
	}
	if v, _ := judgeCitation(n, true); v.Rule != ruleSuperseded {
		t.Errorf("want %q, got %q", ruleSuperseded, v.Rule)
	}
}

func TestJudgeCitationHealthy(t *testing.T) {
	content := "body"
	n := specNode{
		Loc: "cor:api:060:01", Tags: []string{"spec"},
		Abstract: strp("An abstract."), Content: &content,
		AbstractOriginHash: strp(contentHash(content)),
		OutEdges:           []specEdge{{Name: "toc", Loc: "cor:api:060"}},
	}
	if v, bad := judgeCitation(n, true); bad {
		t.Errorf("a live, fresh spec is not a finding: %+v", v)
	}
}

// contentHash must be the server's own definition — SHA-256 of the content,
// truncated to 8 hex chars — or every citation would report as stale.
func TestContentHashShape(t *testing.T) {
	h := contentHash("hello")
	if len(h) != 8 {
		t.Fatalf("want 8 hex chars, got %q", h)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	if h != "2cf24dba" {
		t.Errorf("hash should be the sha256 prefix, got %q", h)
	}
	if contentHash("hello") == contentHash("hello ") {
		t.Error("the hash must be content-sensitive")
	}
}

func TestDistinctCitations(t *testing.T) {
	refs := []citationRef{
		{Citation: "cor:api:060:01", File: "a.go"},
		{Citation: "cor:api:060:01", File: "b.go"},
		{Citation: "cor:dmo:020:01", File: "b.go"},
	}
	got := distinctCitations(refs)
	if len(got) != 2 || got[0] != "cor:api:060:01" || got[1] != "cor:dmo:020:01" {
		t.Errorf("a citation repeated across files costs ONE read, got %v", got)
	}
}
