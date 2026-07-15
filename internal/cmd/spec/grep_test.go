package spec

import (
	"regexp"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestParseGrepFields(t *testing.T) {
	cases := []struct {
		in            string
		content, abst bool
		wantErr       bool
	}{
		{"", true, true, false},
		{"content", true, false, false},
		{"body", true, false, false},
		{"abstract", false, true, false},
		{"ABSTRACT", false, true, false},
		{"tags", false, false, true},
	}
	for _, tc := range cases {
		c, a, err := parseGrepFields(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseGrepFields(%q): want error", tc.in)
			}
			continue
		}
		if err != nil || c != tc.content || a != tc.abst {
			t.Errorf("parseGrepFields(%q) = (%v,%v,%v), want (%v,%v,nil)", tc.in, c, a, err, tc.content, tc.abst)
		}
	}
}

func TestCompileMatcher(t *testing.T) {
	// Literal: metacharacters are inert.
	re, err := compileMatcher("a.b", false, false)
	if err != nil {
		t.Fatalf("literal compile: %v", err)
	}
	if re.MatchString("axb") {
		t.Error("literal 'a.b' must not match 'axb' (dot should be quoted)")
	}
	if !re.MatchString("a.b") {
		t.Error("literal 'a.b' should match 'a.b'")
	}
	// Regex: dot is a wildcard.
	re, _ = compileMatcher("a.b", true, false)
	if !re.MatchString("axb") {
		t.Error("regex 'a.b' should match 'axb'")
	}
	// Ignore-case.
	re, _ = compileMatcher("Foo", false, true)
	if !re.MatchString("foo") {
		t.Error("-i should fold case")
	}
	// Bad regex is a usage error, not a panic.
	if _, err := compileMatcher("a(", true, false); err == nil {
		t.Error("invalid --regex should error")
	}
}

func TestGrepField(t *testing.T) {
	body := "# Title\nh-read-node here\nno match\nsee h-read-node again\n"
	got := grepField("cor:api:010:01", "content", body, mustMatcher(t, "h-read-node"))
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d: %+v", len(got), got)
	}
	if got[0].Line != 2 || got[1].Line != 4 {
		t.Errorf("lines = %d,%d, want 2,4", got[0].Line, got[1].Line)
	}
	if got[0].Citation != "cor:api:010:01" || got[0].Field != "content" {
		t.Errorf("unexpected match meta: %+v", got[0])
	}
	if got[0].Text != "h-read-node here" {
		t.Errorf("text = %q, want the full line", got[0].Text)
	}
	// A CRLF line has its trailing \r trimmed.
	crlf := grepField("x", "content", "foo\r\nbar h-read-node\r\n", mustMatcher(t, "h-read-node"))
	if len(crlf) != 1 || crlf[0].Text != "bar h-read-node" {
		t.Errorf("CRLF trim failed: %+v", crlf)
	}
}

func mustMatcher(t *testing.T, pat string) *regexp.Regexp {
	t.Helper()
	re, err := compileMatcher(pat, false, false)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return re
}

func TestBuildReplacePattern(t *testing.T) {
	// Default: word-boundary on both ends (token starts/ends with word chars).
	old, rx := buildReplacePattern("h-read-node", false, true)
	if !rx || old != `\bh-read-node\b` {
		t.Errorf("word-boundary = (%q,%v), want (\\bh-read-node\\b,true)", old, rx)
	}
	// Metacharacters in a word-boundary literal are escaped.
	if old, _ := buildReplacePattern("a.b", false, true); old != `\ba\.b\b` {
		t.Errorf("escaped word-boundary = %q, want \\ba\\.b\\b", old)
	}
	// A non-word boundary char must NOT get a \b on that side (else \b never
	// matches and the replace silently no-ops): "@handle" anchors only the right.
	if old, _ := buildReplacePattern("@handle", false, true); old != `@handle\b` {
		t.Errorf("leading non-word: got %q, want @handle\\b (no leading \\b)", old)
	}
	// "node!" anchors only the left.
	if old, _ := buildReplacePattern("node!", false, true); old != `\bnode!` {
		t.Errorf("trailing non-word: got %q, want \\bnode! (no trailing \\b)", old)
	}
	// Plain literal (no boundary): passthrough, regex off.
	if old, rx := buildReplacePattern("h-read-node", false, false); rx || old != "h-read-node" {
		t.Errorf("plain literal = (%q,%v), want (h-read-node,false)", old, rx)
	}
	// Regex: passthrough, regex on; boundary flag ignored. Not pre-validated
	// (the server's JS engine is the source of truth), so even a Go-invalid
	// pattern passes through unchanged rather than erroring here.
	if old, rx := buildReplacePattern(`h-chat-(\w+)`, true, true); !rx || old != `h-chat-(\w+)` {
		t.Errorf("regex = (%q,%v), want (h-chat-(\\w+),true)", old, rx)
	}
	if old, rx := buildReplacePattern("a(", true, false); !rx || old != "a(" {
		t.Errorf("regex passthrough = (%q,%v), want (a(,true) — server validates", old, rx)
	}
}

func TestParseReplaceFields(t *testing.T) {
	both, err := parseReplaceFields("")
	if err != nil || len(both) != 2 {
		t.Fatalf("default should be content+abstract, got %v", both)
	}
	c, _ := parseReplaceFields("content")
	if len(c) != 1 || c[0] != gen.NodeTextFieldContent {
		t.Errorf("content-only = %v", c)
	}
	if _, err := parseReplaceFields("bogus"); err == nil {
		t.Error("unknown --field should error")
	}
}
