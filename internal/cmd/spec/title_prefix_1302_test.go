package spec

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1302 item 1: name composition and the new/edit copied-prefix guards read
// one canonical separator, so they cannot drift apart. The literals below are
// the spellings shipped before the refactor (#1148, cli#810): composed names,
// the refused prefix and the refusal text stay byte-identical.
func TestSpecCanonicalPrefixCompositionAndRefusal(t *testing.T) {
	const loc = "qa:api:010:04"
	if got := specNamePrefix(loc); got != "qa:api:010:04 — " {
		t.Fatalf("specNamePrefix = %q", got)
	}
	for _, title := range []string{"Burst allowance", "W4 — 7d check-in", "qa:api:010:03 — Related rule"} {
		want := "qa:api:010:04 — " + title
		if got := specNameAt(loc, title); got != want || got != specNamePrefix(loc)+title {
			t.Errorf("specNameAt(%q) = %q, want %q", title, got, want)
		}
	}
	if got := specName(Citation{Module: "msg", Feature: "010", Rule: "01"}, "Send"); got != "msg:010:01 — Send" {
		t.Errorf("specName = %q", got)
	}

	const refusal = `--title is the text AFTER the citation: "qa:api:010:04 — " is added automatically, so drop it from the value`
	guards := map[string]func(title string) error{
		"new":  func(title string) error { return refuseNewTitlePrefix(title, loc) },
		"edit": func(title string) error { return validateEditTitle(title, loc) },
		// spec edit trims the stored citation before composing the prefix.
		"edit padded citation": func(title string) error { return validateEditTitle(title, "  "+loc+" ") },
	}
	for name, guard := range guards {
		for _, title := range []string{specNameAt(loc, "Burst allowance"), "  qa:api:010:04 — Burst allowance  "} {
			err := guard(title)
			if exitcode.FromError(err) != exitcode.Usage || err.Error() != refusal {
				t.Errorf("%s(%q) = %v, want the Usage refusal %q", name, title, err, refusal)
			}
		}
		// Plain titles, a different citation and a longer loc are not the
		// copied prefix, and pass unchanged.
		for _, title := range []string{"Burst allowance", "qa:api:010:03 — Related rule", "qa:api:010:040 — Related rule", "qa:api:010:04 Burst"} {
			if err := guard(title); err != nil {
				t.Errorf("%s(%q) refused a title that is not the copied prefix: %v", name, title, err)
			}
		}
	}
}
