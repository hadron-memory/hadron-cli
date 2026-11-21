package cmd

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// updateGolden2508 rewrites the golden from the live page (cli#2525 F3): run
//
//	go test ./internal/cmd -run TestTeamWorkerCompileHelpGolden2508 -update-help-golden
//
// and say why in the commit that moves it.
var updateGolden2508 = flag.Bool("update-help-golden", false, "rewrite testdata/worker_compile_help_2508.golden from the live help")

// withoutGlobalFlags2508 drops the trailing "Global Flags:" block. Those flags belong to
// every command, so a new global flag must not fail a test about one page's own text
// (cli#2525 F3). The golden pins the page's description, usage, examples and own flags.
func withoutGlobalFlags2508(help string) string {
	if i := strings.Index(help, "\nGlobal Flags:\n"); i >= 0 {
		return help[:i+1]
	}
	return help
}

// cli#2508 (Jonas's BLIND F7 on #2499): `team worker compile --help` was kept
// byte-identical by hand while the shared bootcompile text was rewritten, and
// QA and LOCAL verified it — nothing pinned it. This golden does: the shared
// text cannot drift again without this test failing. When the page is MEANT
// to change, regenerate the golden deliberately with the -update-help-golden command
// above (it writes the page without the Global Flags block, which is what the test
// compares) and say why in the commit that moves it. (The golden stops before the Global Flags
// block; see withoutGlobalFlags2508. It still carries the citations the scrub policy
// bans on this page: `team worker compile` is on the citation ratchet's exception list
// until its own slice scrubs it.)
func TestTeamWorkerCompileHelpGolden2508(t *testing.T) {
	want, err := os.ReadFile("testdata/worker_compile_help_2508.golden")
	if err != nil {
		t.Fatal(err)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "compile", "--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := withoutGlobalFlags2508(out.String())
	if *updateGolden2508 {
		if err := os.WriteFile("testdata/worker_compile_help_2508.golden", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got != string(want) {
		gotL, wantL := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gotL) || i < len(wantL); i++ {
			var g, w string
			if i < len(gotL) {
				g = gotL[i]
			}
			if i < len(wantL) {
				w = wantL[i]
			}
			if g != w {
				t.Fatalf("team worker compile --help drifted from the golden at line %d:\n got: %q\nwant: %q\n(regenerate testdata/worker_compile_help_2508.golden only on purpose)", i+1, g, w)
			}
		}
		t.Fatal("team worker compile --help drifted from the golden (length)")
	}
}
