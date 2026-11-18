package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// cli#1053 slice 1: what an instructed call records is said per act. A revoke page
// that prints the LIST paragraph is copy-paste drift (audit finding C3), and a
// list page needs the list one. Every app and agent page that carries the shared
// --instruction help is found by walking the tree and classified by its verb, so a
// page added later cannot slip through unchecked.
func TestInstructionRecordParagraphMatchesTheAct1053(t *testing.T) {
	const marker = "claims, for this call, that the worker's holder"
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	seen := 0
	for _, page := range collectSlice1Pages(root) {
		long := flat(page.Long)
		hasList := strings.Contains(long, "An admitted instructed list is NOT record-free")
		hasRevoke := strings.Contains(long, "A changing instructed revoke records one app.credential-act event")
		if !strings.Contains(long, marker) {
			if hasList || hasRevoke {
				t.Errorf("%s carries a record paragraph without the --instruction help", page.CommandPath())
			}
			continue
		}
		seen++
		verb := page.Name()
		switch verb {
		case "list":
			if !hasList || hasRevoke {
				t.Errorf("%s is a list page: want the list paragraph only (list=%v revoke=%v)", page.CommandPath(), hasList, hasRevoke)
			}
		case "revoke", "rm":
			if !hasRevoke || hasList || strings.Contains(long, "every call (each page)") {
				t.Errorf("%s is a revoke page: want the revoke paragraph only (list=%v revoke=%v)", page.CommandPath(), hasList, hasRevoke)
			}
		default:
			t.Errorf("%s carries the --instruction help but its verb %q is neither list nor revoke: classify it", page.CommandPath(), verb)
		}
	}
	if seen < 5 {
		t.Fatalf("found only %d pages with the --instruction help; the walk or the marker is wrong", seen)
	}
}

// Prose in the help that names another command or flag ("hadron app key list
// --scopes") must name one that exists: a quoted reference that does not resolve
// sends the reader nowhere. Checked for every app and agent page.
func TestSlice1HelpCrossReferencesResolve1053(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	quoted := regexp.MustCompile("\"hadron ([^\"]+)\"|`hadron ([^`]+)`")
	checked := 0
	for _, page := range collectSlice1Pages(root) {
		for _, m := range quoted.FindAllStringSubmatch(strings.Join(strings.Fields(page.Long), " "), -1) {
			ref := m[1] + m[2] // exactly one alternative matched
			var words, flags []string
			for _, tok := range strings.Fields(ref) {
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
