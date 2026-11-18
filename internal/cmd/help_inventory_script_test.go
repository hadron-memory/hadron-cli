package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	return runInventoryScriptAs(t, dir, existing, "v9.9.9-1-gabc1234 (abc1234,")
}

// runInventoryScriptAs runs the generator against the fake binary reporting the
// given version prefix, so a test can build "the same tree at another commit".
func runInventoryScriptAs(t *testing.T, dir, existing, versionPrefix string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	bin := filepath.Join(dir, "hadron")
	if err := os.WriteFile(bin, []byte(strings.Replace(fakeHadron, "v9.9.9-1-gabc1234 (abc1234,", versionPrefix, 1)), 0o755); err != nil {
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
	out := inventoryWithoutHashes1126(t, runInventoryScript(t, t.TempDir(), ""))
	for _, unwanted := range []string{"Pinned commit:", "CLI version:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the generator must not print %q (cli#2310)\n%s", unwanted, out)
		}
	}
	for _, want := range []string{
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
	cells := inventoryCells1126(t, prior, "alpha one")
	old := "| " + strings.Join(cells, " | ") + " |"
	cells[1], cells[2], cells[3], cells[4] = "STALE", "STALE", "n", "#1"
	cells[6], cells[7], cells[8] = "Rae", "pass", "https://example.test/a"
	edited := strings.Replace(prior, old, "| "+strings.Join(cells, " | ")+" |", 1)
	if err := os.WriteFile(existing, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out := inventoryWithoutHashes1126(t, runInventoryScript(t, t.TempDir(), existing))
	// Structure is regenerated; the three human columns survive by path.
	want := "| `alpha one` | uno, ein |  | y | #7001 | Rae | pass | https://example.test/a |"
	if !strings.Contains(out, want) {
		t.Errorf("human columns were not carried over\nwant %q\n%s", want, out)
	}
	if strings.Contains(out, "STALE") {
		t.Errorf("a hand-edited structural column must be rewritten by the generator")
	}
}

// #1126: stand-ins exercise generator failure/reset paths without a real CLI.
type inventoryFixture1126 struct{ bin, groups string }
type inventoryResult1126 struct {
	out, stderr string
	code        int
}

func inventoryStandIn1126(t *testing.T, body string) inventoryFixture1126 {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	f := inventoryFixture1126{filepath.Join(dir, "hadron"), filepath.Join(dir, "groups.txt")}
	if err := os.WriteFile(f.bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.groups, []byte("7001|alpha things|alpha\n7002|root and utility|(root) completion help\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func captureInventory1126(t *testing.T, cmd *exec.Cmd) inventoryResult1126 {
	t.Helper()
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return inventoryResult1126{out.String(), stderr.String(), code}
}

func runInventoryScriptErr(t *testing.T, f inventoryFixture1126, existing string) inventoryResult1126 {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "gen-help-inventory.sh"))
	cmd.Env = append(os.Environ(), "HADRON_BIN="+f.bin, "HELP_INVENTORY_GROUPS="+f.groups, "HELP_INVENTORY_EXISTING="+existing)
	return captureInventory1126(t, cmd)
}

func inventoryOK1126(t *testing.T, f inventoryFixture1126, existing string) string {
	t.Helper()
	r := runInventoryScriptErr(t, f, existing)
	if r.code != 0 {
		t.Fatalf("generator exit=%d stderr=%s stdout=%s", r.code, r.stderr, r.out)
	}
	return r.out
}

// Read both formats so the reset test can fail on the actual stale assessment
// on main, rather than stopping at the new column's absence first.
func inventoryCells1126(t *testing.T, doc, path string) []string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| `"+path+"` | ") {
			cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
			if len(cells) != 8 && len(cells) != 9 {
				t.Fatalf("unexpected row shape %q", line)
			}
			return cells
		}
	}
	t.Fatalf("no row for %q", path)
	return nil
}

func markInventory1126(t *testing.T, doc, path, evaluator, result, finding string) string {
	t.Helper()
	cells := inventoryCells1126(t, doc, path)
	old := "| " + strings.Join(cells, " | ") + " |"
	cells[len(cells)-3], cells[len(cells)-2], cells[len(cells)-1] = evaluator, result, finding
	return strings.Replace(doc, old, "| "+strings.Join(cells, " | ")+" |", 1)
}

