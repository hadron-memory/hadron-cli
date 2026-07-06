// Package output renders command results as human-readable text or
// JSON. Commands build explicit DTOs for their output — never
// genqlient-generated structs — so the --json contract stays stable
// across schema regenerations.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// Write renders v as indented JSON when asJSON is set, otherwise
// calls table to render the human view.
func Write(s *IOStreams, asJSON bool, v any, table func(w io.Writer) error) error {
	if asJSON {
		return WriteJSON(s.Out, v)
	}
	return table(s.Out)
}

// WriteJSON marshals v as indented JSON followed by a newline.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Table is a thin tabwriter wrapper for aligned columnar output.
type Table struct {
	tw *tabwriter.Writer
}

func NewTable(w io.Writer, headers ...string) *Table {
	t := &Table{tw: tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)}
	if len(headers) > 0 {
		t.Row(headers...)
	}
	return t
}

// Dash renders an optional string cell: the value, or an em-dash placeholder
// when the pointer is nil or empty. Keeps "no value" columns consistent across
// the table-rendering commands.
func Dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

func (t *Table) Row(cells ...string) {
	for i, c := range cells {
		if i > 0 {
			fmt.Fprint(t.tw, "\t")
		}
		fmt.Fprint(t.tw, c)
	}
	fmt.Fprintln(t.tw)
}

func (t *Table) Flush() error { return t.tw.Flush() }
