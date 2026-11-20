package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const hdr = "| path | aliases | generated | has-examples | child | help-hash | evaluator | result | finding |\n|---|---|---|---|---|---|---|---|---|\n"

func inv(result, eval, finding string) string {
	return hdr + "| `a` |  |  | y | #1 | h1 | " + eval + " | " + result + " | " + finding + " |\n"
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, doc, msg string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", msg)
}

// #2648 (d), pinned through the real git read: main flips a row to pass AFTER
// the branch point, so the PR (which never saw that flip) reads `unread` at its
// head. Measured against current main that looks like a revert (the mutant that
// reads the base ref instead of the merge base fails here); against the merge
// base, where the row was `unread` too, it is not.
func TestMergeBaseNotMainIsTheComparison2648(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, inv("unread", "", ""), "base: row unread")
	run(t, dir, "checkout", "-q", "-b", "pr")
	commit(t, dir, inv("unread", "", "")+"\n<!-- a PR-side edit -->\n", "pr: unrelated edit")
	run(t, dir, "checkout", "-q", "main")
	commit(t, dir, inv("pass", "Gil", "[x](u)"), "main: row flipped to pass")

	bad, mb, err := check(dir, "main", "pr")
	if err != nil || mb == "" {
		t.Fatalf("check: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("a PR merely behind main must not fail for main's newer flip: %v", bad)
	}
}

// The other side: the PR itself reverts a row that was assessed at the merge
// base, with the hash unchanged, and main meanwhile did something else.
func TestRevertSinceTheMergeBaseIsCaught2648(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, inv("pass", "Gil", "[x](u)"), "base: row passed")
	run(t, dir, "checkout", "-q", "-b", "pr")
	commit(t, dir, inv("unread", "", ""), "pr: bad main-merge resolution reverts it")
	run(t, dir, "checkout", "-q", "main")
	commit(t, dir, inv("pass", "Gil", "[x](u)")+"\n<!-- main edit -->\n", "main: unrelated edit")

	bad, _, err := check(dir, "main", "pr")
	if err != nil || len(bad) != 1 || !strings.Contains(bad[0], "`a`") {
		t.Fatalf("want the one reverted row: %v %v", bad, err)
	}
}

// An unreadable revision is an error, never a silent pass; only an inventory
// genuinely absent at the merge base passes.
func TestUnreadableRevisionIsAnError2648(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, inv("pass", "Gil", "[x](u)"), "base")
	if _, _, err := check(dir, "main", "no-such-ref"); err == nil {
		t.Fatal("a ref git cannot resolve must fail the check")
	}
	if _, err := readInventory(dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); err == nil || err == errNoFile {
		t.Fatalf("an unknown revision is an error, not an absent file: %v", err)
	}
	// Absent at the merge base: the check passes (the file is new in the PR).
	d2 := t.TempDir()
	run(t, d2, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(d2, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, d2, "add", "-A")
	run(t, d2, "commit", "-q", "-m", "no inventory")
	run(t, d2, "checkout", "-q", "-b", "pr")
	commit(t, d2, inv("pass", "Gil", "[x](u)"), "pr adds the inventory")
	if bad, _, err := check(d2, "main", "pr"); err != nil || len(bad) != 0 {
		t.Fatalf("a new inventory is not a revert: %v %v", bad, err)
	}
}
