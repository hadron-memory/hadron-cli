package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// cli#2508 (Jonas's BLIND F11 on #2499, evaluator C1): user help carries no
// spec citations, issue numbers or commit hashes. A reader of --help cannot
// resolve cor:dmo:050:09, cannot follow a GitHub issue number from a terminal,
// and cannot compare a server revision hash against the server they run; those
// are provenance for this repository and its specs, not for the help page.
// TestAgentHelpKeepsTheLifecycleParagraphWhole2171 pins this for agent create
// and update; this test walks every page under the app and agent trees, so a
// new page cannot reintroduce them.
//
// Server floors are still stated — as dates ("server 2026-11-03 or later"),
// the shape the #2499 review settled on — not as hashes.

var bannedInHelp2508 = []*regexp.Regexp{
	regexp.MustCompile(`cor:[a-z]+:[0-9]+`),                     // spec citations
	regexp.MustCompile(`#[0-9]{2,}`),                            // issue numbers, with any prefix
	regexp.MustCompile(`[^0-9a-zA-Z]([0-9a-f]{8})[^0-9a-zA-Z]`), // revision hashes
}

// helpTree2508 renders --help for the group and every visible command below it.
func helpTree2508(t *testing.T, group string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	var walk func(path []string)
	walk = func(path []string) {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, path...), "--help"))
		root.SetOut(out)
		root.SetErr(out)
		if err := root.Execute(); err != nil {
			t.Fatalf("%s --help: %v", strings.Join(path, " "), err)
		}
		pages[strings.Join(path, " ")] = out.String()
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			walk(append(append([]string{}, path...), sub.Name()))
		}
	}
	walk([]string{group})
	return pages
}

// firstBannedLine2508 names one offending line so the failure points at the
// text, not just the pattern.
func firstBannedLine2508(help string, re *regexp.Regexp) string {
	for _, l := range strings.Split(help, "\n") {
		if re.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

func TestAppAndAgentHelpCarriesNoCitationsNumbersOrHashes2508(t *testing.T) {
	for _, group := range []string{"app", "agent"} {
		for name, help := range helpTree2508(t, group) {
			for _, re := range bannedInHelp2508 {
				if m := re.FindString(help); m != "" {
					t.Errorf("%s --help carries %q (e.g. %q)", name, m, firstBannedLine2508(help, re))
				}
			}
		}
	}
}
