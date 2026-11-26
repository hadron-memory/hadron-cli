package cmd

// Pins for the help polish on the team pages (hadron-cli#2969, #2992): ragged
// or overlong wrapping left where a citation was deleted, the team events
// handle sentence, and the two server floors' exact "on or after" wording, so
// a later rewording (say "released after") fails a check instead of passing
// every existing one.

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// paragraphFrom returns the help paragraph (consecutive non-blank lines) that
// contains anchor, as its lines, and fails the test when there is none.
func paragraphFrom(t *testing.T, help, anchor string) []string {
	t.Helper()
	for _, par := range strings.Split(help, "\n\n") {
		if strings.Contains(par, anchor) {
			return strings.Split(par, "\n")
		}
	}
	t.Fatalf("no paragraph contains %q:\n%s", anchor, help)
	return nil
}

// raggedLines lists the lines of a hand-wrapped paragraph that end short:
// the first word of the next line would still have fit within width. A reflowed
// paragraph is filled greedily, so it has none; a citation deleted from the
// middle of a line leaves one behind.
func raggedLines(lines []string, width int) []string {
	var out []string
	for i := 0; i+1 < len(lines); i++ {
		next := strings.TrimSpace(lines[i+1])
		if next == "" {
			continue
		}
		word := firstWord(next)
		if utf8.RuneCountInString(lines[i])+1+utf8.RuneCountInString(word) <= width {
			out = append(out, lines[i])
		}
	}
	return out
}

// firstWord is the first unbreakable word of a line: a code span such as
// `team events --x` is one word, with any punctuation stuck to its end.
func firstWord(line string) string {
	end := strings.IndexByte(line, ' ')
	if strings.HasPrefix(line, "`") {
		if c := strings.IndexByte(line[1:], '`'); c >= 0 {
			rest := line[c+2:]
			end = c + 2 + strings.IndexByte(rest+" ", ' ')
		}
	}
	if end < 0 {
		return line
	}
	return line[:end]
}

// overlongLines lists the lines wider than width, counted in runes.
func overlongLines(lines []string, width int) []string {
	var out []string
	for _, l := range lines {
		if utf8.RuneCountInString(l) > width {
			out = append(out, l)
		}
	}
	return out
}

// helpWrapWidth is the column the polished paragraphs are hand-wrapped to: a
// line may not exceed it (overlongLines), and a line may not end so short that
// the next word would still have fit (raggedLines).
const helpWrapWidth = 78

// The server floors the team pages state: one constant each, so the wording
// pins and the dates they pin cannot drift apart.
const (
	registerServerFloor = "2026-11-03"
	eventsServerFloor   = "2026-11-14"
)

// Every paragraph the scrub or the polish rounds touched, on each page: filled
// greedily to helpWrapWidth (no ragged line, none overlong), and, where first is
// set, opening with that exact line, so restoring the old wording fails even
// when the old text happened to wrap acceptably.
func TestHelpParagraphsAreWrapped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		page   []string
		anchor string
		first  string // exact first line of the paragraph, when pinned
		skip   int    // leading lines of the paragraph that are a heading, not prose
	}{
		{"team alarms: IN EFFECT", []string{"team", "alarms"}, "IN EFFECT is the server's answer",
			"IN EFFECT is the server's answer: unacknowledged and with no escalation end", 0},
		{"team alarms: task-less", []string{"team", "alarms"}, "Only alarms on a task appear",
			"Only alarms on a task appear; task-less alarms (dark-worker,", 0},
		{"team events: event types", []string{"team", "events"}, "append-only stream of task work",
			"Read the team App's append-only stream of task work \u2014 task.created,", 0},
		{"team init: -m override", []string{"team", "init"}, "so no memory needs naming", "", 0},
		{"team init: group chat", []string{"team", "init"}, "The team group chat needs no init", "", 0},
		{"team: step 3", []string{"team"}, "(optional) A role definition", "", 1},
		{"team register: floor", []string{"team", "register"}, "Requires a server released", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			help := cmdHelpOut(t, tc.page...)
			lines := paragraphFrom(t, help, tc.anchor)
			if tc.first != "" && lines[0] != tc.first {
				t.Errorf("paragraph opens %q, want %q", lines[0], tc.first)
			}
			lines = lines[tc.skip:]
			if bad := raggedLines(lines, helpWrapWidth); len(bad) != 0 {
				t.Errorf("lines end short (the next word would have fit in %d columns): %q", helpWrapWidth, bad)
			}
			if bad := overlongLines(lines, helpWrapWidth); len(bad) != 0 {
				t.Errorf("lines are wider than %d columns: %q", helpWrapWidth, bad)
			}
			if strings.Contains(strings.Join(lines, "\n"), "on\nor after") {
				t.Error("the floor splits \"on or after\" across two lines")
			}
		})
	}
}

