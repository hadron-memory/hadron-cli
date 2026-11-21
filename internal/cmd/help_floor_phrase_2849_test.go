package cmd

import (
	"strings"
	"testing"
)

// cli#2849 F10: a help test that asserts a bare date (Contains(help, "2026-11-04"))
// is satisfied by any example that happens to carry that date, and broken by an
// unrelated one that carries the date it forbids. The floor is asserted as the
// phrase the help states it in: "server <date> or later".

// squashSpace2849 lowercases s and collapses every run of whitespace to one
// space, so a phrase the help wraps across a line break still matches.
func squashSpace2849(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// namesServerFloor2849 reports whether help states date as a server floor.
func namesServerFloor2849(help, date string) bool {
	return strings.Contains(squashSpace2849(help), "server "+date+" or later")
}

// namesFloorDate2849 reports whether text states date as a floor ("<date> or later"),
// for prose that does not say "server" just before it. Markdown bold is ignored.
func namesFloorDate2849(text, date string) bool {
	return strings.Contains(squashSpace2849(strings.ReplaceAll(text, "**", "")), date+" or later")
}

func TestFloorPhraseAssertionsAreNotFooledByAnExampleDate2849(t *testing.T) {
	const help = "Example: hadron x --since 2026-11-04T00:00:00Z\nNeeds server 2026-11-16 or\nlater."
	// The bare date is on the page, but only as an example's date: it is not the floor.
	if !strings.Contains(help, "2026-11-04") {
		t.Fatal("fixture: the page must carry the example date")
	}
	if namesServerFloor2849(help, "2026-11-04") {
		t.Error("an example date must not satisfy the floor assertion")
	}
	// The floor is found even where the help wraps it across a line.
	if !namesServerFloor2849(help, "2026-11-16") {
		t.Error("a floor wrapped across a line must still match")
	}
	// An unrelated page that carries the forbidden date elsewhere does not trip an absence check.
	if namesServerFloor2849("see 2026-11-03 in the log\nNeeds server 2026-11-04 or later", "2026-11-03") {
		t.Error("the forbidden date outside the floor phrase must not count as naming that floor")
	}
	if !namesFloorDate2849("the flag's base revision, **2026-11-03** or\nlater", "2026-11-03") {
		t.Error("a bold, wrapped floor in prose must match")
	}
	if namesFloorDate2849("run 2026-11-03 again", "2026-11-03") {
		t.Error("a bare date in prose is not a floor")
	}
}
