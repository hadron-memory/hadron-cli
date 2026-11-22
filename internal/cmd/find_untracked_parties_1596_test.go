package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1596 (hadron-server#3132 S4, cor:agt:120:03): --assignee/--owner on
// the find-untracked claim. The claim stays selected only by --assign-to-me;
// the party flags ride a SEPARATE document so the no-flag claim is the old
// document byte for byte (an older server still validates it).

const claimPartiesOp1596 = "ClaimUntrackedTaskItemsForParties"

func claimOK1596() map[string]string {
	ok := claimResult1524(claimedTaskJSON1524, true, "", "")
	return map[string]string{
		"ClaimUntrackedTaskItems": ok,
		claimPartiesOp1596:        ok,
		"UntrackedTaskItems":      untrackedPage1524("", 0, true, ""),
	}
}

// Row 2 + malformed refs: refused locally, exit 2, ZERO requests of any kind
// (not even the binding's App read), never a list turned into a write.
func TestFindUntrackedPartyFlagsRefusedLocally1596(t *testing.T) {
	for name, argv := range map[string][]string{
		"--assignee without --assign-to-me":    {"--assignee", "worker:Iris"},
		"--owner without --assign-to-me":       {"--owner", "user:holger"},
		"both without --assign-to-me":          {"--assignee", "worker:Iris", "--owner", "user:holger"},
		"--assignee empty":                     {"--assign-to-me", "--assignee", ""},
		"--owner empty":                        {"--assign-to-me", "--owner", ""},
		"--assignee without a kind":            {"--assign-to-me", "--assignee", "Iris"},
		"--owner with an unknown kind":         {"--assign-to-me", "--owner", "team:Marco"},
		"--assignee with an empty ref":         {"--assign-to-me", "--assignee", "worker:"},
		"valid --assignee, malformed --owner":  {"--assign-to-me", "--assignee", "worker:Iris", "--owner", "holger"},
		"empty --assignee without the switch":  {"--assignee", ""},
		"--owner=user:x with --skip, no claim": {"--owner", "user:x", "--skip", "2"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, _, err := runFind1524(t, claimOK1596(), append([]string{"--integration", "github"}, argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("the flags must exist: %v", err)
			}
			if len(*calls) != 0 {
				t.Errorf("zero requests expected, got %v", *calls)
			}
			if out != "" {
				t.Errorf("nothing on stdout: %q", out)
			}
		})
	}
}

// The help no longer says an App key is refused, and names the server as the
// judge of who may name parties.
func TestFindUntrackedPartyHelp1596(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "find-untracked", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	if strings.Contains(help, "TASK_CLAIM_PRINCIPAL_UNSUPPORTED") || strings.Contains(help, "an App key is refused") {
		t.Errorf("stale App-key refusal still in help:\n%s", help)
	}
	// cli#1964: the integration path's party paragraph is gone with the path;
	// the parties now ride the adapter's --track claim.
	for _, want := range []string{"--assignee", "--owner", "with --track: the claim's assignee", "with --track: the claim's owner"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// The refusal precedes even the App lookup a legacy binding (team memory, no
// App id) makes: zero requests, not one TeamMemoryApp read.
func TestFindUntrackedPartyRefusalBeforeAppLookup1596(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, claimOK1596())
	setTeamBindingServer(t, srv.URL)
	rewriteBinding1592(t, func(b map[string]any) {
		delete(b, "appId")
		b["teamMemory"] = "hrn:mem:acme.com:eng-team"
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "find-untracked", "--integration", "github", "--owner", "user:holger"})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
	}
	if len(*calls) != 0 {
		t.Errorf("zero requests expected, got %v", *calls)
	}
}
