package team

import (
	"bytes"
	"strings"
	"testing"
)

func TestMetadataApplyArgumentFence(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		for _, bad := range []string{"bad\nargument", strings.Repeat("界", 257)} {
			for index := range 3 {
				args := []string{"app", "server", "proof"}
				args[index] = bad
				var b bytes.Buffer
				if err := writeSwitchoverApplyHint(&b, args[0], args[1], args[2], platform); err != nil {
					t.Fatal(err)
				}
				got := b.String()
				if !strings.Contains(got, "command omitted") || !strings.Contains(got, "--json") || strings.Contains(got, "apply --app") || strings.Contains(got, "Apply with") {
					t.Fatalf("%s argument %d: %q", platform, index, got)
				}
			}
		}
	}
	var b bytes.Buffer
	if err := writeSwitchoverApplyHint(&b, "a; echo x", "s' $HOME", "p", "linux"); err != nil {
		t.Fatal(err)
	}
	if want := "--app 'a; echo x' --server 's'\\'' $HOME' --proof p"; !strings.Contains(b.String(), want) {
		t.Fatalf("missing %q: %q", want, b.String())
	}
	b.Reset()
	if err := writeSwitchoverApplyHint(&b, "app", "server", "proof", "windows"); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, `Apply with --app "app"`) || strings.Contains(got, "hadron team") {
		t.Fatal(got)
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
