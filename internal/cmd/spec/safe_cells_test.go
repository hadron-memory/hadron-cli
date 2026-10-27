package spec

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Alternate/nested table branches get the same boundary as command fixtures.
// The report's JSON DTO must not be changed by rendering its human table.
func TestSlice3AlternateSafeTables(t *testing.T) {
	for _, v := range []string{"a\nb\x1b[31mc\u0085\x7f\u202e\u200b\u2028\u2029", strings.Repeat("日", 1000), "café e\u0301 😀", ""} {
		specs := []specDTO{{Citation: v, Name: v, MemoryID: "m1", MemoryURN: v, ApprovalStatus: &specApprovalDTO{State: v}}, {Citation: v, Name: v, MemoryID: "m2", MemoryURN: v}}
		refs := []referenceDTO{{SourceLoc: v, Kind: v, Field: v, TargetLoc: v, Reason: v, Text: v}}
		rn := renumberDTO{From: "a:1", To: "a:2", Rewrites: []renumberRewriteDTO{{Loc: v, Status: v, Fields: []string{v}, Error: v}}, TextCitations: []textCitationDTO{{SourceLoc: v, Field: v, Citation: v, Excerpt: v}}}
		mint := mintDTO{Memory: "memory", Blockers: []mintFindingDTO{{Loc: v, Kind: v, Rule: v, TargetLoc: v, Message: v}}}
		replace := specReplaceResultDTO{Results: []specReplaceNodeDTO{{Citation: v, Fields: []specReplaceFieldDTO{{Field: v, Matches: 1}}}}, Lint: []lintFindingDTO{{Citation: v, Severity: "error", Rule: "rule", Message: v}}}
		cases := []struct {
			name   string
			dto    any
			render func(io.Writer) error
		}{
			{"list multiple approval", specs, func(w io.Writer) error { return writeSpecListTable(w, specs) }},
			{"find multiple", specs, func(w io.Writer) error { return writeSpecTable(w, specs) }},
			{"find single", specs[:1], func(w io.Writer) error { return writeSpecTable(w, specs[:1]) }},
			{"references", refs, func(w io.Writer) error { return renderReferences(w, refs, false) }},
			{"references problem", refs, func(w io.Writer) error { return renderReferences(w, refs, true) }},
			{"renumber nested", rn, func(w io.Writer) error { return renderRenumber(w, rn) }},
			{"mint blockers", mint, func(w io.Writer) error { return renderMint(w, mint) }},
			{"replace relint", replace, func(w io.Writer) error { return renderSpecReplaceReport(w, replace) }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before, _ := json.Marshal(tc.dto)
				var b bytes.Buffer
				if err := tc.render(&b); err != nil {
					t.Fatal(err)
				}
				after, _ := json.Marshal(tc.dto)
				if !bytes.Equal(before, after) {
					t.Fatal("text render changed original JSON DTO")
				}
				text := b.String()
				for _, r := range text {
					if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)) {
						t.Errorf("hidden rune %U in table: %q", r, text)
						break
					}
				}
				if strings.Contains(v, "\n") && strings.Contains(text, "a\nb") {
					t.Errorf("forged row: %q", text)
				}
				if utf8.RuneCountInString(v) > 256 && (!strings.Contains(text, "…") || strings.Contains(text, strings.Repeat("日", 256))) {
					t.Errorf("unbounded table cell: %q", text)
				}
				for _, line := range strings.Split(text, "\n") {
					for _, cell := range regexp.MustCompile(` {2,}`).Split(line, -1) {
						if utf8.RuneCountInString(cell) > 256 {
							t.Errorf("cell exceeds256 runes: %q", cell)
						}
					}
				}
			})
		}
	}
}

// Mint metadata strings never enter the MINTED column: it is a closed label
// selected from the boolean, unlike approvalStatus.state's remote string.
func TestSlice3MintLabelsAreClosed(t *testing.T) {
	hostile := "remote\n\x1b[31m\u202e" + strings.Repeat("日", 1000)
	for _, tc := range []struct {
		status *specMintDTO
		want   string
	}{
		{nil, "unknown"},
		{&specMintDTO{Minted: true, MintedAt: &hostile, MintedByInfo: &hostile, Hash: &hostile}, "MINTED"},
		{&specMintDTO{Minted: false, MintedAt: &hostile, MintedByInfo: &hostile, Hash: &hostile}, "UNMINTED"},
	} {
		if got := specMintLabel(tc.status); got != tc.want {
			t.Fatalf("label = %q, want %q", got, tc.want)
		}
		var b bytes.Buffer
		if err := writeSpecListTable(&b, []specDTO{{Citation: "msg:010:02", Name: "Title", MintStatus: tc.status}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), hostile) {
			t.Fatal("mint metadata leaked into list")
		}
	}
}

// The command supplies these field literals; neither column is copied from a
// server status or loc. Remote text affects DETAIL and CITATION instead.
func TestSlice3ToolFindingColumnsAreLocal(t *testing.T) {
	for _, field := range []string{"content", "abstract"} {
		findings := checkToolField("remote\nloc", field, "safe\nhadron_not_registered\n", map[string]bool{}, map[string]bool{})
		if len(findings) != 1 {
			t.Fatalf("findings: %+v", findings)
		}
		f := findings[0]
		if f.Kind != "unregistered-tool" || f.Field != field || f.Line != 2 || f.Citation != "remote\nloc" {
			t.Fatalf("local column derivation: %+v", f)
		}
	}
	if kindUnavailable != "unavailable" {
		t.Fatal("unavailable label changed")
	}
}
