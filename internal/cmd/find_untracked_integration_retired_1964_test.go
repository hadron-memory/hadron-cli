package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1964: the server's task integrations are retired (the server no longer
// serves untrackedTaskItems, claimUntrackedTaskItems or ReadyFilter), so every
// --integration call failed with a raw schema error. find-untracked now refuses
// the integration path itself: exit 2, nothing sent (not even the binding's
// App read), and the refusal names what replaced it, the team's intake adapter.
func TestFindUntrackedIntegrationIsRefusedAsRetired_1964(t *testing.T) {
	for name, argv := range map[string][]string{
		"list":                {"--integration", "github"},
		"list, filtered":      {"--integration", "github", "--team", "Marco", "--ready", "only", "--repo", "acme/app"},
		"list, no team":       {"--integration", "github", "--no-team"},
		"list, paged":         {"--integration", "github", "--skip", "20", "--json"},
		"claim":               {"--integration", "github", "--assign-to-me"},
		"claim, with parties": {"--integration", "github", "--assign-to-me", "--assignee", "worker:Iris", "--owner", "user:holger"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(adapterEnvForTest, "") // empty reads as unset: selectAdapter trims it
			calls, out, err := runTeamTask(t, map[string]string{}, append([]string{"team", "task", "find-untracked"}, argv...)...)
			if err == nil {
				t.Fatalf("--integration must be refused, got success:\n%s", out)
			}
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit %d, want %d (usage): %v", code, exitcode.Usage, err)
			}
			msg := err.Error()
			for _, want := range []string{"--integration", "retired", "--adapter", adapterEnvForTest, "nothing was sent"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal must name %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, "ReadyFilter") || strings.Contains(msg, "GRAPHQL") {
				t.Errorf("no schema error may reach the user: %s", msg)
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing may be sent, got %d calls: %+v", len(*calls), *calls)
			}
		})
	}
}

// With no source named and no $HADRON_INTAKE_ADAPTER, the usage error names
// only the adapter: it no longer offers the retired --integration.
func TestFindUntrackedNoSourceNamesOnlyTheAdapter_1964(t *testing.T) {
	t.Setenv(adapterEnvForTest, "") // empty reads as unset: selectAdapter trims it
	calls, _, err := runTeamTask(t, map[string]string{}, "team", "task", "find-untracked")
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit %d, want %d: %v", code, exitcode.Usage, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "--adapter") || strings.Contains(msg, "--integration <name>") {
		t.Fatalf("the no-source error must name only the adapter: %s", msg)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may be sent, got %d calls", len(*calls))
	}
}

// The rest of the integration path's flags are gone, and naming one, alone or
// beside a real --adapter, is refused by name as retired, with the way out,
// rather than cobra's bare "unknown flag": exit 2, nothing sent, the adapter
// not run. A flag that never existed keeps cobra's own error.
func TestFindUntrackedRetiredFlagsAreRefusedByName_1964(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[]}'`, "")
	for _, tc := range []struct {
		flag string
		argv []string
	}{
		{"--team", []string{"--team", "Marco"}},
		{"--team", []string{"--team=Marco"}},
		{"--no-team", []string{"--no-team"}},
		{"--ready", []string{"--ready", "exclude"}},
		{"--repo", []string{"--repo", "acme/app"}},
		{"--skip", []string{"--skip", "20"}},
		{"--assign-to-me", []string{"--assign-to-me"}},
		{"--ready", []string{"--adapter", path, "--ready", "only"}},
		{"--assign-to-me", []string{"--adapter", path, "--track", "--assign-to-me"}},
		{"--integration", []string{"--adapter", path, "--integration", "github"}},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			t.Setenv(adapterEnvForTest, "") // empty reads as unset: selectAdapter trims it
			calls, out, err := runTeamTask(t, map[string]string{}, append([]string{"team", "task", "find-untracked"}, tc.argv...)...)
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit %d, want %d: %v", code, exitcode.Usage, err)
			}
			msg := err.Error()
			for _, want := range []string{tc.flag + " is retired", "--adapter", adapterEnvForTest, "nothing was sent"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal must name %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, "unknown flag") {
				t.Errorf("a retired flag is named as retired, not unknown: %s", msg)
			}
			if len(*calls) != 0 || out != "" {
				t.Fatalf("nothing may be sent or printed: %v %q", *calls, out)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 {
				t.Fatalf("the adapter must not be run: %s", b)
			}
		})
	}
	_, _, err := runTeamTask(t, map[string]string{}, "team", "task", "find-untracked", "--teamx", "a")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --teamx") {
		t.Fatalf("a flag that never existed keeps cobra's error: %v", err)
	}
}

// find-untracked's own flag-error handler must not shadow the root's: every
// flag error that is not a retired flag still exits 2 (usage), as on every
// other command, in text and --json. A malformed value (--limit abc) is the
// case only the root's wrapping gets right; an unknown flag (--teamx) must
// keep cobra's message AND the usage exit.
func TestFindUntrackedOtherFlagErrorsKeepTheUsageExit_1964(t *testing.T) {
	for _, argv := range [][]string{
		{"--limit", "abc"},
		{"--adapter", "/nonexistent/adapter", "--limit", "abc"},
		{"--json", "--limit", "abc"},
		{"--teamx", "a"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			t.Setenv(adapterEnvForTest, "") // empty reads as unset: selectAdapter trims it
			calls, _, err := runTeamTask(t, map[string]string{}, append([]string{"team", "task", "find-untracked"}, argv...)...)
			if err == nil {
				t.Fatal("a malformed flag must be refused")
			}
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit %d, want %d (usage, as the root's flag-error handler gives): %v", code, exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "retired") {
				t.Fatalf("only a retired flag is named as retired: %v", err)
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing may be sent: %v", *calls)
			}
		})
	}
}

// With $HADRON_INTAKE_ADAPTER naming a real adapter, a retired flag is still
// refused by name and the adapter is never run; --no-team=false (which once
// meant "the same as omitting it") is refused too, since the flag is gone.
func TestFindUntrackedRetiredFlagsWithTheAdapterVariableSet_1964(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[]}'`, "")
	for _, tc := range []struct {
		flag string
		argv []string
	}{
		{"--ready", []string{"--ready", "only"}},
		{"--no-team", []string{"--no-team=false"}},
		{"--integration", []string{"--integration", "github"}},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			t.Setenv(adapterEnvForTest, path)
			calls, out, err := runTeamTask(t, map[string]string{}, append([]string{"team", "task", "find-untracked"}, tc.argv...)...)
			if code := exitCodeFor(err); code != exitcode.Usage || err == nil || !strings.Contains(err.Error(), tc.flag+" is retired") {
				t.Fatalf("exit %d, want %d naming %s as retired: %v", code, exitcode.Usage, tc.flag, err)
			}
			if len(*calls) != 0 || out != "" {
				t.Fatalf("nothing may be sent or printed: %v %q", *calls, out)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 {
				t.Fatalf("the adapter must not be run: %s", b)
			}
		})
	}
}
