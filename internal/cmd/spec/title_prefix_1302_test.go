package spec

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1302 item 1: name composition and the new/edit copied-prefix guards read
// one canonical separator, so they cannot drift apart. The literals below are
// the spellings shipped before the refactor (#1148, cli#810): composed names
// and the refused prefix stay byte-identical. The refusal text gained only the
// corrected-title hint (#1302 item 2).
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

	const refusal = `--title is the text AFTER the citation: "qa:api:010:04 — " is added automatically, so drop it from the value: --title 'Burst allowance'`
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

// #1302 item 2: both guards end the refusal with the corrected title to retry
// with — the trimmed remainder, with any repeated copied prefix stripped too,
// so following the hint is never refused again.
func TestSpecCopiedPrefixCorrectedTitleHint(t *testing.T) {
	const loc = "qa:api:010:04"
	for _, tc := range []struct{ title, want string }{
		{"qa:api:010:04 — Burst allowance", "Burst allowance"},
		{"  qa:api:010:04 —   Burst allowance  ", "Burst allowance"},
		{"qa:api:010:04 — qa:api:010:04 — Burst allowance", "Burst allowance"},
		{"qa:api:010:04 — W4 — 7d check-in", "W4 — 7d check-in"},
		{"qa:api:010:04 — Send", "Send"}, // a safe single word stays bare
	} {
		want := `--title is the text AFTER the citation: "qa:api:010:04 — " is added automatically, so drop it from the value: --title ` + cmdutil.ShellQuote(tc.want)
		for name, err := range map[string]error{
			"new":  refuseNewTitlePrefix(tc.title, loc),
			"edit": validateEditTitle(tc.title, loc),
		} {
			if exitcode.FromError(err) != exitcode.Usage || err.Error() != want {
				t.Errorf("%s(%q) = %v, want %q", name, tc.title, err, want)
			}
			if refuseCopiedNamePrefix(tc.want, loc) != nil {
				t.Errorf("%s: the suggested title %q is itself refused", name, tc.want)
			}
		}
	}
}

// #1376 (Baloo P2): the hint is a shell fragment people paste, so the
// corrected title is quoted for a POSIX shell (cmdutil.ShellQuote), never Go-quoted: a
// pasted $HOME, $(...), backquote or quote must reach --title unchanged.
// Each case is round-tripped through a real sh.
func TestSpecCopiedPrefixHintIsShellSafe(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	const loc = "qa:api:010:04"
	for _, rest := range []string{"Cost $HOME", "Run $(touch pwned) now", "Back `id` tick", `it's "quoted"`, "multi\nline", `back\slash`} {
		err := refuseCopiedNamePrefix(specNamePrefix(loc)+rest, loc)
		if err == nil {
			t.Fatalf("%q not refused", rest)
		}
		_, word, ok := strings.Cut(err.Error(), "drop it from the value: --title ")
		if !ok {
			t.Fatalf("no hint in %q", err)
		}
		// In a scratch dir, so a broken quote's $(touch pwned) lands nowhere.
		cmd := exec.Command(sh, "-c", "printf %s "+word)
		cmd.Dir = t.TempDir()
		out, xerr := cmd.Output()
		if xerr != nil || string(out) != rest {
			t.Errorf("hint %s gives the shell %q (%v), want %q", word, out, xerr, rest)
		}
	}
}

// The guard never yields an empty corrected title: the prefix ends in a space
// and the title is compared trimmed, so a prefix-only title ("<loc> — ", with
// or without the space) does not match and is not refused here. That is the
// degenerate spelling #1302 leaves to a spec-owner ruling; this pins today's
// shipped contract, not a choice.
func TestSpecCopiedPrefixOnlyTitleIsTheRulingFirstCase(t *testing.T) {
	const loc = "qa:api:010:04"
	for _, title := range []string{"qa:api:010:04 — ", "qa:api:010:04 —", "  qa:api:010:04 —  "} {
		if err := refuseCopiedNamePrefix(title, loc); err != nil {
			t.Errorf("%q: %v", title, err)
		}
	}
}
