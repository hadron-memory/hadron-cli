package team

import "testing"

// cli#2986: matchFailureCell, the LAST MATCH FAILURE cell, in isolation. A nil failure is the null
// word; a null reason is the time alone (nothing recorded); an empty or blank reason is the time and
// the placeholder (a reason the server served, with nothing in it); a real reason is the time and the
// reason as served; a reason that is literally the placeholder word is a real reason and prints as is.
func TestMatchFailureCell2986(t *testing.T) {
	str := func(s string) *string { return &s }
	const at = "2026-11-22T05:59:00Z"
	for _, tc := range []struct {
		name string
		in   *utilizationMatchFailureDTO
		want string
	}{
		{"none", nil, nullCell},
		{"null reason", &utilizationMatchFailureDTO{At: at}, at},
		{"empty reason", &utilizationMatchFailureDTO{At: at, Reason: str("")}, at + " (empty)"},
		{"blank reason", &utilizationMatchFailureDTO{At: at, Reason: str("  \t ")}, at + " (empty)"},
		{"real reason", &utilizationMatchFailureDTO{At: at, Reason: str("REQUIREMENTS_EXCLUDED")}, at + " REQUIREMENTS_EXCLUDED"},
		{"reason that is the placeholder word", &utilizationMatchFailureDTO{At: at, Reason: str("(empty)")}, at + " (empty)"},
		{"reason with a control character", &utilizationMatchFailureDTO{At: at, Reason: str("A\x1b[31mB")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := matchFailureCell(tc.in)
			if tc.want == "" {
				for _, r := range got {
					if r == 0x1b {
						t.Fatalf("a control character reached the cell: %q", got)
					}
				}
				return
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
