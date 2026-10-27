package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A stand-in binary that prints canned cobra-shaped help, so the generator's
// parsing, grouping and carry-over run without the real tree.
const fakeHadron = `#!/usr/bin/env bash
if [ "$1" = "--version" ]; then echo "hadron v9.9.9-1-gabc1234 (abc1234, 2026-01-01T00:00:00Z)"; exit 0; fi
args="$*"
case "$args" in
  "--help") printf 'Root.\n\nAvailable Commands:\n  alpha   Alpha group\n  beta    Beta group\n  completion  Gen\n  help    Help\n  ghost   No child owns this\n\nFlags:\n  -h, --help\n' ;;
  "alpha --help") printf 'Alpha.\n\nAliases:\n  alpha, alphas\n\nAvailable Commands:\n  one   First\n  two   Second\n\nFlags:\n  -h, --help\n' ;;
  "alpha one --help") printf 'One.\n\nAliases:\n  one, uno, ein\n\nExamples:\n  hadron alpha one\n\nFlags:\n  -h, --help\n' ;;
  "alpha two --help") printf 'Two.\n\nUsage:\n  hadron alpha two\n' ;;
  "beta --help") printf 'Beta.\n\nUsage:\n  hadron beta\n' ;;
  "ghost --help") printf 'Ghost.\n' ;;
  "completion --help") printf 'Completion.\n\nAvailable Commands:\n  bash   Bash\n' ;;
  "help --help") printf 'Help.\n' ;;
  *) echo "unexpected: $args" >&2; exit 1 ;;
esac
`

func runInventoryScript(t *testing.T, dir, existing string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	bin := filepath.Join(dir, "hadron")
	if err := os.WriteFile(bin, []byte(fakeHadron), 0o755); err != nil {
		t.Fatal(err)
	}
	groups := filepath.Join(dir, "groups.txt")
	if err := os.WriteFile(groups, []byte("# comment\n7001|alpha things|alpha\n7002|root and utility|(root) completion help\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, filepath.Join("..", "..", "scripts", "gen-help-inventory.sh"))
	cmd.Env = append(os.Environ(), "HADRON_BIN="+bin, "HELP_INVENTORY_GROUPS="+groups, "HELP_INVENTORY_EXISTING="+existing)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestHelpInventoryGeneratorStructure(t *testing.T) {
	out := runInventoryScript(t, t.TempDir(), "")
	for _, want := range []string{
		"Pinned commit: abc1234\n",
		"CLI version: v9.9.9-1-gabc1234\n", // no build date: two builds of one commit generate the same bytes
		"| `alpha` | alphas |  | n | #7001 |  | unread |  |",
		"| `alpha one` | uno, ein |  | y | #7001 |  | unread |  |", // own name dropped from aliases, examples seen
		"| `alpha two` |  |  | n | #7001 |  | unread |  |",
		"| `completion` |  | generated | n | #7002 |  | unread |  |",
		"| `help` |  | generated | n | #7002 |  | unread |  |",
		"| `(root)` |  |  | n | #7002 |  | unread |  |",
		"<!-- group:unassigned start -->",
		"| `beta` |  |  | n | - |  | unread |  |",
		"| `ghost` |  |  | n | - |  | unread |  |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated inventory lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "completion bash") {
		t.Errorf("generated pages are not walked below the top level")
	}
	if strings.Count(out, "| `(root)`") != 1 {
		t.Errorf("the root page is one row, its children belong to their own groups")
	}
}

func TestHelpInventoryGeneratorKeepsHumanColumns(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.md")
	prior := runInventoryScript(t, t.TempDir(), "")
	edited := strings.Replace(prior,
		"| `alpha one` | uno, ein |  | y | #7001 |  | unread |  |",
		"| `alpha one` | STALE | STALE | n | #1 | Rae | pass | https://example.test/a |", 1)
	if edited == prior {
		t.Fatal("fixture row not found")
	}
	if err := os.WriteFile(existing, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runInventoryScript(t, t.TempDir(), existing)
	// Structure is regenerated; the three human columns survive by path.
	want := "| `alpha one` | uno, ein |  | y | #7001 | Rae | pass | https://example.test/a |"
	if !strings.Contains(out, want) {
		t.Errorf("human columns were not carried over\nwant %q\n%s", want, out)
	}
	if strings.Contains(out, "STALE") {
		t.Errorf("a hand-edited structural column must be rewritten by the generator")
	}
}
