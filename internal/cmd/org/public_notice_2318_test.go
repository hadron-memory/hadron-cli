package org

import (
	"bytes"
	"testing"
)

// cli#2318: printPublicResources takes the offset that was sent and whether
// --offset was given as two values, not one -1 sentinel. Output is unchanged:
// the "(offset O)" suffix appears on a partial list exactly when the flag was
// given (zero included), and never on a complete list or "(none)".
func TestPrintPublicResourcesOffsetPresence2318(t *testing.T) {
	rows := []publicResourceDTO{{Name: "A", URN: "u:a"}, {Name: "B", URN: "u:b"}}
	for _, tc := range []struct {
		name    string
		rs      []publicResourceDTO
		total   int
		hasMore bool
		offset  int
		given   bool
		want    string
	}{
		{"partial, flag omitted", rows, 45, true, 0, false, "  l: showing 2 of 45\n"},
		{"partial, flag given as zero", rows, 45, true, 0, true, "  l: showing 2 of 45 (offset 0)\n"},
		{"partial, flag given positive", rows, 45, false, 40, true, "  l: showing 2 of 45 (offset 40)\n"},
		{"partial, positive offset but flag not given", rows, 45, true, 40, false, "  l: showing 2 of 45\n"},
		{"complete, flag given", rows, 2, false, 5, true, "  l:\n"},
		{"empty, flag given", nil, 0, false, 5, true, "  l: (none)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			printPublicResources(&b, "l", tc.rs, tc.total, tc.hasMore, tc.offset, tc.given)
			got := b.String()
			first := got
			for i := range got {
				if got[i] == '\n' {
					first = got[:i+1]
					break
				}
			}
			if first != tc.want {
				t.Fatalf("header %q, want %q", first, tc.want)
			}
		})
	}
}
