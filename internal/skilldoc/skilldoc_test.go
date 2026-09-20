package skilldoc

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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

func TestDeclared(t *testing.T) {
	d, ok := Declared(map[string]any{"skill": map[string]any{"description": "Use when x"}})
	if !ok || d.Key != "skill" || d.Description != "Use when x" || d.Name != "" {
		t.Fatalf("skill key: ok=%v d=%+v", ok, d)
	}
	d, ok = Declared(map[string]any{"claudeSkill": map[string]any{"name": "old-name", "description": "Use when y"}})
	if !ok || d.Key != "claudeSkill" || d.Name != "old-name" {
		t.Fatalf("legacy key: ok=%v d=%+v", ok, d)
	}
	// The new key wins over a legacy key left behind by a migration — and the
	// leftover is still a warning (Copilot on #589).
	both := map[string]any{"skill": map[string]any{"description": "Use when new"}, "claudeSkill": map[string]any{"description": "old"}}
	d, ok = Declared(both)
	if !ok || d.Key != "skill" || d.Description != "Use when new" {
		t.Fatalf("both keys: ok=%v d=%+v", ok, d)
	}
	if got := rules(Lint(Node{URN: "u", Loc: "tasks:x", IsRunnable: true, Content: "b", Properties: both})); got["skill-legacy-key"] != SevWarning {
		t.Errorf("leftover claudeSkill beside skill not warned: %v", got)
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
	h := Hash("s", "n", "d", "c")
	if len(h) != 16 {
		t.Fatalf("hash length %d, want 16", len(h))
	}
	if h != Hash("s", "n", "d", "c") {
		t.Error("hash not stable")
	}
	for _, alt := range [][4]string{{"s2", "n", "d", "c"}, {"s", "n2", "d", "c"}, {"s", "n", "d2", "c"}, {"s", "n", "d", "c2"}} {
		if Hash(alt[0], alt[1], alt[2], alt[3]) == h {
			t.Errorf("hash insensitive to %v", alt)
		}
	}
	// NUL separation: shifting a boundary must not collide.
	if Hash("s", "ab", "c", "") == Hash("s", "a", "bc", "") {
		t.Error("boundary shift collides")
	}
}

// declaring builds a node carrying the D12 declaration shape —
// `exports.claudeSkill` with a stored name — so a node this helper calls clean
// really is clean. `extra` overrides or adds fields inside the host entry; pass
// {"name": ...} to exercise the name rules, or {"enable": false} for disabled.
func declaring(loc, desc string, extra map[string]any) Node {
	decl := map[string]any{"name": "hadron-" + strings.ReplaceAll(strings.TrimPrefix(loc, "tasks:"), ":", "-"), "description": desc}
	for k, v := range extra {
		decl[k] = v
	}
	return Node{
		URN: "hrn:node:hadronmemory.com:core:" + loc, Loc: loc, MemoryURN: "hrn:mem:hadronmemory.com:core",
		IsRunnable: true, Content: "# Body\n\nDo the thing.",
		Properties: map[string]any{ExportsKey: map[string]any{HostClaudeSkill: decl}},
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
	if fs := Lint(n); len(fs) != 0 {
		t.Fatalf("clean node produced findings: %+v", fs)
	}
}

func TestLintMalformedDeclarationIsAnError(t *testing.T) {
	for _, props := range []map[string]any{
		{"skill": "yes"}, {"claudeSkill": true}, {"skill": []any{"x"}},
	} {
		n := Node{URN: "u", Loc: "tasks:x", IsRunnable: true, Content: "b", Properties: props}
		got := rules(Lint(n))
		if got["skill-declaration-malformed"] != SevError {
			t.Errorf("props %v: want malformed error, got %v", props, got)
		}
	}
}

func TestLintUndeclaredNodeIsSilent(t *testing.T) {
	n := Node{URN: "u", Loc: "tasks:x", IsRunnable: false, Properties: map[string]any{}}
	if fs := Lint(n); len(fs) != 0 {
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
		{"invalid stored name", declaring("tasks:a", "Use when x", map[string]any{"name": "Bad_Name"}), "hadron-",
			map[string]string{"skill-name-invalid": SevError}, nil},
		{"stored name too long", declaring("tasks:a", "Use when x", map[string]any{"name": "hadron-" + strings.Repeat("a", 70)}), "hadron-",
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
		{"retired top-level key", func() Node {
			// The pre-D12 shape, which still WORKS (read as an alias) and is
			// steered to exports.<host> by a warning rather than refused.
			n := declaring("tasks:a", "Use when x", nil)
			host := n.Properties[ExportsKey].(map[string]any)[HostClaudeSkill]
			n.Properties = map[string]any{"claudeSkill": host}
			return n
		}(), "hadron-", map[string]string{"skill-legacy-key": SevWarning}, []string{"skill-declaration-malformed", "skill-name-missing"}},
	}
	for _, c := range cases {
		got := rules(Lint(c.node))
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
	for _, f := range Lint(n) {
		if f.Rule == "skill-description-too-long" {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "1109 characters") || !strings.Contains(msg, "shorten by 85") {
		t.Errorf("message does not state the measurement: %q", msg)
	}
}

func TestLintCollisions(t *testing.T) {
	// D12: the name is STORED, so a collision is two nodes storing one name —
	// across memories and orgs alike, because there is no prefix left to keep
	// two orgs' identically-named tasks apart. That is the cost D12 accepts and
	// this test is where it is visible.
	named := func(loc, memURN, urn, name string) Node {
		return Node{
			URN: urn, Loc: loc, MemoryURN: memURN, IsRunnable: true, Content: "body",
			Properties: map[string]any{ExportsKey: map[string]any{
				HostClaudeSkill: map[string]any{"name": name, "description": "Use when x"},
			}},
		}
	}
	a := named("tasks:start-worker", "hrn:mem:hadronmemory.com:core",
		"hrn:node:hadronmemory.com:core:tasks:start-worker", "hadron-start-worker")
	b := named("tasks:swd", "hrn:mem:hadronmemory.com:hadron-cli",
		"hrn:node:hadronmemory.com:hadron-cli:tasks:swd", "hadron-start-worker")
	c := named("tasks:other", a.MemoryURN,
		"hrn:node:hadronmemory.com:core:tasks:other", "hadron-other")
	undeclared := Node{URN: "u", Loc: "tasks:x", MemoryURN: a.MemoryURN, Properties: map[string]any{}}

	fs := LintCollisions([]Node{a, b, c, undeclared})
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

	// Distinct stored names do not collide, however similar the locs.
	b2 := b
	b2.Properties = map[string]any{ExportsKey: map[string]any{
		HostClaudeSkill: map[string]any{"name": "cli-start-worker", "description": "Use when x"},
	}}
	if fs := LintCollisions([]Node{a, b2}); len(fs) != 0 {
		t.Errorf("distinct stored names still collide: %+v", fs)
	}

	// A declaration with NO name cannot collide — skill-name-missing is its
	// finding, and pairing two nameless nodes as "colliding on \"\"" would be
	// a second, misleading report of the same defect.
	nameless := a
	nameless.Properties = map[string]any{ExportsKey: map[string]any{
		HostClaudeSkill: map[string]any{"description": "Use when x"},
	}}
	nameless2 := b
	nameless2.Properties = nameless.Properties
	if fs := LintCollisions([]Node{nameless, nameless2}); len(fs) != 0 {
		t.Errorf("nameless declarations reported as colliding: %+v", fs)
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
	if want := Hash(f.Source, f.Name, f.Description, f.Body); f.Hash != want {
		t.Errorf("header hash %q != recomputed %q", f.Hash, want)
	}
	// And a hand edit to the body is visible.
	edited := strings.Replace(file, "Step one.", "Step one, edited.", 1)
	g, _ := ParseFile([]byte(edited))
	if Hash(g.Source, g.Name, g.Description, g.Body) == g.Hash {
		t.Error("hand edit not detected by recomputation")
	}
	// So is a hand edit to the provenance line naming another node.
	resourced := strings.Replace(file, "source="+src, "source=hrn:node:hadronmemory.com:core:tasks:other", 1)
	r, _ := ParseFile([]byte(resourced))
	if r.Source != "hrn:node:hadronmemory.com:core:tasks:other" || Hash(r.Source, r.Name, r.Description, r.Body) == r.Hash {
		t.Error("re-sourced header not detected as a local edit")
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
	if f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) {
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
		if f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) {
			t.Errorf("%q: fresh export does not hash equal to its header", first)
		}
	}
}

func TestFrontmatterRuleNeedsAClosingDelimiter(t *testing.T) {
	// A horizontal rule at the top of a body is markdown, not frontmatter.
	rule := declaring("tasks:a", "Use when x", nil)
	rule.Content = "---\n\n# Starts with a rule\n"
	if got := rules(Lint(rule)); got["skill-content-has-frontmatter"] != "" {
		t.Errorf("leading horizontal rule flagged as frontmatter: %v", got)
	}
	fm := declaring("tasks:a", "Use when x", nil)
	fm.Content = "---\nname: x\n---\n\n# Real frontmatter\n"
	if got := rules(Lint(fm)); got["skill-content-has-frontmatter"] != SevError {
		t.Errorf("real frontmatter not flagged: %v", got)
	}
	// An EMPTY frontmatter block is still frontmatter (Copilot on #589).
	empty := declaring("tasks:a", "Use when x", nil)
	empty.Content = "---\n---\n# Body\n"
	if got := rules(Lint(empty)); got["skill-content-has-frontmatter"] != SevError {
		t.Errorf("empty frontmatter block not flagged: %v", got)
	}
	// Windows line endings are the same frontmatter (Codex on #589, round 6).
	crlf := declaring("tasks:a", "Use when x", nil)
	crlf.Content = "---\r\nname: x\r\n---\r\n\r\n# Real frontmatter\r\n"
	if got := rules(Lint(crlf)); got["skill-content-has-frontmatter"] != SevError {
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
	if f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) || f.Hash != Hash("hrn:node:a:b:tasks:x", "hadron-x", "Use when x", body) {
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
		if f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) {
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
	if f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) || f.Hash != Hash("hrn:node:a:b:tasks:x", "hadron-x", desc, body) {
		t.Error("a fresh export does not hash equal to its own header from either the raw or the parsed inputs")
	}
	// The length lint certifies is the exported length: at-limit plus a
	// trailing space is still at limit.
	atLimit := "Use when " + strings.Repeat("x", MaxDescriptionLen-9) + " "
	if got := rules(Lint(declaring("tasks:a", atLimit, nil))); got["skill-description-too-long"] != "" {
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
	if got := rules(Lint(declaring("tasks:a", desc, nil))); got["skill-description-too-long"] != "" {
		t.Errorf("at-limit description flagged as too long: %v", got)
	}
}

func TestLintEmptyStoredNameIsJudged(t *testing.T) {
	// The trap survives D12 even though the rule that found it did not:
	// {"name": ""} is a name that is PRESENT and empty, and it must be judged
	// rather than read as absent. Under D12 the finding is skill-name-missing,
	// which is the same defect the author needs told about.
	n := declaring("tasks:a", "Use when x", map[string]any{"name": ""})
	if got := rules(Lint(n)); got["skill-name-missing"] != SevError {
		t.Errorf("empty stored name not judged: %v", got)
	}
}

func TestPreambleIsOneOfEachAndKeepsWhitespaceLines(t *testing.T) {
	// Copilot on #589, round 3: a body that starts with the exact human line,
	// or with a whitespace-only line, is body — Render keeps both, so the
	// parser must too, or a fresh export fails its own hash check.
	for _, body := range []string{
		humanLine + "\n\n# Body\n",
		"<!-- hadron-skill source=hrn:node:a:b:tasks:x hash=0123456789abcdef -->\n# Body\n",
		"  \n# Body after a whitespace-only line\n",
		"  ---\nname: x\n---\nindented rule, not frontmatter\n",
	} {
		file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", "Use when x", body)
		if err != nil {
			t.Fatal(err)
		}
		f, err := ParseFile([]byte(file))
		if err != nil {
			t.Fatal(err)
		}
		if f.Body != NormalizeBody(body) {
			t.Errorf("body round trip:\n got %q\nwant %q", f.Body, NormalizeBody(body))
		}
		if f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) {
			t.Errorf("%q: fresh export does not hash equal to its header", body)
		}
	}
	// An indented --- block is content on disk, so lint treats it as content.
	n := declaring("tasks:a", "Use when x", nil)
	n.Content = "  ---\nname: x\n---\nbody\n"
	if got := rules(Lint(n)); got["skill-content-has-frontmatter"] != "" {
		t.Errorf("indented rule flagged as frontmatter: %v", got)
	}
}

func TestProvenanceNeedsANodeURNAndAHexHash(t *testing.T) {
	// Codex round 8 / Copilot round 3: a header naming another entity kind, or
	// a malformed hash, is not ours — the file stays foreign.
	for _, pre := range []string{
		"<!-- Generated from hrn:mem:acme.com:kb -->",
		"<!-- hadron-skill source=not-a-node hash=0123456789abcdef -->",
		"<!-- hadron-skill source=hrn:node:a:b:tasks:x hash=xyz -->",
		"<!-- hadron-skill source=hrn:app:a:b hash=0123456789abcdef -->",
	} {
		file := "---\nname: theirs\ndescription: Use when x\n---\n\n" + pre + "\n\n# Body\n"
		f, err := ParseFile([]byte(file))
		if err != nil {
			t.Fatal(err)
		}
		if f.Source != "" || f.Hash != "" {
			t.Errorf("%q classified as generated: %+v", pre, f)
		}
		if !strings.HasPrefix(f.Body, pre) {
			t.Errorf("%q not kept in the body: %q", pre, f.Body)
		}
	}
	// And the real thing still parses.
	for _, pre := range []string{
		"<!-- Generated from hrn:node:hadronmemory.com:core:tasks:mint-spec -->",
	} {
		f, _ := ParseFile([]byte("---\nname: x\ndescription: Use when x\n---\n\n" + pre + "\n\n# Body\n"))
		if f.Source == "" {
			t.Errorf("%q not recognised as legacy provenance", pre)
		}
	}
}

func TestForeignBodyStartingWithHumanLineKeepsIt(t *testing.T) {
	// Copilot on #589, round 4: without a provenance line before it, the
	// human line is body.
	f, err := ParseFile([]byte("---\nname: theirs\ndescription: Use when x\n---\n\n" + humanLine + "\n\n# Body\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "" || !strings.HasPrefix(f.Body, humanLine) {
		t.Errorf("foreign human-line body mishandled: %+v", f)
	}
}

func TestIndentedProvenanceLookalikeIsBody(t *testing.T) {
	// Render never indents; an indented provenance-shaped line is content.
	pre := "  <!-- hadron-skill source=hrn:node:a:b:tasks:x hash=0123456789abcdef -->"
	f, err := ParseFile([]byte("---\nname: theirs\ndescription: Use when x\n---\n\n" + pre + "\n\n# Body\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "" || !strings.HasPrefix(f.Body, pre) {
		t.Errorf("indented lookalike claimed: %+v", f)
	}
}

func TestSourceIsFlatV2Only(t *testing.T) {
	// Provenance names a node in the one form exports write; a v1 or urn:
	// spelling is not recognised (no v1 support in this surface — Holger,
	// 2026-09-16), so such a file stays foreign and the line stays in its body.
	ok := "<!-- Generated from hrn:node:hadronmemory.com:core:tasks:mint-spec -->"
	f, err := ParseFile([]byte("---\nname: x\ndescription: Use when x\n---\n\n" + ok + "\n\n# Body\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "hrn:node:hadronmemory.com:core:tasks:mint-spec" {
		t.Errorf("flat v2 source not recognised: %+v", f)
	}
	for _, pre := range []string{
		"<!-- Generated from hadronmemory.com::core::tasks:mint-spec -->",
		"<!-- Generated from hrn:node:hadronmemory.com::core::tasks:mint-spec -->",
		"<!-- Generated from urn:node:hadronmemory.com:core:tasks:mint-spec -->",
		"<!-- hadron-skill source=hrn:node:hadronmemory.com::core::tasks:mint-spec hash=0123456789abcdef -->",
	} {
		g, err := ParseFile([]byte("---\nname: x\ndescription: Use when x\n---\n\n" + pre + "\n\n# Body\n"))
		if err != nil {
			t.Fatal(err)
		}
		if g.Source != "" || !strings.HasPrefix(g.Body, pre) {
			t.Errorf("%q: v1 spelling recognised as provenance: %+v", pre, g)
		}
	}
}

func TestExtraFrontmatterKeysAreKeptAsALocalEdit(t *testing.T) {
	// Codex on #589, round 11: a key a user adds must not vanish from the
	// parse, or a stale export could overwrite it without --force.
	file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", "Use when x", "# Body")
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Extra) != 0 {
		t.Errorf("fresh export reports extra keys: %v", f.Extra)
	}
	edited := strings.Replace(file, "description:", "compatibility: Claude Code 2.x\ndescription:", 1)
	g, err := ParseFile([]byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if g.Extra["compatibility"] != "Claude Code 2.x" {
		t.Errorf("added frontmatter key not retained: %v", g.Extra)
	}
}

func TestNonStringFrontmatterIsRefused(t *testing.T) {
	for _, fm := range []string{"name: 123\ndescription: Use when x", "name: x\ndescription: [a, b]", "name: x\ndescription: true"} {
		if _, err := ParseFile([]byte("---\n" + fm + "\n---\n\n# Body\n")); err == nil {
			t.Errorf("%q: non-string frontmatter value coerced instead of refused", fm)
		}
	}
}

func TestParsedDescriptionIsNormalized(t *testing.T) {
	f, err := ParseFile([]byte("---\nname: x\ndescription: \" Use when x \"\n---\n\n# Body\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Description != "Use when x" {
		t.Errorf("parsed description not normalized: %q", f.Description)
	}
}

func TestIncompleteMachineHeaderIsBody(t *testing.T) {
	// A hadron-skill comment without BOTH source= and hash= is not provenance.
	for _, first := range []string{"<!-- hadron-skill example=yes -->", "<!-- hadron-skill source=hrn:node:a:b:c -->"} {
		file, err := Render("hadron-x", "hrn:node:a:b:tasks:x", "Use when x", first+"\n\n# Body\n")
		if err != nil {
			t.Fatal(err)
		}
		f, err := ParseFile([]byte(file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(f.Body, first) || f.Hash != Hash(f.Source, f.Name, f.Description, f.Body) {
			t.Errorf("%q: swallowed or mis-hashed: body=%q", first, f.Body)
		}
		// And standing alone in the preamble it does not make the file generated.
		lone := "---\nname: theirs\ndescription: Use when x\n---\n\n" + first + "\n\n# Body\n"
		g, _ := ParseFile([]byte(lone))
		if g.Source != "" || g.Hash != "" {
			t.Errorf("%q alone classified as generated: %+v", first, g)
		}
	}
}

func TestLintNonStringNameOrDescriptionIsMalformed(t *testing.T) {
	// Copilot on #589: a numeric hand-set name was silently ignored, escaping
	// the rule that a stored name must equal the derived one.
	for _, props := range []map[string]any{
		{"skill": map[string]any{"description": "Use when x", "name": 123}},
		{"skill": map[string]any{"description": 42}},
	} {
		n := Node{URN: "u", Loc: "tasks:x", IsRunnable: true, Content: "b", Properties: props}
		if got := rules(Lint(n)); got["skill-declaration-malformed"] != SevError {
			t.Errorf("props %v: want malformed error, got %v", props, got)
		}
	}
}

func TestLintMalformedKeyIsReportedEvenBesideAValidOne(t *testing.T) {
	// Mid-migration: the new key was typo'd while the legacy one still works.
	// The node is declared (via claudeSkill) AND carries a malformed key.
	n := Node{URN: "u", Loc: "tasks:x", IsRunnable: true, Content: "b",
		Properties: map[string]any{"skill": "oops", "claudeSkill": map[string]any{"description": "Use when x"}}}
	got := rules(Lint(n))
	if got["skill-declaration-malformed"] != SevError || got["skill-legacy-key"] != SevWarning {
		t.Errorf("want malformed error AND legacy warning, got %v", got)
	}
}

// --- D12: the exports shape, per-host keying, and the enable switch ---

func TestExportsIsKeyedByHostAndWinsOverRetiredKeys(t *testing.T) {
	host := map[string]any{"name": "hadron-new", "description": "Use when new"}
	props := map[string]any{
		ExportsKey:      map[string]any{HostClaudeSkill: host},
		"skill":         map[string]any{"name": "hadron-mid", "description": "Use when mid"},
		"claudeSkill":   map[string]any{"name": "hadron-old", "description": "Use when old"},
		"somethingElse": "ignored",
	}
	d, ok := Declared(props)
	if !ok {
		t.Fatal("exports.claudeSkill not read as a declaration")
	}
	if d.Key != ExportsKey+"."+HostClaudeSkill || d.Name != "hadron-new" {
		t.Fatalf("exports did not win over the retired keys: %+v", d)
	}
	if d.Host != HostClaudeSkill {
		t.Errorf("host not recorded: %q", d.Host)
	}
	// Both strays are named in ONE finding, so the author is told to remove both.
	n := Node{URN: "u", Loc: "tasks:a", MemoryURN: "m", IsRunnable: true, Content: "b", Properties: props}
	var msg string
	for _, f := range Lint(n) {
		if f.Rule == "skill-legacy-key" {
			msg = f.Message
		}
	}
	for _, want := range []string{"properties.skill", "properties.claudeSkill"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stray %s not named in %q", want, msg)
		}
	}
}

func TestAnotherHostIsDeclaredButNotValidatedHere(t *testing.T) {
	// A node declaring ONLY a future host is still a declaration as far as the
	// discovery predicate is concerned, but this package validates the one host
	// whose renderer is specified. It must not be read as claudeSkill, and it
	// must not be reported as malformed.
	props := map[string]any{ExportsKey: map[string]any{
		"codex": map[string]any{"name": "hadron-x", "description": "Use when x"},
	}}
	if d, ok := Declared(props); ok {
		t.Fatalf("a codex-only declaration was read as claudeSkill: %+v", d)
	}
	if bad := Malformed(props); len(bad) != 0 {
		t.Errorf("a well-formed foreign host reported as malformed: %v", bad)
	}
}

func TestEnableDefaultsToEnabledAndFalseIsRecorded(t *testing.T) {
	// ABSENT means enabled: declaring an export IS the opt-in, and enable:false
	// is the explicit way to stand one down. EnableSet keeps the two apart for a
	// caller that needs to report which.
	d, _ := Declared(map[string]any{ExportsKey: map[string]any{
		HostClaudeSkill: map[string]any{"name": "hadron-a", "description": "Use when x"},
	}})
	if !d.Enable || d.EnableSet {
		t.Errorf("absent enable should be enabled-but-unset, got Enable=%v Set=%v", d.Enable, d.EnableSet)
	}

	for _, want := range []bool{true, false} {
		d, _ := Declared(map[string]any{ExportsKey: map[string]any{
			HostClaudeSkill: map[string]any{"name": "hadron-a", "description": "Use when x", "enable": want},
		}})
		if d.Enable != want || !d.EnableSet {
			t.Errorf("enable=%v not recorded: Enable=%v Set=%v", want, d.Enable, d.EnableSet)
		}
	}

	// A DISABLED declaration is still DECLARED — enable governs whether it is
	// exported, not whether it exists. If this ever returned false, a disabled
	// skill would be indistinguishable from an absent one and its file on disk
	// would linger forever with nothing able to name it.
	d, ok := Declared(map[string]any{ExportsKey: map[string]any{
		HostClaudeSkill: map[string]any{"name": "hadron-a", "description": "Use when x", "enable": false},
	}})
	if !ok || d.Enable {
		t.Errorf("a disabled declaration must still be declared: ok=%v Enable=%v", ok, d.Enable)
	}
}

func TestMalformedExportsPaths(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"exports not an object", map[string]any{ExportsKey: "yes"}, ExportsKey},
		{"host entry not an object", map[string]any{ExportsKey: map[string]any{HostClaudeSkill: true}}, ExportsKey + "." + HostClaudeSkill},
		{"non-string name", map[string]any{ExportsKey: map[string]any{HostClaudeSkill: map[string]any{"name": 123}}}, ExportsKey + "." + HostClaudeSkill + ".name"},
		{"non-string description", map[string]any{ExportsKey: map[string]any{HostClaudeSkill: map[string]any{"description": []any{"x"}}}}, ExportsKey + "." + HostClaudeSkill + ".description"},
		{"non-boolean enable", map[string]any{ExportsKey: map[string]any{HostClaudeSkill: map[string]any{"enable": "true"}}}, ExportsKey + "." + HostClaudeSkill + ".enable"},
		{"foreign host malformed too", map[string]any{ExportsKey: map[string]any{"codex": map[string]any{"enable": 1}}}, ExportsKey + ".codex.enable"},
	}
	for _, c := range cases {
		bad := Malformed(c.props)
		found := false
		for _, b := range bad {
			if b == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q among malformed, got %v", c.name, c.want, bad)
		}
	}
}

func TestMalformedHostPathsAreDeterministic(t *testing.T) {
	// Map iteration order is random in Go, so two hosts both malformed must
	// still report in a stable order — otherwise the finding list flaps between
	// runs and a --strict gate is nondeterministic.
	props := map[string]any{ExportsKey: map[string]any{
		"zulu":  true,
		"alpha": true,
		"mike":  true,
	}}
	want := []string{ExportsKey + ".alpha", ExportsKey + ".mike", ExportsKey + ".zulu"}
	for i := 0; i < 20; i++ {
		got := Malformed(props)
		if len(got) != len(want) {
			t.Fatalf("want %d malformed paths, got %v", len(want), got)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("unstable order: want %v, got %v", want, got)
			}
		}
	}
}
