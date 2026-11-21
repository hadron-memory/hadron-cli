package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/helpinventory"
	"github.com/spf13/cobra"
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

	bad, mb, err := check(dir, "main", "pr", nil)
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

	bad, _, err := check(dir, "main", "pr", nil)
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
	if _, _, err := check(dir, "main", "no-such-ref", nil); err == nil {
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
	if bad, _, err := check(d2, "main", "pr", nil); err != nil || len(bad) != 0 {
		t.Fatalf("a new inventory is not a revert: %v %v", bad, err)
	}
}

// cli#2699 through the real git read: the PR deletes an assessed row. With the command
// still in the live tree the check fails and names the row and the command (red on main,
// where Reverts skipped any base row absent from head); with the command gone from the tree
// the deletion passes. A mutant that ignores the live set turns the first red; one that
// names every deleted row turns the second red.
func TestDeletedAssessedRowIsCaughtOnlyWhileItsCommandExists2699(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, inv("pass", "Gil", "[x](u)"), "base: row passed")
	run(t, dir, "checkout", "-q", "-b", "pr")
	commit(t, dir, hdr+"| `other` |  |  | y | #1 | h9 |  | unread |  |\n", "pr: a bad resolution drops the assessed row")

	bad, _, err := check(dir, "main", "pr", map[string]bool{"a": true, "other": true})
	if err != nil || len(bad) != 1 || !strings.Contains(bad[0], "`a`") || !strings.Contains(bad[0], "hadron a") || !strings.Contains(bad[0], "deleted") {
		t.Fatalf("a deleted assessed row for a surviving command must be named with its command: %v %v", bad, err)
	}
	bad, _, err = check(dir, "main", "pr", map[string]bool{"other": true})
	if err != nil || len(bad) != 0 {
		t.Fatalf("a row deleted together with its command must pass: %v %v", bad, err)
	}
}

