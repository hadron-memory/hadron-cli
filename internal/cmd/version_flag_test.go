package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/build"
)

// #393: `--version` and `-v` are what a human reaches for first — and both were
// unknown flags, so only the bare `version` subcommand worked. Setting
// root.Version registers both; the template makes all three spellings agree,
// and the output must route through the factory streams (not os.Stdout) so it
// is captured here.
func TestVersionFlagSpellings(t *testing.T) {
	want := "hadron " + build.Version + " (" + build.Commit + ", " + build.Date + ")"
	for _, argv := range [][]string{{"--version"}, {"-v"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(argv)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		if got := strings.TrimSpace(out.String()); got != want {
			t.Errorf("%v printed %q, want %q", argv, got, want)
		}
	}
}
