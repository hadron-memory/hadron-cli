package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2310: the inventory no longer carries a per-regeneration commit/version
// pin, so two PRs that regenerate it stop conflicting on those lines, and the
// drift check compares the WHOLE file.

// Two builds of different commits that expose the same command rows generate
// byte-identical output.
func TestHelpInventoryGeneratorIsCommitIndependent2310(t *testing.T) {
	a := runInventoryScriptAs(t, t.TempDir(), "", "v1.0.0-1-g1111111-dirty (1111111,")
	b := runInventoryScriptAs(t, t.TempDir(), "", "v2.5.0-9-g2222222 (2222222,")
	if a != b {
		t.Fatalf("the generator's output depends on the binary's version:\n--- first\n%s\n--- second\n%s", a, b)
	}
}

// scratchTree2310 is a scratch tree holding the real Makefile and generator, the
// real group map and a stand-in binary: what the check recipe needs, nothing of
// the real command tree.
func scratchTree2310(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not available")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	for _, f := range []string{"Makefile", "scripts/gen-help-inventory.sh", "scripts/help-inventory-groups.txt"} {
		data, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "hadron"), []byte(fakeHadron), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// generate2310 runs the generator in the scratch tree as `make help-inventory`
// does (the committed doc, when there is one, supplies the human columns).
func generate2310(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("bash", "scripts/gen-help-inventory.sh")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HADRON_BIN=bin/hadron", "HELP_INVENTORY_EXISTING=docs/help-inventory.md")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("generator failed: %v", err)
	}
	return string(out)
}

// check2310 runs the real `make help-inventory-check` recipe against doc.
// `-o build` marks the build step up to date, so the stand-in stays the binary.
func check2310(t *testing.T, dir, doc string) (string, error) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "help-inventory.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("make", "-o", "build", "help-inventory-check")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHelpInventoryCheckComparesTheWholeFile2310(t *testing.T) {
	dir := scratchTree2310(t)
	fresh := generate2310(t, dir)
	if out, err := check2310(t, dir, fresh); err != nil || !strings.Contains(out, "help inventory matches the command tree") {
		t.Fatalf("a freshly generated inventory must pass the check: %v\n%s", err, out)
	}
	// A real change to a command row is drift.
	rowChanged := strings.Replace(fresh, "| `alpha two` |", "| `alpha twoo` |", 1)
	if rowChanged == fresh {
		t.Fatal("fixture row not found")
	}
	if out, err := check2310(t, dir, rowChanged); err == nil || !strings.Contains(out, "help-inventory drift") {
		t.Fatalf("a changed row must fail the check: %v\n%s", err, out)
	}
	// An old-format file that still carries the pin lines is drift until it is
	// regenerated: the check no longer excuses them.
	oldFormat := strings.Replace(fresh, "\nRules for what is a row:", "\nPinned commit: abc1234\nCLI version: v9.9.9-1-gabc1234\n\nRules for what is a row:", 1)
	if oldFormat == fresh {
		t.Fatal("fixture anchor not found")
	}
	if out, err := check2310(t, dir, oldFormat); err == nil || !strings.Contains(out, "help-inventory drift") {
		t.Fatalf("an old-format file with the pin lines must fail the check: %v\n%s", err, out)
	}
	// One regeneration over the old-format file drops the lines, and the check
	// passes again.
	if err := os.WriteFile(filepath.Join(dir, "docs", "help-inventory.md"), []byte(oldFormat), 0o644); err != nil {
		t.Fatal(err)
	}
	regenerated := generate2310(t, dir)
	if strings.Contains(regenerated, "Pinned commit:") || strings.Contains(regenerated, "CLI version:") {
		t.Fatalf("regeneration kept the pin lines:\n%s", regenerated)
	}
	if out, err := check2310(t, dir, regenerated); err != nil {
		t.Fatalf("regeneration must clear the old-format drift: %v\n%s", err, out)
	}
}
