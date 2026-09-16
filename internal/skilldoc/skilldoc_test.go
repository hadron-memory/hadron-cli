package skilldoc

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDeriveName(t *testing.T) {
	cases := []struct{ prefix, loc, want string }{
		{"hadron-", "tasks:create-release-tag", "hadron-create-release-tag"},
		{"hadron-", "export-task-as-claude-skill", "hadron-export-task-as-claude-skill"},
		{"hadron-", "tasks:review:run", "hadron-review-run"},
		{"mm-", "tasks:mm-briefing", "mm-mm-briefing"},
		// A loc that IS "tasks" (no child) keeps its one segment: dropping it
		// would derive a bare prefix.
		{"hadron-", "tasks", "hadron-tasks"},
		// Only a LEADING tasks segment is structure.
		{"hadron-", "ops:tasks:rotate", "hadron-ops-tasks-rotate"},
		{"", "tasks:foo", "foo"},
	}
	for _, c := range cases {
		if got := DeriveName(c.prefix, c.loc); got != c.want {
			t.Errorf("DeriveName(%q, %q) = %q, want %q", c.prefix, c.loc, got, c.want)
		}
	}
}

func TestValidName(t *testing.T) {
	ok := []string{"hadron-create-release-tag", "a", "a1-b2", strings.Repeat("a", MaxNameLen)}
	bad := []string{"", "Hadron-Foo", "hadron--foo", "-hadron", "hadron-", "hadron_foo", "hadron foo", strings.Repeat("a", MaxNameLen+1)}
	for _, n := range ok {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func TestValidPrefix(t *testing.T) {
	for _, p := range []string{"hadron-", "mm-", "a1-"} {
		if !ValidPrefix(p) {
			t.Errorf("ValidPrefix(%q) = false", p)
		}
	}
	for _, p := range []string{"", "hadron", "Hadron-", "-", "1a-", "hadron_", "ha-dron-"} {
		if ValidPrefix(p) {
			t.Errorf("ValidPrefix(%q) = true", p)
		}
	}
}

func TestDeclared(t *testing.T) {
	d, ok := Declared(map[string]any{"skill": map[string]any{"description": "Use when x"}})
	if !ok || d.Key != "skill" || d.Description != "Use when x" || d.Name != "" {
		t.Fatalf("skill key: ok=%v d=%+v", ok, d)
	}
	d, ok = Declared(map[string]any{"claudeSkill": map[string]any{"name": "old-name", "description": "Use when y"}})
	if !ok || d.Key != "claudeSkill" || d.Name != "old-name" {
		t.Fatalf("legacy key: ok=%v d=%+v", ok, d)
	}
	// The new key wins over a legacy key left behind by a migration.
	d, ok = Declared(map[string]any{"skill": map[string]any{"description": "new"}, "claudeSkill": map[string]any{"description": "old"}})
	if !ok || d.Key != "skill" || d.Description != "new" {
		t.Fatalf("both keys: ok=%v d=%+v", ok, d)
	}
	// Not an object ⇒ not declared: a stray string under the key is not an opt-in.
	if _, ok := Declared(map[string]any{"skill": "yes"}); ok {
		t.Error("non-object skill value read as declared")
	}
	if _, ok := Declared(map[string]any{}); ok {
		t.Error("empty properties read as declared")
	}
	if _, ok := Declared(nil); ok {
		t.Error("nil properties read as declared")
	}
}

func TestHashIsInputSensitiveAndStable(t *testing.T) {
	h := Hash("n", "d", "c")
	if len(h) != 16 {
		t.Fatalf("hash length %d, want 16", len(h))
	}
	if h != Hash("n", "d", "c") {
		t.Error("hash not stable")
	}
	for _, alt := range [][3]string{{"n2", "d", "c"}, {"n", "d2", "c"}, {"n", "d", "c2"}} {
		if Hash(alt[0], alt[1], alt[2]) == h {
			t.Errorf("hash insensitive to %v", alt)
		}
	}
	// NUL separation: shifting a boundary must not collide.
	if Hash("ab", "c", "") == Hash("a", "bc", "") {
		t.Error("boundary shift collides")
	}
}

func declaring(loc, desc string, extra map[string]any) Node {
	decl := map[string]any{"description": desc}
	for k, v := range extra {
		decl[k] = v
	}
	return Node{
		URN: "hrn:node:hadronmemory.com:core:" + loc, Loc: loc, MemoryURN: "hrn:mem:hadronmemory.com:core",
		IsRunnable: true, Content: "# Body\n\nDo the thing.",
		Properties: map[string]any{"skill": decl},
	}
}

func rules(fs []Finding) map[string]string {
	m := map[string]string{}
	for _, f := range fs {
		m[f.Rule] = f.Severity
	}
	return m
}

func TestLintCleanNodeHasNoFindings(t *testing.T) {
	n := declaring("tasks:create-release-tag", "Use when the user says 'cut a release'.", nil)
	if fs := Lint(n, Prefix{Value: "hadron-", Known: true}); len(fs) != 0 {
		t.Fatalf("clean node produced findings: %+v", fs)
	}
}

func TestLintMalformedDeclarationIsAnError(t *testing.T) {
	for _, props := range []map[string]any{
		{"skill": "yes"}, {"claudeSkill": true}, {"skill": []any{"x"}},
	} {
		n := Node{URN: "u", Loc: "tasks:x", IsRunnable: true, Content: "b", Properties: props}
		got := rules(Lint(n, Prefix{Value: "hadron-", Known: true}))
		if got["skill-declaration-malformed"] != SevError {
			t.Errorf("props %v: want malformed error, got %v", props, got)
		}
	}
}

func TestLintUndeclaredNodeIsSilent(t *testing.T) {
	n := Node{URN: "u", Loc: "tasks:x", IsRunnable: false, Properties: map[string]any{}}
	if fs := Lint(n, Prefix{Value: "hadron-", Known: true}); len(fs) != 0 {
		t.Fatalf("undeclared node produced findings: %+v", fs)
	}
}

func TestLintRules(t *testing.T) {
	long := strings.Repeat("x", MaxDescriptionLen+1)
	cases := []struct {
		name   string
		node   Node
		prefix string
		want   map[string]string // rule → severity that MUST be present
		absent []string
	}{
		{"description missing", declaring("tasks:a", "   ", nil), "hadron-",
			map[string]string{"skill-description-missing": SevError}, []string{"skill-description-no-trigger"}},
		{"description too long", declaring("tasks:a", "Use when "+long, nil), "hadron-",
			map[string]string{"skill-description-too-long": SevError}, nil},
		{"no trigger phrasing", declaring("tasks:a", "Exports things.", nil), "hadron-",
			map[string]string{"skill-description-no-trigger": SevWarning}, nil},
		{"hand-set name mismatch", declaring("tasks:mint-spec", "Use when x", map[string]any{"name": "add-spec"}), "hadron-",
			map[string]string{"skill-name-hand-set": SevError}, nil},
		{"hand-set name equal is accepted", declaring("tasks:mint-spec", "Use when x", map[string]any{"name": "hadron-mint-spec"}), "hadron-",
			map[string]string{}, []string{"skill-name-hand-set"}},
		{"invalid derived name", declaring("tasks:Bad_Loc", "Use when x", nil), "hadron-",
			map[string]string{"skill-name-invalid": SevError}, nil},
		{"name too long", declaring("tasks:"+strings.Repeat("a", 70), "Use when x", nil), "hadron-",
			map[string]string{"skill-name-invalid": SevError}, nil},
		{"not runnable", func() Node { n := declaring("tasks:a", "Use when x", nil); n.IsRunnable = false; return n }(), "hadron-",
			map[string]string{"skill-not-runnable": SevError}, nil},
		{"empty content", func() Node { n := declaring("tasks:a", "Use when x", nil); n.Content = " \n"; return n }(), "hadron-",
			map[string]string{"skill-content-empty": SevError}, nil},
		{"frontmatter in content", func() Node {
			n := declaring("tasks:a", "Use when x", nil)
			n.Content = "---\nname: x\n---\nbody"
			return n
		}(), "hadron-",
			map[string]string{"skill-content-has-frontmatter": SevError}, nil},
		{"template placeholder", func() Node { n := declaring("tasks:a", "Use when x", nil); n.Content = "You are {{name}}."; return n }(), "hadron-",
			map[string]string{"skill-content-has-template": SevWarning}, nil},
		{"legacy key", func() Node {
			n := declaring("tasks:a", "Use when x", nil)
			n.Properties = map[string]any{"claudeSkill": n.Properties["skill"]}
			return n
		}(), "hadron-", map[string]string{"skill-legacy-key": SevWarning}, nil},
	}
	for _, c := range cases {
		got := rules(Lint(c.node, Prefix{Value: c.prefix, Known: true}))
		for rule, sev := range c.want {
			if got[rule] != sev {
				t.Errorf("%s: want %s=%s, got %v", c.name, rule, sev, got)
			}
		}
		for _, rule := range c.absent {
			if _, ok := got[rule]; ok {
				t.Errorf("%s: %s must be absent, got %v", c.name, rule, got)
			}
		}
	}
}

func TestLintTooLongMessageCarriesTheOverrun(t *testing.T) {
	n := declaring("tasks:a", "Use when "+strings.Repeat("x", 1100), nil)
	var msg string
	for _, f := range Lint(n, Prefix{Value: "hadron-", Known: true}) {
		if f.Rule == "skill-description-too-long" {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "1109 characters") || !strings.Contains(msg, "shorten by 85") {
		t.Errorf("message does not state the measurement: %q", msg)
	}
}

func TestLintCollisions(t *testing.T) {
	a := declaring("tasks:start-worker-session-desktop", "Use when x", nil)
	b := declaring("tasks:start-worker-session-desktop", "Use when x", nil)
	b.URN = "hrn:node:hadronmemory.com:hadron-cli:tasks:start-worker-session-desktop"
	b.MemoryURN = "hrn:mem:hadronmemory.com:hadron-cli"
	c := declaring("tasks:other", "Use when x", nil)
	undeclared := Node{URN: "u", Loc: "tasks:start-worker-session-desktop", MemoryURN: a.MemoryURN, Properties: map[string]any{}}
	known := func(v string) Prefix { return Prefix{Value: v, Known: true} }
	prefixes := map[string]Prefix{a.MemoryURN: known("hadron-"), b.MemoryURN: known("hadron-")}
	fs := LintCollisions([]Node{a, b, c, undeclared}, prefixes)
	if len(fs) != 2 {
		t.Fatalf("want 2 collision findings (one per member), got %d: %+v", len(fs), fs)
	}
	for _, f := range fs {
		if f.Rule != "skill-name-collision" || f.Severity != SevError {
			t.Errorf("bad finding %+v", f)
		}
		if f.URN == a.URN && !strings.Contains(f.Message, b.URN) {
			t.Errorf("a's finding does not name b: %q", f.Message)
		}
	}
	// Different prefixes ⇒ different names ⇒ no collision.
	prefixes[b.MemoryURN] = known("cli-")
	if fs := LintCollisions([]Node{a, b}, prefixes); len(fs) != 0 {
		t.Errorf("distinct prefixes still collide: %+v", fs)
	}
	// Two orgs with NO prefix yet share a bare slug — not a collision: the
	// prefixes they have yet to choose are what keeps them apart.
	prefixes[a.MemoryURN], prefixes[b.MemoryURN] = Prefix{}, Prefix{}
	if fs := LintCollisions([]Node{a, b}, prefixes); len(fs) != 0 {
		t.Errorf("prefix-less nodes reported as colliding: %+v", fs)
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	desc := "Use when the user says: 'cut a release' — handles #tags and \"quotes\"."
	body := "# Cut a release\n\nStep one.\n\n```sh\ngit tag\n```\n"
	src := "hrn:node:hadronmemory.com:core:tasks:create-release-tag"
	file, err := Render("hadron-create-release-tag", src, desc, body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(file, "---\nname: hadron-create-release-tag\n") {
		t.Fatalf("frontmatter wrong:\n%s", file)
	}
	f, err := ParseFile([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "hadron-create-release-tag" || f.Description != desc || f.Source != src {
		t.Errorf("parsed %+v", f)
	}
	if f.Body != strings.TrimRight(body, "\n") {
		t.Errorf("body round-trip:\n got %q\nwant %q", f.Body, strings.TrimRight(body, "\n"))
	}
	// The contract that makes local-edit detection server-free: the hash in
	// the header equals the hash recomputed from the file's own three inputs.
	if want := Hash(f.Name, f.Description, f.Body); f.Hash != want {
		t.Errorf("header hash %q != recomputed %q", f.Hash, want)
	}
	// And a hand edit to the body is visible.
	edited := strings.Replace(file, "Step one.", "Step one, edited.", 1)
	g, _ := ParseFile([]byte(edited))
	if Hash(g.Name, g.Description, g.Body) == g.Hash {
		t.Error("hand edit not detected by recomputation")
	}
}

func TestBodyKeepsItsOwnLeadingComment(t *testing.T) {
	// Codex on #589, round 3: only OUR provenance lines are preamble. A node
	// whose content opens with an HTML comment of its own must round-trip
	// with that comment in the body, hash-equal to its header.
	body := "<!-- reviewers: read the Scope first -->\n\n# Body\n"
	file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", "Use when x", body)
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.Body, "<!-- reviewers:") {
		t.Errorf("the body's own leading comment was swallowed: %q", f.Body)
	}
	if f.Hash != Hash(f.Name, f.Description, f.Body) {
		t.Error("fresh export with a leading body comment does not hash equal to its header")
	}
}

func TestLookalikeProvenanceCommentsStayInTheBody(t *testing.T) {
	// Codex on #589, round 4: matching by substring ate a body's own
	// `<!-- Generated from a template -->`. Only the exact generated lines
	// are preamble.
	for _, first := range []string{
		"<!-- Generated from a template -->",
		"<!-- Edit the source node before running this -->",
		"<!-- hadron-skill is the command that made this -->",
	} {
		body := first + "\n\n# Body\n"
		file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", "Use when x", body)
		if err != nil {
			t.Fatal(err)
		}
		f, err := ParseFile([]byte(file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(f.Body, first) {
			t.Errorf("%q swallowed as preamble: body=%q", first, f.Body)
		}
		if f.Hash != Hash(f.Name, f.Description, f.Body) {
			t.Errorf("%q: fresh export does not hash equal to its header", first)
		}
	}
}

func TestFrontmatterRuleNeedsAClosingDelimiter(t *testing.T) {
	// A horizontal rule at the top of a body is markdown, not frontmatter.
	rule := declaring("tasks:a", "Use when x", nil)
	rule.Content = "---\n\n# Starts with a rule\n"
	if got := rules(Lint(rule, Prefix{Value: "hadron-", Known: true})); got["skill-content-has-frontmatter"] != "" {
		t.Errorf("leading horizontal rule flagged as frontmatter: %v", got)
	}
	fm := declaring("tasks:a", "Use when x", nil)
	fm.Content = "---\nname: x\n---\n\n# Real frontmatter\n"
	if got := rules(Lint(fm, Prefix{Value: "hadron-", Known: true})); got["skill-content-has-frontmatter"] != SevError {
		t.Errorf("real frontmatter not flagged: %v", got)
	}
	// Windows line endings are the same frontmatter (Codex on #589, round 6).
	crlf := declaring("tasks:a", "Use when x", nil)
	crlf.Content = "---\r\nname: x\r\n---\r\n\r\n# Real frontmatter\r\n"
	if got := rules(Lint(crlf, Prefix{Value: "hadron-", Known: true})); got["skill-content-has-frontmatter"] != SevError {
		t.Errorf("CRLF frontmatter not flagged: %v", got)
	}
}

func TestCRLFBodiesRoundTrip(t *testing.T) {
	body := "# Body\r\n\r\nline one\r\nline two\r\n"
	file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", "Use when x", body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(file, "\r") {
		t.Error("export wrote CRLF")
	}
	f, err := ParseFile([]byte(strings.ReplaceAll(file, "\n", "\r\n"))) // and a file re-saved with CRLF still reads
	if err != nil {
		t.Fatal(err)
	}
	if f.Hash != Hash(f.Name, f.Description, f.Body) || f.Hash != Hash("hadron-x", "Use when x", body) {
		t.Error("CRLF input does not hash equal to its LF export")
	}
}

func TestParseFileLegacyHeaderAndForeignFiles(t *testing.T) {
	legacy := "---\nname: add-spec\ndescription: Use when x\n---\n\n<!-- Generated from hrn:node:hadronmemory.com:core:tasks:mint-spec -->\n<!-- Edit the source node and re-export; do not edit this file directly. -->\n\n# Body\n"
	f, err := ParseFile([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "hrn:node:hadronmemory.com:core:tasks:mint-spec" || f.Hash != "" || f.Body != "# Body" {
		t.Errorf("legacy parse: %+v", f)
	}
	foreign := "---\nname: someone-elses\ndescription: Not ours\n---\n\n# Not generated\n"
	g, err := ParseFile([]byte(foreign))
	if err != nil {
		t.Fatal(err)
	}
	if g.Source != "" || g.Hash != "" {
		t.Errorf("foreign file claimed a source: %+v", g)
	}
	if _, err := ParseFile([]byte("no frontmatter here")); err == nil {
		t.Error("frontmatter-less file parsed")
	}
	// Provenance is recognised in the preamble only: a foreign skill that
	// QUOTES our header in its body stays foreign (Codex on #589).
	quoting := "---\nname: theirs\ndescription: Use when x\n---\n\n# Their skill\n\nHadron writes a line like this:\n\n<!-- hadron-skill source=hrn:node:a:b:tasks:x hash=deadbeefdeadbeef -->\n<!-- Generated from hrn:node:a:b:tasks:x -->\n"
	q, err := ParseFile([]byte(quoting))
	if err != nil {
		t.Fatal(err)
	}
	if q.Source != "" || q.Hash != "" {
		t.Errorf("foreign file claimed via a quoted header in its body: %+v", q)
	}
	if !strings.Contains(q.Body, "<!-- hadron-skill") {
		t.Errorf("a quoted header in the BODY must stay in the body: %q", q.Body)
	}
}

func TestRenderSurvivesTheRealParserOnAwkwardDescriptions(t *testing.T) {
	// Each of these is a plain scalar a hand check would pass and a real
	// YAML parser rejects or alters: a trailing colon (a mapping indicator),
	// trailing whitespace (trimmed), a leading quote, a `#` comment, a
	// colon-space inside. ParseFile uses the same library a host does, so a
	// round trip proves the host reads back exactly what the node said.
	for _, desc := range []string{
		"Use when the user wants these steps:",
		"Use when exporting a task. ",
		"\"Use when\" someone quotes",
		"Use when #tags appear",
		"Use when key: value looks like a mapping",
		"Use when — em dashes, curly ‘quotes’ and ümlauts",
	} {
		file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", desc, "# body")
		if err != nil {
			t.Fatalf("%q: render: %v", desc, err)
		}
		f, err := ParseFile([]byte(file))
		if err != nil {
			t.Fatalf("%q: parse: %v\n%s", desc, err, file)
		}
		// Surrounding whitespace is normalized away on export by design
		// (NormalizeDescription); everything else must come back verbatim.
		if want := NormalizeDescription(desc); f.Description != want {
			t.Errorf("description round trip: got %q, want %q\n%s", f.Description, want, file)
		}
		if f.Hash != Hash(f.Name, f.Description, f.Body) {
			t.Errorf("%q: header hash does not match recomputation", desc)
		}
	}
}

func TestRenderNormalizesWhatLintMeasured(t *testing.T) {
	// Codex on #589: a description with a trailing space and a body wrapped
	// in blank lines. Lint measures the trimmed description; Render must
	// write exactly that, and a fresh export must hash equal to its header.
	desc := "  Use when the user says 'go'.  "
	body := "\n\n# Body\n\nline\n\n"
	file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", desc, body)
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if f.Description != "Use when the user says 'go'." {
		t.Errorf("description not normalized on export: %q", f.Description)
	}
	if f.Body != "# Body\n\nline" {
		t.Errorf("body not normalized on export: %q", f.Body)
	}
	if f.Hash != Hash(f.Name, f.Description, f.Body) || f.Hash != Hash("hadron-x", desc, body) {
		t.Error("a fresh export does not hash equal to its own header from either the raw or the parsed inputs")
	}
	// The length lint certifies is the exported length: at-limit plus a
	// trailing space is still at limit.
	atLimit := "Use when " + strings.Repeat("x", MaxDescriptionLen-9) + " "
	if got := rules(Lint(declaring("tasks:a", atLimit, nil), Prefix{Value: "hadron-", Known: true})); got["skill-description-too-long"] != "" {
		t.Errorf("trailing space counted against the limit: %v", got)
	}
	if utf8.RuneCountInString(NormalizeDescription(atLimit)) != MaxDescriptionLen {
		t.Fatal("fixture is not at the limit after normalization")
	}
}

func TestLimitsCountCharactersNotBytes(t *testing.T) {
	// The host's validator counts code points; an em dash is one character
	// and three bytes. 1024 characters with ten em dashes is at the limit,
	// not 20 bytes over it.
	desc := "Use when " + strings.Repeat("—", 10) + strings.Repeat("x", MaxDescriptionLen-9-10)
	if utf8.RuneCountInString(desc) != MaxDescriptionLen {
		t.Fatalf("fixture is %d chars, want %d", utf8.RuneCountInString(desc), MaxDescriptionLen)
	}
	if got := rules(Lint(declaring("tasks:a", desc, nil), Prefix{Value: "hadron-", Known: true})); got["skill-description-too-long"] != "" {
		t.Errorf("at-limit description flagged as too long: %v", got)
	}
}

func TestLintMalformedKeyIsReportedEvenBesideAValidOne(t *testing.T) {
	// Mid-migration: the new key was typo'd while the legacy one still works.
	// The node is declared (via claudeSkill) AND carries a malformed key.
	n := Node{URN: "u", Loc: "tasks:x", IsRunnable: true, Content: "b",
		Properties: map[string]any{"skill": "oops", "claudeSkill": map[string]any{"description": "Use when x"}}}
	got := rules(Lint(n, Prefix{Value: "hadron-", Known: true}))
	if got["skill-declaration-malformed"] != SevError || got["skill-legacy-key"] != SevWarning {
		t.Errorf("want malformed error AND legacy warning, got %v", got)
	}
}

func TestLintPrefixes(t *testing.T) {
	a := declaring("tasks:a", "Use when a", nil)
	a.MemoryURN = "hrn:mem:acme.com:ops"
	empty := Node{URN: "e", Loc: "tasks:e", MemoryURN: "hrn:mem:acme.com:empty", Properties: map[string]any{}}
	prefixes := map[string]Prefix{} // neither memory has a prefix
	fs := LintPrefixes([]Node{a, empty}, prefixes)
	if len(fs) != 1 || fs[0].URN != a.MemoryURN || fs[0].Rule != "skill-prefix-missing" {
		t.Fatalf("want one finding for the declaring memory only, got %+v", fs)
	}
	prefixes[a.MemoryURN] = Prefix{Value: "acme-", Known: true}
	if fs := LintPrefixes([]Node{a, empty}, prefixes); len(fs) != 0 {
		t.Errorf("known prefix still reported: %+v", fs)
	}
	// A hand-set name is not judged against a prefix that is unknown.
	h := declaring("tasks:mint-spec", "Use when x", map[string]any{"name": "add-spec"})
	if got := rules(Lint(h, Prefix{})); got["skill-name-hand-set"] != "" {
		t.Errorf("hand-set name judged with no prefix: %v", got)
	}
}
