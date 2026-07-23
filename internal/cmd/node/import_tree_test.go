package node

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugifyAtom(t *testing.T) {
	cases := []struct{ in, want string }{
		{"setup", "setup"},
		{"My Notes", "my-notes"},
		{"README.md", "readme.md"},
		{"weird  spaces\tand/slashes", "weird-spaces-and-slashes"},
		{"--leading-and-trailing--", "leading-and-trailing"},
		{"_underscored_", "underscored"},
		{"Über café", "ber-caf"}, // non-ASCII drops to boundaries
		{"...", "n"},             // only illegal/boundary chars → defensive fallback
		{"", "n"},
	}
	for _, c := range cases {
		if got := slugifyAtom(c.in); got != c.want {
			t.Errorf("slugifyAtom(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 64-char cap, still alnum-bounded.
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	if got := slugifyAtom(string(long)); len(got) != 64 {
		t.Errorf("slugifyAtom should cap at 64, got len %d", len(got))
	}
}

func TestStripExt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"setup.md", "setup"},
		{"archive.tar.gz", "archive.tar"},
		{"noext", "noext"},
		{".gitignore", ".gitignore"}, // whole name is the extension
	}
	for _, c := range cases {
		if got := stripExt(c.in); got != c.want {
			t.Errorf("stripExt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsText(t *testing.T) {
	if !isText([]byte("plain text\n")) {
		t.Error("plain text should be text")
	}
	if !isText(nil) {
		t.Error("empty should count as text")
	}
	if isText([]byte{0x89, 0x50, 0x00, 0x01}) {
		t.Error("NUL-containing bytes should be binary")
	}
	if isText([]byte{0xff, 0xfe, 0xfd}) {
		t.Error("invalid UTF-8 should be binary")
	}
}

// planDir is the whole walk→plan step (no network): it exercises README fold,
// binary/hidden skips, collision suffixing, and the branch/leaf tree shape.
func TestPlanDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "docs")
	mustWrite(t, filepath.Join(root, "README.md"), "root landing")
	mustWrite(t, filepath.Join(root, "how-to", "setup.md"), "the setup")
	mustWrite(t, filepath.Join(root, "how-to", "setup.txt"), "also setup")       // collides → setup-2
	mustWrite(t, filepath.Join(root, ".secret"), "nope")                         // hidden
	mustWriteBytes(t, filepath.Join(root, "logo.png"), []byte{0x89, 0x00, 0x01}) // binary

	res := &planResult{skipped: []skipEntry{}, collisions: []collisionEntry{}}
	p := &planner{o: treeImportOpts{maxFileSize: 1 << 20}, res: res}
	node, err := p.planDir(root, "", "docs", "docs")
	if err != nil {
		t.Fatalf("planDir: %v", err)
	}

	// The root branch folds its README into content, not a child.
	if node.kind != "branch" || node.loc != "docs" {
		t.Fatalf("root should be branch at loc docs, got %+v", node)
	}
	if node.content != "root landing" {
		t.Errorf("README should fold into the branch content, got %q", node.content)
	}
	// Only the how-to subdir survives as a child (secret hidden, png binary,
	// README folded).
	if len(node.children) != 1 || node.children[0].loc != "docs:how-to" {
		t.Fatalf("root should have one child docs:how-to, got %+v", node.children)
	}
	howto := node.children[0]
	if len(howto.children) != 2 {
		t.Fatalf("how-to should have 2 leaves, got %d", len(howto.children))
	}
	// Sorted order: setup.md → setup, setup.txt → setup-2 (collision suffix).
	if howto.children[0].loc != "docs:how-to:setup" || howto.children[0].name != "setup.md" {
		t.Errorf("first leaf should be setup.md at docs:how-to:setup, got %+v", howto.children[0])
	}
	if howto.children[1].loc != "docs:how-to:setup-2" {
		t.Errorf("colliding leaf should be renamed to setup-2, got %q", howto.children[1].loc)
	}
	if howto.children[1].kind != "leaf" || howto.children[1].content != "also setup" {
		t.Errorf("leaf content/kind wrong, got %+v", howto.children[1])
	}

	// Skips and collisions are reported with root-relative paths.
	if !hasSkip(res.skipped, ".secret", "hidden") {
		t.Errorf("hidden file should be reported skipped, got %+v", res.skipped)
	}
	if !hasSkip(res.skipped, "logo.png", "binary") {
		t.Errorf("binary file should be reported skipped, got %+v", res.skipped)
	}
	if len(res.collisions) != 1 || res.collisions[0].Loc != "docs:how-to:setup-2" {
		t.Errorf("collision should be reported, got %+v", res.collisions)
	}
}

// A too-large file and an --exclude glob are both skipped with their reason,
// while --include prunes everything else.
func TestPlanDirFilters(t *testing.T) {
	root := filepath.Join(t.TempDir(), "src")
	mustWrite(t, filepath.Join(root, "keep.md"), "ok")
	mustWrite(t, filepath.Join(root, "drop.tmp"), "scratch")
	mustWrite(t, filepath.Join(root, "big.md"), "way too large for the cap")

	res := &planResult{skipped: []skipEntry{}, collisions: []collisionEntry{}}
	p := &planner{o: treeImportOpts{
		maxFileSize: 5,
		include:     []string{"**/*.md"},
		exclude:     []string{"**/*.tmp"},
	}, res: res}
	node, err := p.planDir(root, "", "src", "src")
	if err != nil {
		t.Fatalf("planDir: %v", err)
	}
	if len(node.children) != 1 || node.children[0].name != "keep.md" {
		t.Fatalf("only keep.md should survive, got %+v", node.children)
	}
	if !hasSkip(res.skipped, "drop.tmp", "excluded") {
		t.Errorf("drop.tmp should be excluded, got %+v", res.skipped)
	}
	if !hasSkip(res.skipped, "big.md", "too-large") {
		t.Errorf("big.md should be too-large, got %+v", res.skipped)
	}
}

// A literal `setup-2.md` beside two `setup.*` must not have its slot stolen by
// the renamed second `setup` — the suffix advances past the occupied atom.
func TestPlanDirCollisionAvoidsOccupied(t *testing.T) {
	root := filepath.Join(t.TempDir(), "d")
	mustWrite(t, filepath.Join(root, "setup-2.md"), "a")
	mustWrite(t, filepath.Join(root, "setup.md"), "b")
	mustWrite(t, filepath.Join(root, "setup.txt"), "c")

	res := &planResult{skipped: []skipEntry{}, collisions: []collisionEntry{}}
	p := &planner{o: treeImportOpts{maxFileSize: 1 << 20}, res: res}
	node, err := p.planDir(root, "", "d", "d")
	if err != nil {
		t.Fatalf("planDir: %v", err)
	}
	locs := map[string]bool{}
	for _, c := range node.children {
		if locs[c.loc] {
			t.Fatalf("duplicate loc assigned: %s (children %+v)", c.loc, node.children)
		}
		locs[c.loc] = true
	}
	// sorted: setup-2.md→setup-2, setup.md→setup, setup.txt→setup(taken)→setup-2(taken)→setup-3
	if !locs["d:setup-2"] || !locs["d:setup"] || !locs["d:setup-3"] {
		t.Errorf("expected d:setup-2, d:setup, d:setup-3, got %v", locs)
	}
}

// A symlink (even one pointing at a regular file) is skipped as irregular — it
// could pull content from outside the import root.
func TestPlanDirSkipsSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "d")
	mustWrite(t, filepath.Join(root, "real.md"), "real")
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	res := &planResult{skipped: []skipEntry{}, collisions: []collisionEntry{}}
	p := &planner{o: treeImportOpts{maxFileSize: 1 << 20}, res: res}
	node, err := p.planDir(root, "", "d", "d")
	if err != nil {
		t.Fatalf("planDir: %v", err)
	}
	if len(node.children) != 1 || node.children[0].name != "real.md" {
		t.Errorf("only the regular file should become a node, got %+v", node.children)
	}
	if !hasSkip(res.skipped, "link.md", "irregular") {
		t.Errorf("symlink should be skipped as irregular, got %+v", res.skipped)
	}
}

func hasSkip(skips []skipEntry, path, reason string) bool {
	for _, s := range skips {
		if s.Path == path && s.Reason == reason {
			return true
		}
	}
	return false
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustWriteBytes(t, path, []byte(content))
}

func mustWriteBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
