package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// cellInRow reports whether the attacked render carries cellWant AS the cell
// (#1119), judged at every place the clean render carried cleanCell. Each
// place is identified by its line and its row prefix (the row's key and every
// column before the cell), compared with whitespace collapsed because a
// widened column elsewhere repads the whole table. Where the attacked line
// still has that prefix, what follows must be one of:
//
//   - cleanCell: untouched (the marker also occurs as unrelated text);
//   - cellWant, ending at column padding, the end of the line, or the
//     separator that followed the clean cell (a prose line's "family X: 1").
//
// Anything else there — a raw value, a split line — fails. A line whose
// prefix no longer matches was rewritten (a hint reworded for an unsafe
// value) and cannot place the cell, so it is not judged. At least one place
// must carry the cell. A sanitized echo elsewhere (a headline, an adjacent
// line) therefore cannot stand in for a raw cell: the raw cell's own place
// fails. Nothing right of the cell is compared, because the bound can eat a
// composed cell's closing text ("PENDING (…)") and some fields feed a later
// column too (scope → TARGET). Once a line has carried the cell, later places
// on it sit behind the widened cell and are not judged.
func cellInRow(clean, out, cleanCell, cellWant string) bool {
	if cleanCell == "" || cellWant == "" {
		return false
	}
	cl, al := strings.Split(clean, "\n"), strings.Split(out, "\n")
	cells := 0
	for i := range cl {
		if i >= len(al) {
			break
		}
		lineHasCell := false
		for o := 0; ; {
			j := strings.Index(cl[i][o:], cleanCell)
			if j < 0 {
				break
			}
			at := o + j
			o = at + 1
			if lineHasCell {
				continue
			}
			rest, ok := afterRowPrefix(al[i], cl[i][:at])
			if !ok {
				continue // rewritten line: cannot place the cell
			}
			if strings.HasPrefix(rest, cleanCell) {
				continue // untouched
			}
			end := at + len(cleanCell)
			tail, isCell := strings.CutPrefix(rest, cellWant)
			ends := tail == "" || tail[0] == ' ' || (end < len(cl[i]) && tail[0] == cl[i][end])
			if !isCell || !ends {
				return false // a raw value, a split line, or a cell that runs on
			}
			lineHasCell = true
			cells++
		}
	}
	return cells > 0
}

// afterRowPrefix reads line past the clean row prefix field by field, padding
// collapsed, and returns what follows. The separation before the cell must
// match the clean row's: padding where it had padding (a column boundary),
// none where it had none (a composed cell's own text, "Jonas/claude[").
func afterRowPrefix(line, prefix string) (string, bool) {
	rest := line
	for _, f := range strings.Fields(prefix) {
		var ok bool
		if rest, ok = strings.CutPrefix(strings.TrimLeft(rest, " "), f); !ok {
			return "", false
		}
	}
	trimmed := strings.TrimLeft(rest, " ")
	if strings.HasSuffix(prefix, " ") != (trimmed != rest) {
		return "", false
	}
	return trimmed, true
}

// TestCellInRowAnchorsOnTheRow pins the #1119 tightening: a sanitized value
// echoed on another line must not satisfy the cell assertion while the cell
// itself is raw or missing.
func TestCellInRowAnchorsOnTheRow(t *testing.T) {
	const want = "a b [31mc界…"
	clean := "NAME   ROLE       HOST\n" +
		"Iris   old-role   h1\n" +
		"note: role old-role is new\n"
	cases := []struct {
		name string
		out  string
		ok   bool
	}{
		{"cell in its row", "NAME   ROLE          HOST\n" +
			"Iris   " + want + "   h1\n" +
			"note: role old-role is new\n", true},
		{"cell raw, sanitized echo on the next line", "NAME   ROLE       HOST\n" +
			"Iris   a\nb\x1b[31mc界   h1\n" +
			"note: role " + want + " is new\n", false},
		{"cell in another row's line", "NAME   ROLE       HOST\n" +
			"Iris   old-role   h1\n" +
			"note: " + want + "\n", false},
		{"cell in the right line, wrong column", "NAME   ROLE       HOST\n" +
			"Iris   old-role   " + want + "\n" +
			"note: role old-role is new\n", false},
		{"sanitized headline, raw cell", "NAME   ROLE       HOST\n" +
			"Iris   a\nb\x1b[31mc界   h1\n" +
			"note: role " + want + " is new\n", false},
		{"cell and its echo both sanitized", "NAME   ROLE          HOST\n" +
			"Iris   " + want + "   h1\n" +
			"note: role " + want + " is new\n", true},
		{"row key changed", "NAME   ROLE          HOST\n" +
			"Ivan   " + want + "   h1\n" +
			"note: role old-role is new\n", false},
		{"cell runs on past the bound", "NAME   ROLE          HOST\n" +
			"Iris   " + want + "x   h1\n" +
			"note: role old-role is new\n", false},
		{"composed cell loses its closing text to the bound", "NAME   ROLE          HOST\n" +
			"Iris   " + want + "\n" +
			"note: role old-role is new\n", true},
	}
	// A headcount-style prose line: the cell ends at the row's own separator.
	line := "  role lead · family old-role: 1\n"
	for _, tc := range []struct {
		name string
		out  string
		ok   bool
	}{
		{"prose line keeps its separator", "  role lead · family " + want + ": 1\n", true},
		{"prose line, value echoed after the separator", "  role lead · family old-role: " + want + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellInRow(line, tc.out, "old-role", want); got != tc.ok {
				t.Errorf("cellInRow = %v, want %v", got, tc.ok)
			}
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellInRow(clean, tc.out, "old-role", want); got != tc.ok {
				t.Errorf("cellInRow = %v, want %v", got, tc.ok)
			}
		})
	}
}

// duplicateJSONKeys walks the ORIGINAL token stream and returns every key an
// object repeats. A decode into a map cannot see this: it keeps the last
// value and discards the duplicate before anyone looks (#1119).
func duplicateJSONKeys(body string) ([]string, error) {
	type frame struct {
		object    bool
		expectKey bool
		keys      map[string]bool
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var stack []*frame
	dups := []string{}
	value := func() { // a value completed in the current object
		if n := len(stack); n > 0 && stack[n-1].object {
			stack[n-1].expectKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				value()
				stack = append(stack, &frame{object: true, expectKey: true, keys: map[string]bool{}})
			case '[':
				value()
				stack = append(stack, &frame{})
			default:
				stack = stack[:len(stack)-1]
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].expectKey {
				top := stack[n-1]
				if top.keys[v] {
					dups = append(dups, v)
				}
				top.keys[v] = true
				top.expectKey = false
				continue
			}
			value()
		default:
			value()
		}
	}
	return dups, nil
}

func TestDuplicateJSONKeysReadsTheTokenStream(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"a":1,"b":{"a":2},"c":[{"a":3},{"a":4}]}`, []string{}},
		{`{"a":null,"a":"x"}`, []string{"a"}},
		{`{"s":{"k":{"x":1},"k":[1]},"t":"k"}`, []string{"k"}},
		{`[{"v":"v","v":"v"}]`, []string{"v"}},
	} {
		got, err := duplicateJSONKeys(tc.body)
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: got %v, want %v", tc.body, got, tc.want)
		}
	}
}
