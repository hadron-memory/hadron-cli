package nodedoc

import (
	"path/filepath"
	"testing"
)

func TestNodeFilePath(t *testing.T) {
	root := "/tmp/kb"
	cases := []struct {
		loc, want string
	}{
		{"", filepath.Join(root, "README.md")},
		{"a", filepath.Join(root, "a.md")},
		{"a:b:c", filepath.Join(root, "a", "b", "c.md")},
		{"msg:010:02", filepath.Join(root, "msg", "010", "02.md")},
	}
	for _, c := range cases {
		got, err := NodeFilePath(root, c.loc)
		if err != nil {
			t.Errorf("NodeFilePath(%q): unexpected err %v", c.loc, err)
			continue
		}
		if got != c.want {
			t.Errorf("NodeFilePath(%q) = %q, want %q", c.loc, got, c.want)
		}
	}

	// Malformed locs and path-traversal attempts must be rejected — a segment
	// like "../escape" or "a/b" would otherwise let filepath.Join walk outside
	// the output tree.
	for _, bad := range []string{"a::b", "a:", ":b", "a:..:b", "a:.:b", "../escape", "a/../b", `a\b`, "/abs", "a:../b"} {
		got, err := NodeFilePath(root, bad)
		if err == nil {
			t.Errorf("NodeFilePath(%q) = %q: expected error for unsafe loc", bad, got)
		}
	}
}