func saveInventory1126(t *testing.T, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "inventory.md")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHelpInventoryResetsRowWhenHelpTextChanges(t *testing.T) {
	for _, result := range []string{"pass", "fail"} {
		t.Run(result, func(t *testing.T) {
			f := inventoryStandIn1126(t, fakeHadron)
			prior := inventoryOK1126(t, f, "")
			prior = markInventory1126(t, prior, "alpha one", "Rae", result, "https://example.test/old")
			prior = markInventory1126(t, prior, "alpha two", "Steady", "pass", "https://example.test/keep")
			changed := strings.Replace(fakeHadron, "printf 'One.", "printf 'One revised.", 1)
			if changed == fakeHadron {
				t.Fatal("help fixture edit did not apply")
			}
			next := inventoryOK1126(t, inventoryStandIn1126(t, changed), saveInventory1126(t, prior))
			row := inventoryCells1126(t, next, "alpha one")
			if row[len(row)-3] != "" || row[len(row)-2] != "unread" || row[len(row)-1] != "" {
				t.Fatalf("changed help retained its %s assessment: %q", result, row)
			}
			before := inventoryCells1126(t, prior, "alpha one")
			if len(row) != 9 || len(row[5]) != 12 || (len(before) == 9 && row[5] == before[5]) {
				t.Fatalf("hash did not change: before=%q after=%q", before, row)
			}
			stable := inventoryCells1126(t, next, "alpha two")
			if stable[len(stable)-3] != "Steady" || stable[len(stable)-2] != "pass" || stable[len(stable)-1] != "https://example.test/keep" {
				t.Fatalf("unchanged row lost assessment: %q", stable)
			}
		})
	}
}

func TestHelpInventoryFirstAdoptionSeedsHash(t *testing.T) {
	for _, legacy := range []string{
		"| `alpha one` |  |  | n | #7001 | Rae | pass | https://example.test/pass |\n| `alpha two` |  |  | n | #7001 | Bo | fail | https://example.test/fail |\n",
		"| `alpha one` |  |  | n | #7001 |  | Rae | pass | https://example.test/pass |\n| `alpha two` |  |  | n | #7001 |  | Bo | fail | https://example.test/fail |\n",
	} {
		f := inventoryStandIn1126(t, fakeHadron)
		first := inventoryOK1126(t, f, saveInventory1126(t, legacy))
		for _, tc := range []struct{ path, ev, res, finding string }{{"alpha one", "Rae", "pass", "https://example.test/pass"}, {"alpha two", "Bo", "fail", "https://example.test/fail"}} {
			row := inventoryCells1126(t, first, tc.path)
			if len(row) != 9 || row[5] == "" || row[6] != tc.ev || row[7] != tc.res || row[8] != tc.finding {
				t.Fatalf("first adoption did not seed/keep %q: %q", tc.path, row)
			}
		}
		if second := inventoryOK1126(t, f, saveInventory1126(t, first)); second != first {
			t.Fatal("second generation is not byte-identical")
		}
	}
}

func TestHelpInventoryHashMatchesCapturedHelp(t *testing.T) {
	f := inventoryStandIn1126(t, fakeHadron)
	got := inventoryCells1126(t, inventoryOK1126(t, f, ""), "alpha one")
	raw, err := exec.Command("bash", f.bin, "alpha", "one", "--help").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	// Bash command substitution captures help_of without trailing newlines.
	want := fmt.Sprintf("%x", sha256.Sum256(bytes.TrimRight(raw, "\n")))[:12]
	if len(got) != 9 || got[5] != want {
		t.Fatalf("help hash: row=%q want=%s", got, want)
	}
}

