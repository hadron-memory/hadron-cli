package spec

import "testing"

func TestUpperFirst(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"currently retired": "Currently retired",
		"Already":           "Already",
		"émigré":            "Émigré",
		"1 linked":          "1 linked",
	} {
		if got := upperFirst(in); got != want {
			t.Errorf("upperFirst(%q) = %q, want %q", in, got, want)
		}
	}
}
