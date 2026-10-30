package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exceed pipe capacity after the early heading so a reader that exits on its
// first match cannot silently turn producer SIGPIPE into a negative result.
const largeInventoryHelp = `#!/usr/bin/env bash
set -eu
pad() { printf '%262144s\n' '' | tr ' ' x; }
case "$*" in
 --version) echo 'hadron v1.0.0 (abc1234, fixture)' ;;
 --help) printf 'Root\n\nAvailable Commands:\n  alpha Group\n  beta Negative\n\nFlags:\n' ;;
 'alpha --help')
   if [ "${FIXTURE_ALIASES:-}" = y ]; then
     printf 'Aliases:\n  alpha, alphas\n\nAvailable Commands:\n  leaf Leaf\n\n'
   fi
   printf 'Examples:\n  hadron alpha\n\n'; pad ;;
 'alpha leaf --help') printf 'Leaf\n' ;;
 'beta --help')
   if [ "${FIXTURE_FAIL:-}" = y ]; then echo 'fixture help failure' >&2; exit 23; fi
   printf 'No examples heading here\n'; pad ;;
 *) echo "unexpected $*" >&2; exit 24 ;;
esac
`

func inventoryLargeFixture(t *testing.T, aliases, fail bool) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hadron")
	if err := os.WriteFile(bin, []byte(largeInventoryHelp), 0o755); err != nil {
		t.Fatal(err)
	}
	groups := filepath.Join(dir, "groups")
	if err := os.WriteFile(groups, []byte("7001|fixture|alpha beta (root)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "gen-help-inventory.sh"))
	cmd.Env = append(os.Environ(), "HADRON_BIN="+bin, "HELP_INVENTORY_GROUPS="+groups, "HELP_INVENTORY_EXISTING=", "FIXTURE_ALIASES=", "FIXTURE_FAIL=")
	if aliases {
		cmd.Env = append(cmd.Env, "FIXTURE_ALIASES=y")
	}
	if fail {
		cmd.Env = append(cmd.Env, "FIXTURE_FAIL=y")
	}
	return cmd.CombinedOutput()
}

func TestHelpInventoryLargePageExamplesStable1312(t *testing.T) {
	var prior string
	for i := 0; i < 3; i++ {
		out, err := inventoryLargeFixture(t, false, false)
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, out)
		}
		for _, want := range []string{
			"| `alpha` |  |  | y | #7001 |  | unread |  |",
			"| `beta` |  |  | n | #7001 |  | unread |  |",
		} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("run %d missing %q\n%s", i, want, out)
			}
		}
		if i > 0 && string(out) != prior {
			t.Fatal("fixed help pages generated different bytes")
		}
		prior = string(out)
	}
}

func TestHelpInventoryLargePageAliasesAndChildren1312(t *testing.T) {
	out, err := inventoryLargeFixture(t, true, false)
	if err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"| `alpha` | alphas |  | y | #7001 |  | unread |  |",
		"| `alpha leaf` |  |  | n | #7001 |  | unread |  |",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q\n%s", want, out)
		}
	}
}

func TestHelpInventoryLargePageRealFailure1312(t *testing.T) {
	out, err := inventoryLargeFixture(t, false, true)
	if err == nil {
		t.Fatalf("real help failure succeeded\n%s", out)
	}
	if !strings.Contains(string(out), "--help' failed") {
		t.Fatalf("missing help-call failure diagnostic: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "# Help inventory") {
		t.Fatalf("failed walk emitted a successful inventory\n%s", out)
	}
}