func failingHelp1126(t *testing.T, args string) inventoryFixture1126 {
	t.Helper()
	body := strings.Replace(fakeHadron, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\nif [ \"$*\" = \""+args+"\" ]; then echo 'synthetic partial help'; exit 7; fi\n", 1)
	return inventoryStandIn1126(t, body)
}

func TestHelpInventoryGeneratorHelpFailure(t *testing.T) {
	for _, args := range []string{"alpha two --help", "--help"} {
		t.Run(args, func(t *testing.T) {
			r := runInventoryScriptErr(t, failingHelp1126(t, args), "")
			if r.code != 1 || r.out != "" || !strings.Contains(r.stderr, args+"' failed") {
				t.Fatalf("help failure: exit=%d stdout=%q stderr=%q", r.code, r.out, r.stderr)
			}
		})
	}
}

func TestHelpInventoryHasNoVersionDependency(t *testing.T) {
	base := inventoryOK1126(t, inventoryStandIn1126(t, fakeHadron), "")
	for _, version := range []string{"if [ \"$1\" = \"--version\" ]; then echo 'hadron dev'; exit 0; fi", "if [ \"$1\" = \"--version\" ]; then echo 'version must not be read' >&2; exit 97; fi"} {
		marker := filepath.Join(t.TempDir(), "version-called")
		version = strings.Replace(version, "then ", "then printf called >> "+shellQuote1126(marker)+"; ", 1)
		start := strings.Index(fakeHadron, "if [ \"$1\" = \"--version\" ]")
		end := start + strings.Index(fakeHadron[start:], "\n")
		body := fakeHadron[:start] + version + fakeHadron[end:]
		if got := inventoryOK1126(t, inventoryStandIn1126(t, body), ""); got != base {
			t.Fatal("version behaviour changed the inventory (cli#2310)")
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("generator read --version: %v", err)
		}
	}
}

func shellQuote1126(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func TestHelpInventoryFailureLeavesCommittedFileUntouched(t *testing.T) {
	for _, args := range []string{"alpha two --help", "--help"} {
		t.Run(args, func(t *testing.T) {
			f := failingHelp1126(t, args)
			dir := t.TempDir()
			dest := filepath.Join(dir, "inventory with spaces.md")
			const original = "pre-existing assessment inventory\n"
			if err := os.WriteFile(dest, []byte(original), 0o640); err != nil {
				t.Fatal(err)
			}
			generator, err := filepath.Abs(filepath.Join("..", "..", "scripts", "gen-help-inventory.sh"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "write-if-produced.sh"), dest)
			command := "HADRON_BIN=" + shellQuote1126(f.bin) + " HELP_INVENTORY_GROUPS=" + shellQuote1126(f.groups) + " HELP_INVENTORY_EXISTING=" + shellQuote1126(dest) + " bash " + shellQuote1126(generator)
			cmd.Env = append(os.Environ(), "WRITE_IF_PRODUCED_CMD="+command)
			r := captureInventory1126(t, cmd)
			if r.code != 1 || !strings.Contains(r.stderr, "generator failed") {
				t.Fatalf("wrapper failure: %+v", r)
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != original {
				t.Fatalf("destination changed: %v %q", err, got)
			}
			st, err := os.Stat(dest)
			if err != nil || st.Mode().Perm() != 0o640 {
				t.Fatalf("destination mode changed: %v %v", st, err)
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, ".write-if-produced.*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("staging leftovers: %v %v", leftovers, err)
			}
		})
	}
}

// Existing structure assertions stay independent of help digests, while every
// generated row is still required to have a well-formed hash column.
func inventoryWithoutHashes1126(t *testing.T, doc string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
		if len(cells) != 9 {
			t.Fatalf("generated row must have nine columns: %q", line)
		}
		if _, err := hex.DecodeString(cells[5]); len(cells[5]) != 12 || err != nil {
			t.Fatalf("invalid help hash in %q", line)
		}
		legacy := append(append([]string{}, cells[:5]...), cells[6:]...)
		lines[i] = "| " + strings.Join(legacy, " | ") + " |"
	}
	return strings.Join(lines, "\n")
}

func TestHelpInventoryShasumFallback1126(t *testing.T) {
	f := inventoryStandIn1126(t, fakeHadron)
	tools := t.TempDir()
	for _, name := range []string{"bash", "awk", "sed", "tr", "cut", "sort", "mktemp", "rm", "grep", "cat", "shasum"} {
		actual, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s not available", name)
		}
		if err := os.Symlink(actual, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Only the fallback digest tool is on PATH. The fixture and generator still
	// use real shells/readers so this proves the portable branch works end to end.
	t.Setenv("PATH", tools)
	row := inventoryCells1126(t, inventoryOK1126(t, f, ""), "alpha one")
	raw, err := exec.Command("bash", f.bin, "alpha", "one", "--help").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(bytes.TrimRight(raw, "\n")))[:12]
	if len(row) != 9 || row[5] != want {
		t.Fatalf("shasum help hash: %q want=%s", row, want)
	}
}

func TestHelpInventoryHashCellPaddingDoesNotInvalidate1126(t *testing.T) {
	f := inventoryStandIn1126(t, fakeHadron)
	for _, empty := range []bool{false, true} {
		prior := markInventory1126(t, inventoryOK1126(t, f, ""), "alpha one", "Rae", "pass", "https://example.test/keep")
		cells := inventoryCells1126(t, prior, "alpha one")
		old := "| " + strings.Join(cells, " | ") + " |"
		if empty {
			cells[5] = "   "
		} else {
			cells[5] = "  " + cells[5] + "  "
		}
		prior = strings.Replace(prior, old, "| "+strings.Join(cells, " | ")+" |", 1)
		row := inventoryCells1126(t, inventoryOK1126(t, f, saveInventory1126(t, prior)), "alpha one")
		if row[6] != "Rae" || row[7] != "pass" || row[8] != "https://example.test/keep" {
			t.Fatalf("Markdown padding invalidated assessment (empty=%v): %q", empty, row)
		}
	}
}
