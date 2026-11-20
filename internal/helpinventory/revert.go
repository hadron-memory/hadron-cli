// Package helpinventory reads docs/help-inventory.md rows so a CI check can
// refuse a PR that erases evaluator work (cli#2648).
package helpinventory

import (
	"fmt"
	"sort"
	"strings"
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

// Parse reads the data rows of the inventory (a table row whose first cell is a
// backticked path). Header, separator and prose lines are ignored. A path that
// appears twice keeps its first row, as the generator never emits one.
func Parse(doc string) map[string]Row {
	rows := map[string]Row{}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimSpace(line), "|"), "|")
		if len(cells) < 10 {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		path := strings.Trim(cells[1], "`")
		if _, dup := rows[path]; dup {
			continue
		}
		rows[path] = Row{Path: path, Hash: cells[6], Evaluator: cells[7], Result: cells[8], Finding: cells[9]}
	}
	return rows
}

// Reverts lists the rows a change put back from an assessed state while the
// help text did not change. base is the inventory at the merge base, head the
// inventory under check. A row is a revert when its help-hash is the same in
// both, base had it assessed (result pass or fail), and head now reads unread or
// has lost its evaluator or finding. A changed hash is the one legitimate reset
// (the help really changed), and unread to pass stays allowed. Measuring against
// the merge base, not current main, is what keeps a PR that is merely behind
// main from failing for main's newer flips.
func Reverts(base, head map[string]Row) []string {
	var out []string
	for path, b := range base {
		h, ok := head[path]
		if !ok || b.Hash == "" || h.Hash != b.Hash {
			continue
		}
		if b.Result != "pass" && b.Result != "fail" {
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
