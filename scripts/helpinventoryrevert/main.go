// Command helpinventoryrevert fails when a change puts docs/help-inventory.md
// rows back from an assessed state with an unchanged help hash (cli#2648), or
// deletes an assessed row while its command still exists (cli#2699).
//
// It compares the inventory at HEAD with the inventory at the MERGE BASE of HEAD
// and the base ref (default origin/main), never with current main: a PR that is
// merely behind main would otherwise fail for main's newer flips. Flags:
//
//	-base <ref>   the branch to take the merge base with (default origin/main)
//	-head <ref>   the change under check (default HEAD)
//
// "The command still exists" means it is in the live cobra command tree: the tool
// builds the CLI's own root in-process and passes its pages to helpinventory.Reverts
// (no help text is parsed and no shell script is run). That tree is the one of the code
// this tool was BUILT FROM, that is the checked-out working tree, and -head is only
// where the inventory is read from: when -head names a revision other than the
// checked-out HEAD (CI checks out the PR merge commit and passes the PR head sha) the
// tool says so on stderr, because a command added or removed by the other side is judged
// by the checked-out tree, not by the ref.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/cmd"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/helpinventory"
)

const file = "docs/help-inventory.md"

// errNoFile means the inventory does not exist at that revision (as opposed to
// git failing to read it).
var errNoFile = errors.New("no inventory at that revision")

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return strings.TrimSpace(string(out)), err
}

// readInventory returns the inventory at rev, errNoFile only when git says the
// path is absent there, and any other git failure as itself.
func readInventory(dir, rev string) (string, error) {
	if _, err := git(dir, "cat-file", "-e", rev+":"+file); err != nil {
		if _, verr := git(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); verr != nil {
			return "", fmt.Errorf("revision %s is not readable: %w", rev, verr)
		}
		return "", errNoFile
	}
	return git(dir, "show", rev+":"+file)
}

// check compares the inventory at head with the one at the merge base of head
// and base, run in repo dir, against the live command pages. It returns the reverted
// and deleted rows and the merge base.
func check(dir, base, head string, live map[string]bool) (bad []string, mergeBase string, err error) {
	mergeBase, err = git(dir, "merge-base", base, head)
	if err != nil || mergeBase == "" {
		return nil, "", fmt.Errorf("no merge base of %s and %s (a shallow clone? fetch full history): %v", base, head, err)
	}
	baseDoc, err := readInventory(dir, mergeBase)
	if errors.Is(err, errNoFile) {
		return nil, mergeBase, nil
	}
	if err != nil {
		return nil, mergeBase, err
	}
	headDoc, err := readInventory(dir, head)
	if err != nil {
		return nil, mergeBase, fmt.Errorf("cannot read %s at %s: %w", file, head, err)
	}
	baseRows, err := helpinventory.Parse(baseDoc)
	if err != nil {
		return nil, mergeBase, fmt.Errorf("%s at merge base %.9s: %w", file, mergeBase, err)
	}
	headRows, err := helpinventory.Parse(headDoc)
	if err != nil {
		return nil, mergeBase, fmt.Errorf("%s at %s: %w", file, head, err)
	}
	return helpinventory.Reverts(baseRows, headRows, live), mergeBase, nil
}

func main() {
	baseRef := flag.String("base", "origin/main", "branch to take the merge base with")
	headRef := flag.String("head", "HEAD", "the change under check")
	flag.Parse()

	// The live command tree of this build (the checked-out tree); building the root reads no
	// configuration and makes no request.
	live := helpinventory.LivePaths(cmd.NewRootCmd(cmdutil.NewFactory()))
	noteIfHeadIsNotCheckedOut(*headRef)
	bad, mb, err := check("", *baseRef, *headRef, live)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ help-inventory revert check: %v\n", err)
		os.Exit(2)
	}
	if len(bad) == 0 {
		fmt.Printf("✓ help inventory: no assessed row reverted since the merge base %.9s\n", mb)
		return
	}
	fmt.Printf("✗ help inventory: %d row(s) went back from an assessed state while their help hash did not change, or were deleted while their command still exists:\n", len(bad))
	for _, b := range bad {
		fmt.Println("  - " + b)
	}
	fmt.Printf("\nThis is usually a bad merge resolution of %s. Take main's file and regenerate: `git checkout %s -- %s && make help-inventory`, then re-apply only your own rows.\n", file, *baseRef, file)
	os.Exit(1)
}

// noteIfHeadIsNotCheckedOut says on stderr when -head is a revision other than the
// checked-out HEAD, so a reader of the result knows which tree "the command still exists"
// was judged against. It never fails the check: the revision may be unreadable here
// (checkHead reports that itself) or simply the PR head of a merge checkout.
func noteIfHeadIsNotCheckedOut(head string) {
	if head == "HEAD" {
		return
	}
	want, err1 := git("", "rev-parse", "--verify", "--quiet", head+"^{commit}")
	have, err2 := git("", "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err1 != nil || err2 != nil || want == have {
		return
	}
	fmt.Fprintf(os.Stderr, "note: the live command tree is the checked-out one (%.9s); the inventory is read from -head %.9s\n", have, want)
}
