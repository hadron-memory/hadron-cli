package spec

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestEditorArgv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := editorArgv(); len(got) != 1 || got[0] != "vi" {
		t.Errorf("default = %v, want [vi]", got)
	}

	t.Setenv("EDITOR", "nano -w")
	if got := editorArgv(); len(got) != 2 || got[0] != "nano" || got[1] != "-w" {
		t.Errorf("EDITOR with args = %v, want [nano -w]", got)
	}

	// VISUAL takes precedence over EDITOR.
	t.Setenv("VISUAL", "code --wait")
	got := editorArgv()
	if len(got) != 2 || got[0] != "code" || got[1] != "--wait" {
		t.Errorf("VISUAL should win, got %v", got)
	}
}

func TestLaunchEditorNonTerminal(t *testing.T) {
	// output.Test() streams are buffers, not a TTY — launchEditor must refuse
	// rather than try to spawn an editor.
	io, _, _ := output.Test()
	if _, err := launchEditor(io, "body"); exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("non-terminal launchEditor should be Usage, got %v", err)
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"a\nb\nc\n", 3},
	}
	for _, c := range cases {
		if got := countLines(c.in); got != c.want {
			t.Errorf("countLines(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
