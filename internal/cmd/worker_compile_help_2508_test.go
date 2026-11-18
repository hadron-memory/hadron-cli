package cmd

import (
	"os"
	"strings"
	"testing"
)

// cli#2508 (Jonas's BLIND F7 on #2499): `team worker compile --help` was kept
// byte-identical by hand while the shared bootcompile text was rewritten, and
// QA and LOCAL verified it — nothing pinned it. This golden does: the shared
// text cannot drift again without this test failing. When the page is MEANT
// to change, regenerate the golden deliberately with
//
//	bin/hadron team worker compile --help > internal/cmd/testdata/worker_compile_help_2508.golden
//
// and say why in the commit that moves it.
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
	if got := out.String(); got != string(want) {
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
