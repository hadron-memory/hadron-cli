package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpInventoryPathPaddingPreservesHumanText2479(t *testing.T) {
	f := inventoryStandIn1126(t, fakeHadron)
	prior := inventoryOK1126(t, f, "")
	cells := inventoryCells1126(t, prior, "alpha one")
	old := "| " + strings.Join(cells, " | ") + " |"
	// Table padding around the backticked key is structural; human text is not.
	cells[0] = " \t`alpha one`\t "
	cells[6], cells[7], cells[8] = "  Rae  ", "pass", "  [finding text](https://example.test/keep)  "
	prior = strings.Replace(prior, old, "| "+strings.Join(cells, " | ")+" |", 1)
	got := inventoryCells1126(t, inventoryOK1126(t, f, saveInventory1126(t, prior)), "alpha one")
	if got[6] != cells[6] || got[7] != cells[7] || got[8] != cells[8] {
		t.Fatalf("path padding lost or changed human text: got=%q want=%q", got[6:], cells[6:])
	}
}

func TestHelpInventoryCaptureEnvironment2479(t *testing.T) {
	// The help process reports its environment through a real generator walk.
	body := strings.Replace(fakeHadron, "args=\"$*\"", "[ \"$LC_ALL\" = C ] && [ \"$COLUMNS\" = 80 ] || { echo 'unexpected capture environment' >&2; exit 23; }\nargs=\"$*\"", 1)
	if body == fakeHadron {
		t.Fatal("fixture marker missing")
	}
	var first string
	for _, width := range []string{"137", "999"} {
		t.Run(width, func(t *testing.T) {
			t.Setenv("LC_ALL", "POSIX")
			t.Setenv("COLUMNS", width)
			got := inventoryOK1126(t, inventoryStandIn1126(t, body), "")
			if first != "" && got != first {
				t.Fatal("caller environment changed output")
			}
			first = got
		})
	}
}

func TestInventoryVersionBranchRejectsFixtureDrift2479(t *testing.T) {
	for _, body := range []string{"no version branch\n", "if [ \"$1\" = \"--version\" ]"} {
		t.Run(body, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("fixture drift panicked instead of returning an error: %v", p)
				}
			}()
			if _, err := replaceInventoryVersionBranch(body, "replacement"); err == nil {
				t.Fatal("fixture drift was not rejected")
			}
		})
	}
}

// Only these tools exist in the fixture PATH. No digest executable leaks from
// the host; the selected one is an explicit test fixture.
func inventoryDigestPath(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"bash", "awk", "sed", "tr", "cut", "sort", "mktemp", "rm", "grep", "cat", "dirname", "chmod", "mv"} {
		actual, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not available", tool)
		}
		if err := os.Symlink(actual, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A stand-in shasum uses Go's independent SHA-256 implementation, so the
// fallback branch runs even on hosts without Perl/shasum. Args are checked by
// the executable fixture, not ignored.
func TestHelpInventoryPortableShasum2479(t *testing.T) {
	testBin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := inventoryStandIn1126(t, fakeHadron)
	tools := inventoryDigestPath(t, "shasum", "[ \"$*\" = '-a 256' ] || exit 29\nexec "+shellQuote1126(testBin)+" -test.run=^TestHelpInventoryDigestProcess2479$\n")
	t.Setenv("PATH", tools)
	t.Setenv("HELP_INVENTORY_DIGEST_PROCESS", "1")
	row := inventoryCells1126(t, inventoryOK1126(t, f, ""), "alpha one")
	raw, err := exec.Command("bash", f.bin, "alpha", "one", "--help").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(string(raw), "\n"))))[:12]
	if row[5] != want {
		t.Fatalf("fallback digest=%q want=%q", row[5], want)
	}
}

func TestHelpInventoryDigestProcess2479(t *testing.T) {
	if os.Getenv("HELP_INVENTORY_DIGEST_PROCESS") != "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%x  -\n", sha256.Sum256(data))
	os.Exit(0)
}

func TestHelpInventoryDigestFailurePreservesFile2479(t *testing.T) {
	for _, tool := range []string{"sha256sum", "shasum"} {
		t.Run(tool, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "digest-called")
			tools := inventoryDigestPath(t, tool, "printf '%s\\n' \"$*\" >> "+shellQuote1126(marker)+"\ncat >/dev/null\nprintf '0123456789abcdef  -\\n'\necho 'synthetic digest failure' >&2\nexit 19\n")
			f := inventoryStandIn1126(t, fakeHadron)
			t.Setenv("PATH", tools)
			direct := runInventoryScriptErr(t, f, "")
			if direct.code != 1 || direct.out != "" || !strings.Contains(direct.stderr, "synthetic digest failure") {
				t.Fatalf("digest failure masked: %+v", direct)
			}
			dir := t.TempDir()
			dest := filepath.Join(dir, "inventory with spaces.md")
			const original = "existing assessment bytes\n"
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
			wrapped := captureInventory1126(t, cmd)
			if wrapped.code != 1 || wrapped.out != "" || !strings.Contains(wrapped.stderr, "generator failed") || !strings.Contains(wrapped.stderr, "synthetic digest failure") {
				t.Fatalf("wrapper masked digest failure: %+v", wrapped)
			}
			data, err := os.ReadFile(dest)
			if err != nil || string(data) != original {
				t.Fatalf("destination changed: %q %v", data, err)
			}
			st, err := os.Stat(dest)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0o640 {
				t.Fatalf("mode changed: %v", st.Mode())
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, ".write-if-produced.*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("staging leftovers: %v %v", leftovers, err)
			}
			calls, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			want := "\n\n"
			if tool == "shasum" {
				want = "-a 256\n-a 256\n"
			}
			if string(calls) != want {
				t.Fatalf("wrong digest calls: %q want=%q", calls, want)
			}
		})
	}
}
