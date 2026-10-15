package cmd

// cli#889 — `team roster --help` names all eight derived states in the
// SERVER's precedence (roster.ts order), and the #12143 CLI half folded in:
// `team host issue --help` and agentic-usage say worker.start and worker.end
// are issuable by App managers with only worker.deliver not yet issued, and
// the examples use full owner/repo repo keys and <repo>/<label> worktree
// tokens.

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
)

func cmdHelpOut(t *testing.T, args ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v --help: %v", args, err)
	}
	return out.String()
}

func TestTeamRosterHelpStatesInServerPrecedence(t *testing.T) {
	help := cmdHelpOut(t, "team", "roster")
	// roster.ts: most actionable first — and WORKING is one of the eight.
	want := "YOUR_MOVE, UNAVAILABLE, WAITING, WORKING, ASSIGNED_NOT_STARTED, AVAILABLE, NO_FAMILY, NOT_LAUNCHABLE"
	if !strings.Contains(help, want) {
		t.Fatalf("roster help must name all eight states in the server's precedence:\n%s", help)
	}
	for _, stale := range []string{
		"ASSIGNED_NOT_STARTED, AVAILABLE, NOT_LAUNCHABLE",
		"NO_FAMILY, UNAVAILABLE, WAITING, YOUR_MOVE…",
	} {
		if strings.Contains(help, stale) {
			t.Fatalf("roster help carries the stale precedence %q", stale)
		}
	}
}

func TestTeamHostIssueHelpNamesTheIssuerArms(t *testing.T) {
	help := cmdHelpOut(t, "team", "host", "issue")
	if !strings.Contains(help, "worker.start and worker.end are issued by whoever manages the App") {
		t.Fatalf("host issue help must say start and end are issuable by App managers (#1971):\n%s", help)
	}
	if !strings.Contains(help, "worker.deliver is not issued") {
		t.Fatalf("host issue help must name worker.deliver as the only not-yet-issued:\n%s", help)
	}
	if strings.Contains(help, "worker.start, worker.end and worker.deliver are in the set but not issued") {
		t.Fatalf("host issue help carries the pre-#1971 wording")
	}
}

func TestTeamHostIssueExamplesUseFullTokens(t *testing.T) {
	help := cmdHelpOut(t, "team", "host", "issue")
	// A repo key is a full owner/repo; a worktree token is a full <repo>/<label>.
	if !strings.Contains(help, "--arg repo=hadron-memory/hadron-cli") {
		t.Fatalf("the worktree.create example must use a full owner/repo repo key:\n%s", help)
	}
	if !strings.Contains(help, "--arg worktree=hadron-memory/hadron-cli/jonas-3") {
		t.Fatalf("the worktree.remove example must use a full <repo>/<label> worktree token:\n%s", help)
	}
}

func TestAgenticUsageHostIssueArms(t *testing.T) {
	doc := agentic.Doc()
	if !strings.Contains(doc, "worker.start|worker.end issued by App managers (worker.deliver exit 2 HOST_OPERATION_NOT_ISSUABLE") {
		t.Fatalf("agentic-usage must carry the #1971 issuer arms on the host issue line")
	}
}
