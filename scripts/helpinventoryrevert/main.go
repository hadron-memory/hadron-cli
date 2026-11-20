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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/helpinventory"
)

const file = "docs/help-inventory.md"

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func main() {
	baseRef := flag.String("base", "origin/main", "branch to take the merge base with")
	headRef := flag.String("head", "HEAD", "the change under check")
	flag.Parse()

	mb, err := git("merge-base", *baseRef, *headRef)
	if err != nil || mb == "" {
		fmt.Fprintf(os.Stderr, "✗ help-inventory revert check: no merge base of %s and %s (a shallow clone? fetch full history): %v\n", *baseRef, *headRef, err)
		os.Exit(2)
	}
	baseDoc, err := git("show", mb+":"+file)
	if err != nil {
		fmt.Printf("✓ help inventory revert check: %s does not exist at the merge base %s\n", file, mb[:9])
		return
	}
	headDoc, err := git("show", *headRef+":"+file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ help-inventory revert check: cannot read %s at %s: %v\n", file, *headRef, err)
		os.Exit(2)
	}
	bad := helpinventory.Reverts(helpinventory.Parse(baseDoc), helpinventory.Parse(headDoc))
	if len(bad) == 0 {
		fmt.Printf("✓ help inventory: no assessed row reverted since the merge base %s\n", mb[:9])
		return
	}
	fmt.Printf("✗ help inventory: %d row(s) went back from an assessed state while their help hash did not change:\n", len(bad))
	for _, b := range bad {
		fmt.Println("  - " + b)
	}
	fmt.Printf("\nThis is usually a bad merge resolution of %s. Take main's file and regenerate: `git checkout %s -- %s && make help-inventory`, then re-apply only your own rows.\n", file, *baseRef, file)
	os.Exit(1)
}
