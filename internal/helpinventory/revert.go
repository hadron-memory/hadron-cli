// Package helpinventory reads docs/help-inventory.md rows so a CI check can
// refuse a PR that erases evaluator work (cli#2648).
package helpinventory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// Row is one inventory row: the command path, its help hash and the three
// human columns.
type Row struct {
	Path      string
	Hash      string
	Evaluator string
	Result    string
	Finding   string
}

// headerLine is the column header the generator writes
// (scripts/gen-help-inventory.sh); Parse refuses a data row under any other, so a
// reordered, renamed or added column cannot be read at the old indices.
const headerLine = "| path | aliases | generated | has-examples | child | help-hash | evaluator | result | finding |"

// Parse reads the data rows of the inventory (a table row whose first cell is a
// backticked path). Header, separator and prose lines are ignored. It fails
// closed: a data row without the nine columns, a path that appears twice (the
// generator never emits one) or a document with no data row at all is an error,
// because a parser that quietly reads nothing would let every revert pass.
func Parse(doc string) (map[string]Row, error) {
	rows := map[string]Row{}
	header := ""
	for n, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| path ") {
			header = strings.TrimSpace(line)
			continue
		}
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		if header != headerLine {
			return nil, fmt.Errorf("line %d: row under an unexpected table header %q (want %q)", n+1, header, headerLine)
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 10 {
			return nil, fmt.Errorf("line %d: an inventory row needs 9 columns, found %d (did the table format change?)", n+1, len(cells)-1)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		path := strings.Trim(cells[1], "`")
		if _, dup := rows[path]; dup {
			return nil, fmt.Errorf("line %d: path `%s` appears twice", n+1, path)
		}
		switch cells[8] {
		case "pass", "fail", "unread":
		default:
			return nil, fmt.Errorf("line %d: result %q for `%s` is not pass, fail or unread", n+1, cells[8], path)
		}
		rows[path] = Row{Path: path, Hash: cells[6], Evaluator: cells[7], Result: cells[8], Finding: cells[9]}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no inventory rows found (did the table format change?)")
	}
	return rows, nil
}

// rootPath is the inventory path of the root command page.
const rootPath = "(root)"

// LivePaths returns the inventory paths of the command pages that exist in the live
// cobra command tree built from the CLI's own root (cli#2699): the same set of pages
// the generator walks with `hadron <path> --help`, spelled the way an inventory row
// spells them. A page is a command cobra lists (cobra's own IsAvailableCommand: not
// hidden, not deprecated, runnable or with a listed child), found by its canonical
// name (an alias is a column of its target's row, never a path); the root is "(root)".
// cobra adds the help and completion commands when a command runs, not when the root
// is built, so the two `generated` pages the inventory lists are added by name; that
// hard-coding is tied to the generator (which lists help and completion from the root's
// own "Available Commands:" and does not walk below them) and pinned against a walk of the
// real root's help text in scripts/helpinventoryrevert (TestLivePathsEqualTheHelpWalk2699).
func LivePaths(root *cobra.Command) map[string]bool {
	live := map[string]bool{rootPath: true, "help": true, "completion": true}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, ch := range c.Commands() {
			if !ch.IsAvailableCommand() {
				continue
			}
			path := ch.Name()
			if prefix != "" {
				path = prefix + " " + ch.Name()
			}
			live[path] = true
			if prefix == "" && (ch.Name() == "help" || ch.Name() == "completion") {
				// The generator lists these two pages and does not walk below them: the
				// completion command's per-shell children are not inventory rows.
				continue
			}
			walk(ch, path)
		}
	}
	walk(root, "")
	return live
}

// Reverts lists the rows a change put back from an assessed state while the
// help text did not change, and the assessed rows it deleted while their command
// still exists. base is the inventory at the merge base, head the inventory under
// check, live the command pages of the live command tree (LivePaths).
//
// A row is a revert when its help-hash is the same in both, base had it assessed
// (result pass or fail), and head now reads unread or has lost its evaluator or
// finding. A changed hash is the one legitimate reset (the help really changed),
// and unread to pass stays allowed. Measuring against the merge base, not current
// main, is what keeps a PR that is merely behind main from failing for main's
// newer flips.
//
// A base row that is assessed and ABSENT from head is a deletion. When its command
// path is still live it is a bad merge resolution (a regeneration would have kept the
// row, as unread, so only a hand edit or a bad resolution can drop it): the check names
// the row and the command. When the command is no longer live (a command may be
// removed) the deletion is allowed. A nil live set DISABLES the deletion rule (no tree was
// given, so no command is known to exist): it is not a way to say "every command was
// removed", and a caller that wants the rule must pass the tree (the tool refuses to run
// with an empty one).
func Reverts(base, head map[string]Row, live map[string]bool) []string {
	var out []string
	for path, b := range base {
		if b.Result != "pass" && b.Result != "fail" {
			continue
		}
		h, ok := head[path]
		if !ok {
			if live[path] {
				out = append(out, fmt.Sprintf("`%s`: assessed row (%s by %s) deleted while the command `%s` still exists in the command tree, help hash %s at the merge base", path, b.Result, evaluatorOrDash(b.Evaluator), commandOf(path), evaluatorOrDash(b.Hash)))
			}
			continue
		}
		if b.Hash == "" || h.Hash != b.Hash {
			continue
		}
		switch {
		case h.Result != "pass" && h.Result != "fail":
			out = append(out, fmt.Sprintf("`%s`: %s (%s) -> %s, help hash %s unchanged", path, b.Result, b.Evaluator, h.Result, b.Hash))
		case b.Evaluator != "" && h.Evaluator == "":
			out = append(out, fmt.Sprintf("`%s`: evaluator %s dropped, help hash %s unchanged", path, b.Evaluator, b.Hash))
		case b.Finding != "" && h.Finding == "":
			out = append(out, fmt.Sprintf("`%s`: finding link dropped, help hash %s unchanged", path, b.Hash))
		}
	}
	sort.Strings(out)
	return out
}

// evaluatorOrDash renders an empty cell as a dash.
func evaluatorOrDash(e string) string {
	if e == "" {
		return "-"
	}
	return e
}

// commandOf names the command a row path stands for, as a user types it.
func commandOf(path string) string {
	if path == rootPath {
		return "hadron"
	}
	return "hadron " + path
}
