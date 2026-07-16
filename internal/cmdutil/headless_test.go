package cmdutil

import "testing"

func TestCanonicalNodeURNRejectsIncompletePrefixedNodeURN(t *testing.T) {
	if got, err := CanonicalNodeURN("hrn:node:acme.com"); err == nil {
		t.Fatalf("CanonicalNodeURN() = %q, want usage error", got)
	}
}
