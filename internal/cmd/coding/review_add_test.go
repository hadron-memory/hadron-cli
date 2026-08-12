package coding

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestCheckLoc(t *testing.T) {
	cases := []struct {
		name    string
		root    string
		in      string
		want    string
		wantErr bool
	}{
		{"bare name", "review", "thin-resolver-field", "review:thin-resolver-field", false},
		{"already prefixed", "review", "review:thin-resolver-field", "review:thin-resolver-field", false},
		{"custom root", "checks", "foo", "checks:foo", false},
		{"trims space", "review", "  foo  ", "review:foo", false},
		{"nested loc rejected", "review", "graphql:thin-resolver", "", true},
		{"empty rejected", "review", "   ", "", true},
		{"prefix only rejected", "review", "review:", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkLoc(tc.root, tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("checkLoc(%q, %q) = %q, want an error", tc.root, tc.in, got)
				}
				if code := exitcode.FromError(err); code != exitcode.Usage {
					t.Errorf("a bad name is a usage error (2), got %d", code)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkLoc(%q, %q): %v", tc.root, tc.in, err)
			}
			if got != tc.want {
				t.Errorf("checkLoc(%q, %q) = %q, want %q", tc.root, tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeTrigger(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"stem prepended", "a resolver changes", "Applies when a resolver changes", false},
		{"stem kept", "Applies when a resolver changes", "Applies when a resolver changes", false},
		{"stem case-insensitive", "applies WHEN a resolver changes", "applies WHEN a resolver changes", false},
		{"whitespace collapsed", "  a  resolver\nchanges ", "Applies when a resolver changes", false},
		{"bare stem rejected", "Applies when", "", true},
		{"bare stem with space rejected", "Applies when   ", "", true},
		{"empty rejected", "  ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTrigger(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeTrigger(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeTrigger(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("normalizeTrigger(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The point of the command: what it writes must satisfy the linter that reads
// it. Any label rule that `create` stopped honouring would show up here as a
// finding on a freshly-created check.
func TestCreatedCheckLintsClean(t *testing.T) {
	loc, err := checkLoc("review", "thin-resolver-field")
	if err != nil {
		t.Fatalf("checkLoc: %v", err)
	}
	label, err := normalizeTrigger("adding or modifying a GraphQL resolver")
	if err != nil {
		t.Fatalf("normalizeTrigger: %v", err)
	}
	desc := "Resolver fields stay thin — applies when adding or modifying a resolver."
	in := reviewInput{
		Members: map[string]checkNode{loc: {
			Loc: loc, Name: checkName(loc), Description: desc,
			Content: scaffoldCheckBody(loc, label, "", desc), Tags: mergeTags(defaultCheckTags, nil),
		}},
		Edges: map[string]graphEdge{loc: {ID: "e1", Label: label, Other: loc}},
	}
	if findings := lintReview(in); len(findings) != 0 {
		t.Errorf("a freshly-created check must lint clean, got %+v", findings)
	}
}

// The Scope blockquote is the one scaffolded line that has to be real: the
// review runner reads only it before deciding to skip, and `review lint` quotes
// it when a label goes missing. It must therefore parse with bodyTrigger.
func TestScaffoldScopeIsMachineReadable(t *testing.T) {
	body := scaffoldCheckBody("review:thin-resolver-field",
		"Applies when adding or modifying a GraphQL resolver", "", "Resolver fields stay thin.")
	got, ok := bodyTrigger(body)
	if !ok {
		t.Fatalf("the scaffolded body must state a machine-readable scope:\n%s", body)
	}
	if !strings.Contains(got, "adding or modifying a GraphQL resolver") {
		t.Errorf("the scope should carry the trigger's condition, got %q", got)
	}
	if strings.Contains(got, "Applies when") {
		t.Errorf("the stem belongs in the label, not the Scope sentence: %q", got)
	}
	// --scope wins when given.
	body = scaffoldCheckBody("review:x", "Applies when a resolver changes", "the diff touches src/resolvers", "d")
	if got, _ := bodyTrigger(body); !strings.Contains(got, "src/resolvers") {
		t.Errorf("--scope should replace the trigger-derived sentence, got %q", got)
	}
}

func TestParseLinks(t *testing.T) {
	got, err := parseLinks([]string{"conventions:output-contract", "findings:x=documented-by", " spaced = label "})
	if err != nil {
		t.Fatalf("parseLinks: %v", err)
	}
	want := []newLink{
		{Ref: "conventions:output-contract", Label: defaultLinkLabel},
		{Ref: "findings:x", Label: "documented-by"},
		{Ref: "spaced", Label: "label"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseLinks returned %d links, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, err := parseLinks([]string{"=label"}); err == nil {
		t.Error("a --link with no ref must be a usage error")
	}
}

func TestMergeTags(t *testing.T) {
	got := mergeTags(defaultCheckTags, []string{"json", "REVIEW", "contract", "  "})
	want := []string{"review", "review-criteria", "contract", "json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mergeTags = %v, want %v (defaults first, extras sorted, case-insensitively deduped)", got, want)
	}
}
