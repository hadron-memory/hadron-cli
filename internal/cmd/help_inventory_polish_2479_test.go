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
	padded := strings.Replace(prior, old, "| "+strings.Join(cells, " | ")+" |", 1)
	if padded == prior {
		t.Fatal("the padded row replacement did not apply: the fixture row text changed, so this test would pass without testing anything")
	}
	prior = padded
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
// the host; the selected one is an explicit test fixture. The list is what
// scripts/gen-help-inventory.sh and scripts/write-if-produced.sh run (bash itself;
// awk, sed, tr, cut and sort for the row parsing and hashing; mktemp, rm, mv,
// chmod and dirname for the staged write; grep and cat). A tool missing on the host
// skips with the tool and the reason named, and a symlink that cannot be made
// fails with the tool and the directory, so a change to either script that adds a
// dependency shows up as a named failure, not a bare "command not found".
func inventoryDigestPath(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"bash", "awk", "sed", "tr", "cut", "sort", "mktemp", "rm", "grep", "cat", "dirname", "chmod", "mv"} {
		actual, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not on this host's PATH, and the digest fixture PATH needs it for the inventory scripts", tool)
		}
		if err := os.Symlink(actual, filepath.Join(dir, tool)); err != nil {
			t.Fatalf("linking %s (%s) into the fixture PATH %s: %v", tool, actual, dir, err)
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
	tools := inventoryDigestPath(t, "shasum", "[ \"$*\" = '-a 256' ] || exit 29\nexec "+shellQuote1126(testBin)+" -test.run=^TestHelpInventoryDigestHelperProcess2479$\n")
	t.Setenv("PATH", tools)
	t.Setenv("HELP_INVENTORY_DIGEST_PROCESS", "1")
	t.Setenv(digestHelperSentinelKey2479, digestHelperSentinel2479)
	row := inventoryCells1126(t, inventoryOK1126(t, f, ""), "alpha one")
	// The reference capture is made the way the generator makes it (C locale, 80
	// columns), so a future environment-sensitive fixture text cannot make this
	// oracle diverge from the generator for a reason that is not the digest. The
	// oracle stays independent: it hashes with Go, not with the generator's code.
	reference := exec.Command("bash", f.bin, "alpha", "one", "--help")
	reference.Env = append(os.Environ(), "LC_ALL=C", "COLUMNS=80")
	raw, err := reference.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(string(raw), "\n"))))[:12]
	if row[5] != want {
		t.Fatalf("fallback digest=%q want=%q", row[5], want)
	}
}

// The digest helper is this test binary re-executed as a stand-in `shasum`
// (TestHelpInventoryPortableShasum2479). It does its work only when BOTH
// HELP_INVENTORY_DIGEST_PROCESS=1 and the sentinel are set, so an ambient
// HELP_INVENTORY_DIGEST_PROCESS=1 cannot make an ordinary `go test` run read stdin
// and exit the whole test binary. It writes only the digest line to stdout, and
// the package's TestMain must not print before it runs (it does not; a banner
// there would corrupt the digest the generator reads).
const (
	digestHelperSentinelKey2479 = "HELP_INVENTORY_DIGEST_SENTINEL"
	digestHelperSentinel2479    = "re-exec:TestHelpInventoryDigestHelperProcess2479"
)

func TestHelpInventoryDigestHelperProcess2479(t *testing.T) {
	if os.Getenv("HELP_INVENTORY_DIGEST_PROCESS") != "1" || os.Getenv(digestHelperSentinelKey2479) != digestHelperSentinel2479 {
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

// An ambient HELP_INVENTORY_DIGEST_PROCESS=1 alone must not run the helper.
func TestHelpInventoryDigestHelperNeedsItsSentinel2479(t *testing.T) {
	t.Setenv("HELP_INVENTORY_DIGEST_PROCESS", "1")
	t.Setenv(digestHelperSentinelKey2479, "")
	// If the helper ran it would read stdin, print a digest and os.Exit(0), ending this
	// whole test binary early; returning here is the pass.
	TestHelpInventoryDigestHelperProcess2479(t)
}

func TestHelpInventoryDigestFailurePreservesFile2479(t *testing.T) {
	for _, tool := range []string{"sha256sum", "shasum"} {
		t.Run(tool, func(t *testing.T) {
			// One log per run (direct generator, then the write-if-produced wrapper), named by
			// the run through DIGEST_MARKER, so each run is judged on its own evidence
			// instead of an exact two-call combined log.
			logs := t.TempDir()
			directLog, wrappedLog := filepath.Join(logs, "direct"), filepath.Join(logs, "wrapped")
			tools := inventoryDigestPath(t, tool, "printf '%s\\n' \"$*\" >> \"$DIGEST_MARKER\"\ncat >/dev/null\nprintf '0123456789abcdef  -\\n'\necho 'synthetic digest failure' >&2\nexit 19\n")
			f := inventoryStandIn1126(t, fakeHadron)
			t.Setenv("PATH", tools)
			t.Setenv("DIGEST_MARKER", directLog)
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
			t.Setenv("DIGEST_MARKER", wrappedLog)
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
			// Each run invoked the SELECTED digest at least once, with the right arguments
			// (none for sha256sum, -a 256 for shasum). How many times is not the contract.
			wantArgs := ""
			if tool == "shasum" {
				wantArgs = "-a 256"
			}
			for run, log := range map[string]string{"direct generator": directLog, "wrapper": wrappedLog} {
				calls, err := os.ReadFile(log)
				if err != nil {
					t.Fatalf("%s never invoked %s: %v", run, tool, err)
				}
				lines := strings.Split(strings.TrimSuffix(string(calls), "\n"), "\n")
				for _, line := range lines {
					if line != wantArgs {
						t.Fatalf("%s invoked %s with %q, want %q (log %q)", run, tool, line, wantArgs, calls)
					}
				}
			}
		})
	}
}
