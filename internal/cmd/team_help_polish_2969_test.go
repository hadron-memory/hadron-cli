package cmd

// hadron-cli#2969: the follow-ups from the citation scrub of the team pages.
// Items 1 and 2 are help text (ragged wrapping left where a citation was
// deleted; the team events handle sentence); item 3 pins the two server
// floors' exact "on or after" wording, so a later rewording (say "released
// after") fails a check instead of passing every existing one.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// paragraphFrom returns the help paragraph (consecutive non-blank lines) that
// contains anchor, as its lines, and fails the test when there is none.
func paragraphFrom2969(t *testing.T, help, anchor string) []string {
	t.Helper()
	for _, par := range strings.Split(help, "\n\n") {
		if strings.Contains(par, anchor) {
			return strings.Split(par, "\n")
		}
	}
	t.Fatalf("no paragraph contains %q:\n%s", anchor, help)
	return nil
}

// raggedLines2969 lists the lines of a hand-wrapped paragraph that end short:
// the first word of the next line would still have fit within width. A reflowed
// paragraph is filled greedily, so it has none; a citation deleted from the
// middle of a line leaves one behind.
func raggedLines2969(lines []string, width int) []string {
	var out []string
	for i := 0; i+1 < len(lines); i++ {
		next := strings.Fields(lines[i+1])
		if len(next) == 0 {
			continue
		}
		if utf8.RuneCountInString(lines[i])+1+utf8.RuneCountInString(next[0]) <= width {
			out = append(out, lines[i])
		}
	}
	return out
}

// Item 1: the three pages the scrub left ragged, plus register's split floor line.
func TestHelpParagraphsAreNotRagged2969(t *testing.T) {
	for _, tc := range []struct {
		name   string
		page   []string
		anchor string
		skip   int // leading lines of the paragraph that are a heading, not prose
	}{
		{"team alarms: IN EFFECT", []string{"team", "alarms"}, "IN EFFECT is the server's answer", 0},
		{"team init: -m override", []string{"team", "init"}, "stays as an explicit override", 0},
		{"team init: group chat", []string{"team", "init"}, "The team group chat needs no init", 0},
		{"team: step 3", []string{"team"}, "(optional) A role definition", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			help := cmdHelpOut(t, tc.page...)
			if bad := raggedLines2969(paragraphFrom2969(t, help, tc.anchor)[tc.skip:], 78); len(bad) != 0 {
				t.Errorf("lines end short (the next word would have fit in 78 columns): %q", bad)
			}
		})
	}
	// register's floor sentence must not split "on or after" across lines.
	if strings.Contains(cmdHelpOut(t, "team", "register"), "on\nor after") {
		t.Error("team register splits \"on or after\" across two lines")
	}
}

// Item 2: the events handle sentence names the thing once, and says whose release.
func TestEventsHandleSentenceIsTight2969(t *testing.T) {
	help := cmdHelpOut(t, "team", "events")
	flat := strings.Join(strings.Fields(help), " ")
	if n := strings.Count(flat, "task handle"); n != 0 {
		t.Errorf("the handle is named once, as the task's handle in the TASK cell, not %d more times as \"task handle\":\n%s", n, flat)
	}
	if !strings.Contains(flat, "Showing the handle needs a server released on or after 2026-11-14") {
		t.Errorf("the sentence must say what needs the server:\n%s", flat)
	}
	if strings.Contains(flat, "it carries") {
		t.Errorf("the unstated \"it\" is gone:\n%s", flat)
	}
}

// Item 3: the two floors are one date each, "on or after", on both pages.
func TestServerFloorWordingIsPinned2969(t *testing.T) {
	for _, tc := range []struct {
		page []string
		date string
	}{
		{[]string{"team", "register"}, "2026-11-03"},
		{[]string{"team", "events"}, "2026-11-14"},
	} {
		flat := strings.Join(strings.Fields(cmdHelpOut(t, tc.page...)), " ")
		if !strings.Contains(flat, "released on or after "+tc.date) {
			t.Errorf("%v: the floor reads \"released on or after %s\":\n%s", tc.page, tc.date, flat)
		}
		if strings.Contains(flat, "released after") {
			t.Errorf("%v: \"released after\" would leave the floor day out:\n%s", tc.page, flat)
		}
	}
}
