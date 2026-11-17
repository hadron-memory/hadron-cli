package team

import (
	"bytes"
	"strings"
	"testing"
)

// The Unix suggestion is shell-quoted, so a cursor that passes the display
// fence unchanged still cannot become extra shell syntax.
func TestMetadataCursorHintIsShellQuoted(t *testing.T) {
	var b bytes.Buffer
	if err := writeHostCursorHint(&b, "c; echo x' $HOME", "linux"); err != nil {
		t.Fatal(err)
	}
	if want := `--after 'c; echo x'\'' $HOME'`; !strings.Contains(b.String(), want) {
		t.Fatalf("missing %q: %q", want, b.String())
	}
}

func TestMetadataWindowsGuidance(t *testing.T) {
	for _, value := range []string{"bad\ncursor", strings.Repeat("界", 257)} {
		var b bytes.Buffer
		if err := writeHostCursorHint(&b, value, "windows"); err != nil {
			t.Fatal(err)
		}
		if got := b.String(); !strings.Contains(got, "suggestion omitted") || !strings.Contains(got, "--json") || strings.Contains(got, "pass the original") {
			t.Fatal(got)
		}
		if got := roleDefinitionHint(value, "windows"); !strings.Contains(got, "command omitted") || strings.Contains(got, "role create") {
			t.Fatal(got)
		}
	}
	var b bytes.Buffer
	if err := writeHostCursorHint(&b, "cursor; x", "windows"); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, `cursor "cursor; x"`) || strings.Contains(got, "continue with") {
		t.Fatal(got)
	}
	if got := roleDefinitionHint("role; x", "windows"); strings.Contains(got, "role create") || !strings.Contains(got, "persona role") {
		t.Fatal(got)
	}
}
