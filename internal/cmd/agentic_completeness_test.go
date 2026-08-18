package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
)

// TestAgenticUsageDocumentsEveryCommand keeps the embedded agentic-usage.md — the
// authoritative command contract — from drifting behind the command tree: every
// leaf command must appear on its group's `hadron <group>` surface line (the
// exact class of gap that let `spec use` ship undocumented). Add a command,
// document it here, or the build fails.
//
// Exceptions:
//   - cobra built-ins (help, completion + its per-shell leaves) — auto-generated.
//   - groups documented in prose without a surface line — their leaf must still
//     appear somewhere in the doc.
func TestAgenticUsageDocumentsEveryCommand(t *testing.T) {
	doc := agentic.Doc()

	// Groups intentionally covered in prose rather than a `hadron <group>` line.
	proseGroups := map[string]bool{"access": true}
	// Whole subtrees to skip (cobra-generated, not part of the contract).
	skipGroup := map[string]bool{"help": true, "completion": true}

	f, _ := testFactory(t)
	root := NewRootCmd(f)

	// Index the surface lines by their group (first word after "hadron ").
	groupLine := map[string]string{}
	for _, ln := range strings.Split(doc, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "hadron "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				groupLine[fields[0]] += " " + ln
			}
		}
	}

	// Collect leaf commands (no subcommands). The doc may name a leaf by any of
	// its aliases (e.g. `ls` for `list`, `add` for `create`), so we match against
	// the primary name OR any alias.
	type leaf struct {
		group string
		names []string // primary + aliases
	}
	var leaves []leaf
	var visit func(c *cobra.Command, group string)
	visit = func(c *cobra.Command, group string) {
		subs := c.Commands()
		if len(subs) == 0 {
			leaves = append(leaves, leaf{group: group, names: append([]string{c.Name()}, c.Aliases...)})
			return
		}
		for _, sc := range subs {
			g := group
			if group == "" {
				g = sc.Name() // a top-level command is its own group
			}
			visit(sc, g)
		}
	}
	visit(root, "")

	// Match a command name as a whole token on a surface line, tolerating the
	// `|`-separated shorthand (e.g. "create|ls|validate|revoke", "| use <urn>").
	wordRE := func(w string) *regexp.Regexp {
		return regexp.MustCompile(`(^|[^a-zA-Z0-9-])` + regexp.QuoteMeta(w) + `([^a-zA-Z0-9-]|$)`)
	}

	for _, lf := range leaves {
		if skipGroup[lf.group] {
			continue
		}
		if proseGroups[lf.group] {
			if !strings.Contains(doc, lf.group) {
				t.Errorf("prose group %q not mentioned in agentic-usage.md", lf.group)
			}
			continue
		}
		line, ok := groupLine[lf.group]
		if !ok {
			t.Errorf("command group %q has no `hadron %s …` surface line in agentic-usage.md", lf.group, lf.group)
			continue
		}
		documented := false
		for _, n := range lf.names {
			if wordRE(n).MatchString(line) {
				documented = true
				break
			}
		}
		if !documented {
			t.Errorf("`hadron %s %s` is not on the %q surface line in agentic-usage.md — add it (or update the doc)", lf.group, lf.names[0], lf.group)
		}
	}
}

