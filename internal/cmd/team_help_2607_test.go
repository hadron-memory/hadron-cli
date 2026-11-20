package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2607: the slice-B help follow-ups, each a literal the page must carry.
func TestTeamHelpSliceBFollowUps2607(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	longOf := func(path ...string) string {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		return c.Long
	}
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, c := range []struct {
		path []string
		want []string
	}{
		// 1, 15: the request page states its local refusal and drops the contradiction.
		{[]string{"team", "grant", "request"}, []string{`"pass --holder-id"`, "With --holder-id it sends"}},
		// 2: unbound, list and get refuse locally.
		{[]string{"team", "grant", "list"}, []string{"require a worker session"}},
		{[]string{"team", "grant", "get"}, []string{"require a worker session"}},
		// 8: the group says what the three things are.
		{[]string{"team", "grant"}, []string{"Three groups of commands"}},
		// 10: register pick prints one worker, or one entry per manager.
		{[]string{"team", "register", "pick"}, []string{"one entry per manager"}},
		// 11: the id of a grant is a <grant-id>, the signing request's is a <request-id>.
		{[]string{"team", "grant", "signing-request", "get"}, []string{"<grant-id>", "<request-id>"}},
		{[]string{"team", "grant", "request-signature"}, []string{"<grant-id> is the id that 'hadron team grant request' printed"}},
		// 12: the role-create suggestion is omitted when display sanitising would alter its argument.
		{[]string{"team", "role", "list"}, []string{"omitted"}},
		// 13: the removed-flag history lists --name-convention.
		{[]string{"team", "role"}, []string{"--name-convention"}},
		// 16: these phrases already stand on the pages (the slice only reflowed
		// them); the pin makes a reword deliberate, and the sweep-runs columns
		// are the field names the sweep-mode sentence uses.
		{[]string{"team", "app", "sweep-mode"}, []string{"planned, applied, skipped or failed"}},
		{[]string{"team", "host", "sweep-runs"}, []string{"PLANNED, APPLIED, SKIPPED, FAILED"}},
	} {
		got := flat(longOf(c.path...))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v help lacks %q", c.path, w)
			}
		}
	}
	// 3: get opens with what it does.
	if !strings.HasPrefix(longOf("team", "grant", "get"), "Read one worker grant") {
		t.Error("team grant get does not open with what it does")
	}
	// 9: the list page is list-only, it carries get's not-found exit.
	if strings.Contains(longOf("team", "grant", "list"), "exit 4") {
		t.Error("team grant list mentions get's not-found exit 4")
	}
	// 4: one version string on the rm page.
	c, _, err := root.Find([]string{"team", "grant", "rm"})
	if err != nil {
		t.Fatal(err)
	}
	usage := c.Flags().Lookup("instruction").Usage
	if !strings.Contains(c.Long, "Needs server 2026-11-04 or later") || !strings.Contains(usage, "Needs server 2026-11-04 or later") || strings.Contains(usage, "4bac6127") || strings.Contains(c.Long, "4bac6127") {
		t.Errorf("team grant rm names two versions: long %q, flag %q", flat(c.Long), usage)
	}
}

// cli#2607 item 14: the prose of the revised team grant and team register
// pages names a team grant or team register command with its "hadron" prefix,
// so it can be copied. The pin is scoped to those two families (not to every
// command named on the role pages).
func TestTeamHelpSliceBUsesOneCommandPrefix2607(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range [][]string{{"team", "grant"}, {"team", "grant", "add"}, {"team", "grant", "rm"}, {"team", "grant", "request"}, {"team", "grant", "signing-request", "get"}, {"team", "register", "pick"}, {"team", "register", "candidates"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(c.Long, "\n") {
			for _, bare := range []string{"'team grant ", "('team grant", "'team register ", "('team register"} {
				if strings.Contains(line, bare) {
					t.Errorf("%v: bare command in prose: %q", path, line)
				}
			}
		}
	}
}

// cli#2607 item 15: the CLI refuses fast, so the help may say so. With no
// binding and no --holder-id nothing is sent and the exit is 2.
func TestTeamGrantRequestUnboundWithoutHolderIDRefusesLocally2607(t *testing.T) {
	teamGitDir(t) // an empty git dir: no binding
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "grant", "request", "--action", "a.b", "--values", "{}", "--reason", "r"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "pass --holder-id") {
		t.Fatalf("want a local usage refusal naming --holder-id, got %v", err)
	}
}

// cli#2607 item 6: pickerHelp's two verbs keep their order wording, so a
// reword is deliberate.
func TestPickerHelpKeyPhrases2607(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, c := range []struct {
		verb string
		want []string
	}{
		{"pick", []string{"stops at the first match", "the worker you name with --worker", "the one least recently assigned", "one holder of the coordinator role", "the App's managers", "nearest first"}},
		{"candidates", []string{"equally near", "a worker's own Agent is depth 0 and each base link adds one", "all readable workers tied at the nearest depth"}},
	} {
		cmd, _, err := root.Find([]string{"team", "register", c.verb})
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(strings.Fields(cmd.Long), " ")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("register %s help lacks %q", c.verb, w)
			}
		}
	}
}

// cli#2607 item 2 (blind P2#1): the claim "unbound, list and get refuse
// locally" is behaviour, so it is run: exit 2, the stated message, nothing sent.
func TestTeamGrantListAndGetUnboundRefuseLocally2607(t *testing.T) {
	for _, argv := range [][]string{{"team", "grant", "list"}, {"team", "grant", "get", "g1"}} {
		teamGitDir(t) // an empty git dir: no binding
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(argv)
		err := root.Execute()
		if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "worker-bound grants require a worker session") {
			t.Errorf("%v: want a local usage refusal, got %v", argv, err)
		}
	}
}
