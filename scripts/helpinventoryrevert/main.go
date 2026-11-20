// Command helpinventoryrevert fails when a change puts docs/help-inventory.md
// rows back from an assessed state with an unchanged help hash (cli#2648).
//
// It compares the inventory at HEAD with the inventory at the MERGE BASE of HEAD
// and the base ref (default origin/main), never with current main: a PR that is
// merely behind main would otherwise fail for main's newer flips. Flags:
//
//	-base <ref>   the branch to take the merge base with (default origin/main)
//	-head <ref>   the change under check (default HEAD)
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
// and base, run in repo dir. It returns the reverted rows and the merge base.
func check(dir, base, head string) (bad []string, mergeBase string, err error) {
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
	return helpinventory.Reverts(helpinventory.Parse(baseDoc), helpinventory.Parse(headDoc)), mergeBase, nil
}

func main() {
	baseRef := flag.String("base", "origin/main", "branch to take the merge base with")
	headRef := flag.String("head", "HEAD", "the change under check")
	flag.Parse()

	bad, mb, err := check("", *baseRef, *headRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ help-inventory revert check: %v\n", err)
		os.Exit(2)
	}
	if len(bad) == 0 {
		fmt.Printf("✓ help inventory: no assessed row reverted since the merge base %.9s\n", mb)
		return
	}
	fmt.Printf("✗ help inventory: %d row(s) went back from an assessed state while their help hash did not change:\n", len(bad))
	for _, b := range bad {
		fmt.Println("  - " + b)
	}
	fmt.Printf("\nThis is usually a bad merge resolution of %s. Take main's file and regenerate: `git checkout %s -- %s && make help-inventory`, then re-apply only your own rows.\n", file, *baseRef, file)
	os.Exit(1)
}