// TestRefTakingCommandsAdvertiseRef keeps the DISCOVERY surfaces honest about
// which arguments accept a URN.
//
// hadron-server#789 made `updateAgent`/`deleteAgent` accept a PK **or** a
// fully-qualified URN (they were PK-only). The CLI already forwarded whatever
// the user typed, so the capability worked the moment the server shipped — but
// `agent update <id>` / `agent rm <id>` in the help, and the same wording in
// agentic-usage.md, still told callers to pre-resolve an ID. A capability
// nobody can discover is not shipped (Codex review, hadron-cli#342).
//
// `<ref>` is the established spelling for a PK-or-URN positional across this
// CLI (`agent get <ref>`, `memory get <memoryRef>`), so pin it.
func TestRefTakingCommandsAdvertiseRef(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)

	// group -> leaf commands whose first positional accepts a PK or a URN.
	refTaking := map[string][]string{
		"agent": {"get", "update", "rm"},
	}

	for group, leaves := range refTaking {
		var groupCmd *cobra.Command
		for _, c := range root.Commands() {
			if c.Name() == group {
				groupCmd = c
				break
			}
		}
		if groupCmd == nil {
			t.Fatalf("command group %q not found", group)
		}
		for _, leaf := range leaves {
			var cmd *cobra.Command
			for _, c := range groupCmd.Commands() {
				if c.Name() == leaf {
					cmd = c
					break
				}
			}
			if cmd == nil {
				t.Fatalf("%s %s not found", group, leaf)
			}
			// Use line must say <ref>, never <id> — the latter tells a caller
			// (or a shelling agent) that a URN will be rejected.
			if strings.Contains(cmd.Use, "<id>") {
				t.Errorf("`%s %s` Use is %q — a PK-or-URN positional must be spelled <ref>", group, leaf, cmd.Use)
			}
			if !strings.Contains(cmd.Use, "<ref>") {
				t.Errorf("`%s %s` Use is %q — expected a <ref> positional", group, leaf, cmd.Use)
			}
		}
	}

	// agentic-usage.md is the contract a shelling agent reads; it must not
	// still advertise the ID-only form for these.
	doc := agentic.Doc()
	for _, banned := range []string{"agent update <id>", "agent rm <id>"} {
		if strings.Contains(doc, banned) {
			t.Errorf("agentic-usage.md still advertises %q — #789 made it accept a URN", banned)
		}
	}
}

// TestTeamSessionVocabularyIsQualified pins the worker-session / chat-session
// distinction in the surfaces a confused human actually reads (#467,
// hadron-server#1034).
//
// "Session" names two independent things: the WORKER SESSION is the Hadron
// binding that holds the worker; the CHAT SESSION is the conversation the human
// is in. Ending the second does not end the first, and the failure is silent —
// someone archives their window, assumes the worker is free, and the next
// driver meets a takeover prompt they cannot interpret. This App has already
// lost a worker to it for 19 hours.
//
// Prose is the one part of a command that can rot without anything going red,
// so this is a guard rather than a nicety: the distinction has to survive every
// future edit to these help texts.
func TestTeamSessionVocabularyIsQualified(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)

	find := func(path ...string) *cobra.Command {
		t.Helper()
		cur := root
		for _, name := range path {
			var next *cobra.Command
			for _, c := range cur.Commands() {
				if c.Name() == name {
					next = c
					break
				}
			}
			if next == nil {
				t.Fatalf("command %v not found", path)
			}
			cur = next
		}
		return cur
	}

	// Both surfaces must state that the two are different AND that closing one
	// does not end the other — the second half is the load-bearing one.
	for _, tc := range []struct {
		path  []string
		wants []string
	}{
		{[]string{"team", "session"}, []string{"worker session", "chat session", "does not release the worker"}},
		{[]string{"team", "session", "end"}, []string{"WORKER SESSION", "CHAT SESSION", "does not do this"}},
	} {
		cmd := find(tc.path...)
		help := cmd.Long + " " + cmd.Short
		for _, want := range tc.wants {
			if !strings.Contains(help, want) {
				t.Errorf("`%s` help must carry %q — the distinction is the whole point of #467:\n%s",
					strings.Join(tc.path, " "), want, cmd.Long)
			}
		}
	}

	// Copy rule 2 (hadron-server#1034): in this team "the chat" is the TEAM
	// chat, so a bare "the chat" in team help is ambiguous with a chat session.
	// Checked across the whole team tree rather than the files touched here, so
	// a future command cannot reintroduce it.
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, phrase := range []string{"close the chat", "end the chat", "closing the chat."} {
			if strings.Contains(strings.ToLower(c.Long), phrase) {
				t.Errorf("`%s` help says %q — always qualify: \"team chat\" or \"chat session\"",
					c.CommandPath(), phrase)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(find("team"))
}
