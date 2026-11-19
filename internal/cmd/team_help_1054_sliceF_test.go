package cmd

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func sliceFFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	f, _ := testFactory(t)
	return f
}

// #1054 slice F (AT0.8605): the revised pages keep working examples and say
// the plain facts the evaluator found missing. Verification mode for the
// examples: parsed against the real command tree (command path and every flag
// must exist), never run, so no host, App or memory is touched.
func TestTeamSliceFHelpExamplesParseAndFactsHold(t *testing.T) {
	quoted := regexp.MustCompile(`"[^"]*"`)
	angle := regexp.MustCompile(`<[^>]*>`)
	pages := []struct {
		path  []string
		facts []string
	}{
		{[]string{"team", "stop"}, []string{"Pick the mildest level", `"hadron team pause"`}},
		{[]string{"team", "stop", "set"}, []string{"hadron team host list", "STOP_SIGNAL_UNAVAILABLE (exit 5, nothing set)", "only a halt, and only on that host"}},
		{[]string{"team", "delivery-settings"}, []string{"A dark alarm is the alert", "hadron ticker update <ticker-id> --cadence-seconds", "Deprecated, kept for compatibility"}},
		{[]string{"team", "delivery-settings", "set"}, []string{"Needs server 2026-11-04 or later."}},
		{[]string{"team", "wake-settings"}, []string{"A wake is the nudge", "Which one to use:"}},
		{[]string{"team", "wake-settings", "set"}, []string{"a time in the future", "<future RFC 3339 time>"}},
		{[]string{"team", "wakes", "due"}, []string{"is a\nspending read", "Use --preview to"}},
		{[]string{"team", "pause"}, []string{"Regular wakes are the nudges", "It asks no confirmation."}},
		{[]string{"team", "resume"}, []string{"Regular wakes are the nudges"}},
		{[]string{"team", "push"}, []string{"<worker> is the recipient's name, URN or id", "total wait before sending 120s", "Exit codes specific to push", "Orca"}},
	}
	for _, p := range pages {
		t.Run(strings.Join(p.path, " "), func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, p.path...), "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := out.String()
			for _, w := range p.facts {
				if strings.Contains(w, "\n") {
					w = strings.Join(strings.Fields(w), " ")
					if !strings.Contains(strings.Join(strings.Fields(help), " "), w) {
						t.Errorf("help lacks %q", w)
					}
					continue
				}
				if !strings.Contains(help, w) && !strings.Contains(strings.Join(strings.Fields(help), " "), w) {
					t.Errorf("help lacks %q", w)
				}
			}
			if strings.Contains(help, "Needs server 4bac6127") || strings.Contains(help, "2026-11-01T00:00:00Z") || strings.Contains(help, "wait120s") || strings.Contains(help, "Source merge does not migrate") {
				t.Error("help still carries a retired phrase")
			}
			// Parse every example against the real tree.
			cmd, _, err := NewRootCmd(sliceFFactory(t)).Find(p.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(cmd.Example, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "hadron ") {
					continue
				}
				line = quoted.ReplaceAllString(line, "x")
				line = angle.ReplaceAllString(line, "x")
				args := strings.Fields(strings.TrimPrefix(line, "hadron "))
				root3 := NewRootCmd(sliceFFactory(t))
				c, rest, err := root3.Find(args)
				if err != nil {
					t.Errorf("example %q: %v", line, err)
					continue
				}
				if err := c.ParseFlags(rest); err != nil {
					t.Errorf("example %q: %v", line, err)
					continue
				}
				// Find tolerates a mistyped subcommand under a group: it stops at the
				// group and leaves the typo among the leftover words, possibly after
				// flags ("team grant --json lsit"). Once the flags are parsed, a group
				// with any positional word left is a typo; a leaf takes its arguments.
				if c.HasSubCommands() && len(c.Flags().Args()) > 0 {
					t.Errorf("example %q: %q is not a subcommand of %q", line, c.Flags().Args()[0], c.CommandPath())
					continue
				}
				// The page describes draft waits of at least 30s, overlapping a 30s
				// stable interval, so an example giving the total wait less than a
				// minute (twice the 30s default) could end held on a draft: it is
				// rejected here; a minute or more is accepted.
				if d, err := c.Flags().GetDuration("wait-timeout"); err == nil && c.Flags().Changed("wait-timeout") && d < time.Minute {
					t.Errorf("example %q: --wait-timeout %s is shorter than the draft waits the page describes", line, d)
				}
			}
		})
	}
}
