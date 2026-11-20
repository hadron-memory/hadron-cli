package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// cli#2617: the deferred help P3s of the slice-A seat reviews (#2578). Each
// test pins one finding against the live command tree, so a regression of the
// help text fails here.

// sliceAHelpText2617 is every slice-A page's Long and Example, keyed by page.
func sliceAHelpText2617(t *testing.T) map[string]string {
	t.Helper()
	f, _ := testFactory(t)
	out := map[string]string{}
	for _, page := range collectSliceAPages(NewRootCmd(f)) {
		out[strings.TrimPrefix(page.CommandPath(), "hadron ")] = page.Long + "\n" + page.Example
	}
	return out
}

// F1: no help line of these pages runs past 150 columns; a longer one is
// rewrapped (Juniper F1, Jonas P3-8: `sweep-runs` Long was one physical line).
func TestSliceAHelpLinesAreWrapped2617(t *testing.T) {
	for page, text := range sliceAHelpText2617(t) {
		for i, line := range strings.Split(text, "\n") {
			if n := utf8.RuneCountInString(line); n > 150 {
				t.Errorf("%s: help line %d is %d columns (limit 150): %.60s…", page, i+1, n, line)
			}
		}
	}
}

// P3-8: the cor:agt:070 spec ids are internal; no slice-A page prints them
// (host_run.go kept them while the other pages had them stripped).
func TestSliceAHelpCarriesNoInternalSpecIDs2617(t *testing.T) {
	for page, text := range sliceAHelpText2617(t) {
		if strings.Contains(text, "cor:agt:") {
			t.Errorf("%s: help prints an internal spec id (cor:agt:…)", page)
		}
	}
}

// F2: a ticker page names the server revision once, not as a commit on the
// flag and as a date in the shared instruction text.
func TestTickerSettingsSetNamesOneServerRevision2617(t *testing.T) {
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"team", "ticker", "settings", "set"})
	if err != nil {
		t.Fatal(err)
	}
	help := cmd.Long + "\n" + cmd.Flags().Lookup("instruction").Usage
	if n := strings.Count(help, "Needs server"); n != 2 {
		t.Fatalf("expected the flag and the shared text to each say Needs server once, got %d", n)
	}
	if strings.Contains(help, "Needs server 4bac6127") {
		t.Error("the --instruction flag still names the commit 4bac6127 while the shared text says 2026-11-04; one revision string per page")
	}
}

// P3-5: ticker settings set states each bound once and does not repeat the
// floor sentence.
func TestTickerSettingsSetHelpStatesFloorOnce2617(t *testing.T) {
	text := sliceAHelpText2617(t)["team ticker settings set"]
	if strings.Contains(text, "bounds how high the floor may rise") {
		t.Error("the floor-above-an-existing-cadence limit is stated twice; keep one sentence")
	}
	if !strings.Contains(strings.Join(strings.Fields(text), " "), "the examples show --app first, then the App context") {
		t.Error("the App-source sentence must say which spelling each example uses")
	}
}

// P3-5: host.go lists `up` among the commands that need host.yaml; up needs no
// host.yaml (--config is only its separate reporting key), and `issue` omits
// the sweepMode APPLY caveat for `up --apply`.
func TestHostHelpPrerequisitesAndIssueCaveat2617(t *testing.T) {
	text := sliceAHelpText2617(t)
	flat := strings.Join(strings.Fields(text["team host"]), " ")
	i := strings.Index(flat, "host-local commands")
	if i < 0 {
		t.Fatal("team host help lost its host-local commands sentence")
	}
	sentence := flat[i:]
	if j := strings.Index(sentence, "host.yaml"); j > 0 {
		sentence = sentence[:j]
	}
	if strings.Contains(sentence, "`up`") {
		t.Errorf("team host lists `up` among the commands that need a host.yaml: %q", sentence)
	}
	if !strings.Contains(text["team host issue"], "sweepMode") {
		t.Error("team host issue points at `up --apply` without the sweepMode APPLY caveat")
	}
}

// P3-7: `import` help explains leave-alone in plain words, not "lane".
func TestHostImportHelpAvoidsLaneJargon2617(t *testing.T) {
	text := sliceAHelpText2617(t)["team host import"]
	if strings.Contains(text, "lane is released") {
		t.Error("team host import help says the sweep leaves a worker alone \"until its lane is released\"")
	}
}