// eventsHandleSentence is the TASK-cell sentence of team events, as one line:
// from "A task event's TASK cell" to the end of the server-floor sentence. The
// page has other prose around it, so the checks below read only this span.
func eventsHandleSentence(t *testing.T) string {
	t.Helper()
	flat := strings.Join(strings.Fields(cmdHelpOut(t, "team", "events")), " ")
	start := strings.Index(flat, "A task event's TASK cell")
	if start < 0 {
		t.Fatalf("team events has no TASK-cell sentence:\n%s", flat)
	}
	end := strings.Index(flat[start:], "nothing is printed.")
	if end < 0 {
		t.Fatalf("the TASK-cell sentence does not end where expected:\n%s", flat[start:])
	}
	return flat[start : start+end+len("nothing is printed.")]
}

// The events handle sentence names the thing once, and says whose release.
func TestEventsHandleSentenceIsTight(t *testing.T) {
	s := eventsHandleSentence(t)
	if n := strings.Count(s, "task handle"); n != 0 {
		t.Errorf("the handle is named once, as the task's handle in the TASK cell, not %d more times as \"task handle\":\n%s", n, s)
	}
	if want := "Showing the handle needs a server released on or after " + eventsServerFloor; !strings.Contains(s, want) {
		t.Errorf("the sentence must say what needs the server (%q):\n%s", want, s)
	}
	if strings.Contains(s, "it carries") {
		t.Errorf("the unstated \"it\" is gone:\n%s", s)
	}
}

// onOrAfterWords matches the floor phrase with whatever whitespace separates its words.
var onOrAfterWords = regexp.MustCompile(`on(\s+)or(\s+)after`)

// The events floor sentence is in the TASK-cell paragraph, which is not a
// TestHelpParagraphsAreWrapped case, and eventsHandleSentence flattens
// whitespace, so neither would see a re-wrap that splits "on or after". Read the
// raw help: no line break may fall inside the phrase, on either page (cli#3170).
func TestFloorOnOrAfterIsNotSplitAcrossLines(t *testing.T) {
	// cli#3216 item 6: also the task pages that carry the phrase (taskclone.go, taskread.go).
	for _, page := range [][]string{{"team", "events"}, {"team", "register"}, {"team", "task", "clone"}, {"team", "task", "get"}, {"team", "task", "list"}} {
		raw := cmdHelpOut(t, page...)
		// Presence uses the same whitespace-tolerant pattern as the split check, so a re-wrapped phrase
		// is reported as split, not as missing.
		if !onOrAfterWords.MatchString(raw) {
			t.Fatalf("%v: no \"on or after\" floor wording on the raw help:\n%s", page, raw)
		}
		// Any whitespace run holding a line break splits the phrase, indented continuation lines
		// included (cli#3189 item 6); a plain two-form substring check missed "on or\n    after".
		for _, m := range onOrAfterWords.FindAllStringSubmatch(raw, -1) {
			if strings.Contains(m[1], "\n") || strings.Contains(m[2], "\n") {
				t.Errorf("%v: the floor splits \"on or after\" across two lines (%q)", page, m[0])
			}
		}
	}
}

// The two floors are one date each, "on or after", on both pages.
func TestServerFloorWordingIsPinned(t *testing.T) {
	for _, tc := range []struct {
		page []string
		date string
	}{
		{[]string{"team", "register"}, registerServerFloor},
		{[]string{"team", "events"}, eventsServerFloor},
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
