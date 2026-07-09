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
