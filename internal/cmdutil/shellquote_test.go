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
