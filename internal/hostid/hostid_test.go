package hostid

import (
	"strings"
	"testing"
)

func TestValid3239(t *testing.T) {
	for id, want := range map[string]bool{
		"orca1": true, "holger-mbp4": true, "a.b_c:d-e": true, "9": true,
		strings.Repeat("a", 128): true, strings.Repeat("a", 129): false,
		"": false, "..": false, "-a": false, ".a": false, "a/b": false, "a b": false, " a": false, "a\\b": false,
	} {
		if got := Valid(id); got != want {
			t.Errorf("Valid(%q) = %v, want %v", id, got, want)
		}
	}
}
