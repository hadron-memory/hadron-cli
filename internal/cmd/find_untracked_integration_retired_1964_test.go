package cmd

import (
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
			t.Setenv("HADRON_INTAKE_ADAPTER", "")
			calls, out, err := runTeamTask(t, map[string]string{}, append([]string{"team", "task", "find-untracked"}, argv...)...)
			if err == nil {
				t.Fatalf("--integration must be refused, got success:\n%s", out)
			}
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Fatalf("exit %d, want %d (usage): %v", code, exitcode.Usage, err)
			}
			msg := err.Error()
			for _, want := range []string{"--integration", "retired", "--adapter", "HADRON_INTAKE_ADAPTER", "nothing was sent"} {
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
	t.Setenv("HADRON_INTAKE_ADAPTER", "")
	calls, _, err := runTeamTask(t, map[string]string{}, "team", "task", "find-untracked")
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Fatalf("exit %d, want %d: %v", code, exitcode.Usage, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "--adapter") || strings.Contains(msg, "--integration <name>") {
		t.Fatalf("the no-source error must name only the adapter: %s", msg)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may be sent, got %d calls", len(*calls))
	}
}
