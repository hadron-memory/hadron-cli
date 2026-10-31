package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// cellInRow reports whether the attacked render carries cellWant AS the cell:
// at the column where the clean render carried cleanCell. (Today's check:
// any occurrence anywhere in the output, #1119 — the stand-in this pin is
// red against.)
func cellInRow(clean, out, cleanCell, cellWant string) bool {
	_ = clean
	_ = cleanCell
	return strings.Contains(out, cellWant)
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
		{"rest of the row changed", "NAME   ROLE          HOST\n" +
			"Iris   " + want + "   h2\n" +
			"note: role old-role is new\n", false},
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
