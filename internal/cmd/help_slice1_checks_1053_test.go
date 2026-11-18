package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// cli#1053 slice 1: what an instructed call records is said per act. A revoke page
// that prints the LIST paragraph is copy-paste drift (audit finding C3), and a
// list page needs the list one.
func TestInstructionRecordParagraphMatchesTheAct1053(t *testing.T) {
	help := func(path ...string) string {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(path, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(out.String()), " ")
	}
	for _, p := range [][]string{{"app", "key", "revoke"}, {"app", "key", "scope", "revoke"}, {"app", "memory-grant", "rm"}} {
		h := help(p...)
		if strings.Contains(h, "NOT record-free") || strings.Contains(h, "every call (each page)") {
			t.Errorf("%v is a revoke page and must not carry the list paragraph", p)
		}
		if !strings.Contains(h, "A changing instructed revoke records one app.credential-act event") {
			t.Errorf("%v lacks the revoke paragraph", p)
		}
	}
	for _, p := range [][]string{{"app", "key", "list"}, {"app", "memory-grant", "list"}} {
		h := help(p...)
		if !strings.Contains(h, "An admitted instructed list is NOT record-free") {
			t.Errorf("%v is a list page and lacks the list paragraph", p)
		}
		if strings.Contains(h, "A changing instructed revoke") {
			t.Errorf("%v is a list page and must not carry the revoke paragraph", p)
		}
	}
}

// Prose in the help that names another command or flag ("hadron app key list
// --scopes") must name one that exists: a quoted reference that does not resolve
// sends the reader nowhere. Checked for every app and agent page.
func TestSlice1HelpCrossReferencesResolve1053(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	quoted := regexp.MustCompile("[\"`]hadron ([^\"`\\n]+)[\"`]")
	checked := 0
	for _, page := range collectSlice1Pages(root) {
		for _, m := range quoted.FindAllStringSubmatch(page.Long, -1) {
			var words, flags []string
			for _, tok := range strings.Fields(m[1]) {
				switch {
				case strings.HasPrefix(tok, "--"):
					flags = append(flags, strings.TrimRight(strings.SplitN(tok, "=", 2)[0], ".,;:)"))
				case strings.HasPrefix(tok, "<") || strings.HasPrefix(tok, "[") || strings.HasPrefix(tok, "-") || len(flags) > 0:
				default:
					words = append(words, strings.TrimRight(tok, ".,;:)"))
				}
			}
			// A reference ends at the first word that is not a command name.
			cmd, rest, err := root.Find(words)
			if err != nil || cmd == nil {
				t.Errorf("%s: %q names no command", page.CommandPath(), m[0])
				continue
			}
			for len(rest) > 0 && !cmd.HasSubCommands() {
				rest = rest[:len(rest)-1] // trailing arguments (a ref, an id) are not part of the path
			}
			if len(rest) > 0 {
				t.Errorf("%s: %q names no command (stopped at %q)", page.CommandPath(), m[0], cmd.CommandPath())
				continue
			}
			for _, fl := range flags {
				name := strings.TrimPrefix(fl, "--")
				if name == "help" {
					continue // cobra adds --help to every command when it runs
				}
				if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil && cmd.PersistentFlags().Lookup(name) == nil {
					t.Errorf("%s: %q names the flag --%s, which %s does not have", page.CommandPath(), m[0], name, cmd.CommandPath())
				}
			}
			checked++
		}
	}
	if checked < 10 {
		t.Fatalf("checked only %d cross-references; the pattern or the walk is wrong", checked)
	}
}
