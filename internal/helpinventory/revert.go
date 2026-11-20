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