// The live tree is the real one, and its path spelling is the inventory's (Juniper's P2-2):
// every path of the committed inventory is a live page, with none missing and none tolerated,
// because a path absent from the live tree makes the deletion rule inert for exactly that row
// (it would read as "its command was removed"). The other direction (a live page without a
// row) is not asserted: the inventory legitimately lags a PR that adds a command (cli#2310);
// the walk test below pins that direction against the generator's own enumeration. If this
// fails, run `make help-inventory`, or delete the row of a command that was removed.
func TestRealCommandTreeSpellingMatchesTheInventory2699(t *testing.T) {
	live := helpinventory.LivePaths(cmd.NewRootCmd(cmdutil.NewFactory()))
	doc, err := os.ReadFile(filepath.Join("..", "..", file))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := helpinventory.Parse(string(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"(root)", "help", "completion"} {
		if _, ok := rows[r]; !ok {
			t.Errorf("the inventory has no %q row", r)
		}
	}
	var missing []string
	for path := range rows {
		if !live[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Errorf("%d inventory path(s) are not pages of the live command tree (a spelling disagreement, or a removed command whose row is stale): %v", len(missing), missing)
	}
}

// helpWalk enumerates the pages exactly the way scripts/gen-help-inventory.sh does, but
// in-process: from the root, read the first "Available Commands:" block of each page's help,
// one name per line indented by two spaces, and recurse, except below help and completion. The
// generator reads `hadron <path> --help`; this reads the usage text cobra prints for the same
// command (the block lives in the usage template), after cobra has added its own help and
// completion commands, which it does when a command runs.
func helpWalk(root *cobra.Command) map[string]bool {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	pages := map[string]bool{"(root)": true}
	children := func(c *cobra.Command) []string {
		var names []string
		in, seen := false, false
		for _, line := range strings.Split(c.UsageString(), "\n") {
			switch {
			case !seen && strings.HasPrefix(line, "Available Commands:"):
				in, seen = true, true
			case in && line == "":
				in = false
			case in && strings.HasPrefix(line, "  ") && len(line) > 2 && line[2] != ' ':
				names = append(names, strings.Fields(line)[0])
			}
		}
		return names
	}
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		for _, name := range children(c) {
			child, _, err := c.Find([]string{name})
			if err != nil || child == c {
				continue
			}
			p := name
			if path != "" {
				p = path + " " + name
			}
			pages[p] = true
			if name == "help" || name == "completion" {
				continue
			}
			walk(child, p)
		}
	}
	walk(root, "")
	return pages
}

// Juniper's P2-1: LivePaths is a second walk of the tree, and nothing but this test says it
// agrees with the generator's. On the real root the set of pages LivePaths reports equals the
// set of pages the generator's enumeration finds (hidden, deprecated, non-runnable groups,
// the generated help and completion pages, aliases): a path one lists and the other omits is
// either a deletion the rule can never catch (fails open) or a legitimate regeneration it
// would reject (fails closed). Both directions, with the differences named.
func TestLivePathsEqualTheHelpWalk2699(t *testing.T) {
	live := helpinventory.LivePaths(cmd.NewRootCmd(cmdutil.NewFactory()))
	walked := helpWalk(cmd.NewRootCmd(cmdutil.NewFactory()))
	var onlyLive, onlyWalk []string
	for p := range live {
		if !walked[p] {
			onlyLive = append(onlyLive, p)
		}
	}
	for p := range walked {
		if !live[p] {
			onlyWalk = append(onlyWalk, p)
		}
	}
	sort.Strings(onlyLive)
	sort.Strings(onlyWalk)
	if len(onlyLive) != 0 || len(onlyWalk) != 0 {
		t.Fatalf("LivePaths and the generator's enumeration disagree: only in LivePaths %v; only in the help walk %v", onlyLive, onlyWalk)
	}
	if len(live) < 50 {
		t.Fatalf("the real command tree reports only %d pages: the walk is not reading it", len(live))
	}
}

// The walk helper reads a root cobra has not run: a hidden and a deprecated command, a
// non-runnable group with nothing listed and an alias are not pages, and help and completion
// are (so the equality test above would catch a LivePaths that forgot or invented them).
func TestHelpWalkOnASyntheticRoot2699(t *testing.T) {
	root := &cobra.Command{Use: "hadron"}
	grp := &cobra.Command{Use: "grp", Short: "g"}
	grp.AddCommand(&cobra.Command{Use: "sub", Aliases: []string{"s"}, Short: "s", Run: func(*cobra.Command, []string) {}})
	grp.AddCommand(&cobra.Command{Use: "gone", Short: "gone", Hidden: true, Run: func(*cobra.Command, []string) {}})
	root.AddCommand(grp, &cobra.Command{Use: "old", Short: "old", Deprecated: "use grp", Run: func(*cobra.Command, []string) {}})
	walked := helpWalk(root)
	live := helpinventory.LivePaths(root)
	for _, want := range []string{"(root)", "help", "completion", "grp", "grp sub"} {
		if !walked[want] || !live[want] {
			t.Errorf("page %q: walked=%v live=%v", want, walked[want], live[want])
		}
	}
	for _, not := range []string{"grp gone", "old", "grp s"} {
		if walked[not] || live[not] {
			t.Errorf("%q must not be a page: walked=%v live=%v", not, walked[not], live[not])
		}
	}
}

// An empty root is exactly the three pages cobra and the generator always have, which is what
// the tool's guard (minLivePages) refuses to judge against.
func TestAnEmptyRootHasOnlyTheFixedPages2699(t *testing.T) {
	if got := helpinventory.LivePaths(&cobra.Command{Use: "hadron"}); len(got) != minLivePages {
		t.Fatalf("an empty root reports %d pages, the guard's floor is %d: %v", len(got), minLivePages, got)
	}
}

// The -head note (Joran's P3): quiet when -head is the checked-out revision or the default,
// and naming both revisions when -head is another commit.
func TestHeadNote2699(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, inv("pass", "Gil", "[x](u)"), "one")
	run(t, dir, "checkout", "-q", "-b", "other")
	commit(t, dir, inv("pass", "Gil", "[x](u)")+"\n<!-- two -->\n", "two")
	run(t, dir, "checkout", "-q", "main")
	if n := headNote(dir, "HEAD"); n != "" {
		t.Errorf("the default HEAD needs no note: %q", n)
	}
	if n := headNote(dir, "main"); n != "" {
		t.Errorf("-head naming the checked-out commit needs no note: %q", n)
	}
	if n := headNote(dir, "other"); !strings.Contains(n, "checked-out") || !strings.Contains(n, "-head") {
		t.Errorf("a different -head must be noted with both revisions: %q", n)
	}
	if n := headNote(dir, "no-such-ref"); n != "" {
		t.Errorf("an unreadable -head is check's error to report, not a note: %q", n)
	}
}
