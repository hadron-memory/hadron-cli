package cmdutil

import (
	"os/exec"
	"testing"
)

func TestShellQuoteSurvivesTheShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh")
	}
	for _, s := range []string{"", "plain", "hrn:mem:a.com:b", "two words", `$HOME`, "`id`", `$(x)`,
		`isn't`, `"q"`, `back\slash`, "tab\there", "line\nbreak", "!event", "a*b?[c]", "~user", "x=1;y|z&"} {
		cmd := exec.Command(sh, "-c", `printf '%s' `+ShellQuote(s))
		cmd.Env = []string{"HOME=/expanded", "PATH=/nonexistent"}
		out, err := cmd.Output()
		if err != nil || string(out) != s {
			t.Errorf("ShellQuote(%q) = %s gave %q (err %v)", s, ShellQuote(s), out, err)
		}
	}
	if ShellQuote("msg:010:02") != "msg:010:02" {
		t.Error("a plain citation stays bare")
	}
}

// A direct pin of the quoting itself, no shell spawned (cli#3024): a metacharacter-bearing
// value must come out as ONE single-quoted word with every ' escaped the POSIX way, and a
// word no shell treats specially must stay bare. The survive-the-shell test above proves the
// contract end to end but skips where no POSIX sh exists; this pin stands alone, so the
// one-word guarantee holds wherever ShellQuote is relied on (e.g. the host-list continue hint).
func TestShellQuotePinsOneWordDirectly_3024(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"hrn:app:acme.com:ops", "hrn:app:acme.com:ops"},
		{"https://srv.example/?a=b&c=d", `'https://srv.example/?a=b&c=d'`},
		{"two words", `'two words'`},
		{"it's", `'it'\''s'`},
		{`$(x)`, `'$(x)'`},
		{"", `''`},
	} {
		if got := ShellQuote(tc.in); got != tc.want {
			t.Errorf("ShellQuote(%q) = %q, want the one word %q", tc.in, got, tc.want)
		}
	}
}
