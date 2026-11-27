package billing

import "testing"

// hadron-cli#3185: the snapshot re-pin made the ledger's organization id nullable.
// These commands drive organization ledgers, so a value passes through unchanged and
// a null (a person's ledger, not reachable from here) prints as empty.
func TestStringOrEmpty(t *testing.T) {
	v := "org-1"
	for name, c := range map[string]struct {
		in   *string
		want string
	}{"nil": {nil, ""}, "value": {&v, "org-1"}, "empty value": {new(string), ""}} {
		if got := stringOrEmpty(c.in); got != c.want {
			t.Errorf("%s: stringOrEmpty = %q, want %q", name, got, c.want)
		}
	}
}
