package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hadron-cli#2476 (deferred F1 of #2473): gen-help-inventory.sh has two failure
// stops besides the help-walk one that help_inventory_script_test.go pins:
// neither digest tool on PATH, and a committed inventory whose row has an
// unsupported column count. Each must exit 1 with its own diagnostic, publish
// nothing on stdout, and, through write-if-produced.sh, leave the destination
// byte-identical.

// inventoryNoDigestPath2476 is a PATH holding every tool the generator needs
// before and after the digest check, and neither sha256sum nor shasum.
func inventoryNoDigestPath2476(t *testing.T) string {
	t.Helper()
	tools := t.TempDir()
	for _, name := range []string{"bash", "env", "awk", "sed", "tr", "cut", "sort", "mktemp", "rm", "grep", "cat", "dirname", "chmod", "mv"} {
		actual, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s not available", name)
		}
		if err := os.Symlink(actual, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	return tools
}

// runInventoryStop2476 runs the generator directly and again through
// write-if-produced.sh over a destination holding original, and returns both.
func runInventoryStop2476(t *testing.T, f inventoryFixture1126, existingBody, path string) (direct, wrapped inventoryResult1126) {
	t.Helper()
	dir := t.TempDir()
	dest := filepath.Join(dir, "inventory.md")
	const original = "pre-existing assessment inventory\n"
	if err := os.WriteFile(dest, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	existing := ""
	if existingBody != "" {
		existing = filepath.Join(dir, "existing.md")
		if err := os.WriteFile(existing, []byte(existingBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "HADRON_BIN="+f.bin, "HELP_INVENTORY_GROUPS="+f.groups, "HELP_INVENTORY_EXISTING="+existing)
	if path != "" {
		env = append(env, "PATH="+path)
	}
	generator, err := filepath.Abs(filepath.Join("..", "..", "scripts", "gen-help-inventory.sh"))
	if err != nil {
		t.Fatal(err)
	}
	d := exec.Command("bash", generator)
	d.Env = env
	direct = captureInventory1126(t, d)

	w := exec.Command("bash", filepath.Join("..", "..", "scripts", "write-if-produced.sh"), dest)
	command := "HADRON_BIN=" + shellQuote1126(f.bin) + " HELP_INVENTORY_GROUPS=" + shellQuote1126(f.groups) +
		" HELP_INVENTORY_EXISTING=" + shellQuote1126(existing) + " bash " + shellQuote1126(generator)
	w.Env = append(env, "WRITE_IF_PRODUCED_CMD="+command)
	wrapped = captureInventory1126(t, w)

	got, err := os.ReadFile(dest)
	if err != nil || string(got) != original {
		t.Fatalf("destination changed: %v %q", err, got)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("destination mode changed: %v %v", st, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".write-if-produced.*")); len(left) != 0 {
		t.Fatalf("staging leftovers: %v", left)
	}
	return direct, wrapped
}

func assertInventoryStop2476(t *testing.T, direct, wrapped inventoryResult1126, diagnostic string) {
	t.Helper()
	if direct.code != 1 || direct.out != "" || !strings.Contains(direct.stderr, diagnostic) {
		t.Fatalf("generator: exit=%d stdout=%q stderr=%q, want exit 1, no stdout, %q", direct.code, direct.out, direct.stderr, diagnostic)
	}
	if wrapped.code != 1 || !strings.Contains(wrapped.stderr, diagnostic) || !strings.Contains(wrapped.stderr, "generator failed") {
		t.Fatalf("through write-if-produced: %+v, want exit 1 with %q and 'generator failed'", wrapped, diagnostic)
	}
}

func TestHelpInventoryMissingDigestTool2476(t *testing.T) {
	f := inventoryStandIn1126(t, fakeHadron)
	direct, wrapped := runInventoryStop2476(t, f, "", inventoryNoDigestPath2476(t))
	assertInventoryStop2476(t, direct, wrapped, "gen-help-inventory: sha256sum or shasum is required")
}

func TestHelpInventoryMalformedExistingRow2476(t *testing.T) {
	f := inventoryStandIn1126(t, fakeHadron)
	// Three cells: neither the 8-column (pre-hash) nor the 9-column layout.
	malformed := "| `alpha one` | uno | y |\n"
	direct, wrapped := runInventoryStop2476(t, f, malformed, "")
	assertInventoryStop2476(t, direct, wrapped, "gen-help-inventory: unsupported inventory row shape: | `alpha one` | uno | y |")
}
