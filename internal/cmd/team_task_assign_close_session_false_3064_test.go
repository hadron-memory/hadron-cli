package cmd

// hadron-cli#3064 — `task assign` / `task reassign` refuse a SUPPLIED
// --close-session=false / --no-close-session=false the way `task update` does
// (#1137): a plain bool cannot tell the =false spelling from an absent flag, so
// it would silently keep the stored choice. The refusal is a usage error
// (exit 2) raised before any request, and names the spelling that does say no.

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestTaskAssignRefusesFalseCloseSession3064(t *testing.T) {
	for _, verb := range []string{"assign", "reassign"} {
		for _, tc := range []struct {
			flag string
			want string
		}{
			{"--close-session=false", "--no-close-session"},
			{"--no-close-session=false", "--close-session"},
		} {
			t.Run(verb+" "+tc.flag, func(t *testing.T) {
				calls, _, err := runTeamTask(t, closeSessionResponses(true),
					"team", "task", verb, "AT1.2", "--assignee", "worker:Iris", tc.flag)
				if err == nil || exitCodeFor(err) != exitcode.Usage {
					t.Fatalf("want a usage error (exit 2), got %v", err)
				}
				if !strings.Contains(err.Error(), tc.flag) || !strings.Contains(err.Error(), "changes nothing") || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("refusal must name the flag and the spelling that works (%s): %v", tc.want, err)
				}
				for _, c := range *calls {
					if strings.HasPrefix(c.Op, "AgentTaskAssign") || strings.HasPrefix(c.Op, "AgentTaskReassign") {
						t.Fatalf("a request was sent: %s", c.Op)
					}
				}
			})
		}
	}
}

// The three spellings that already worked keep working: nothing, --close-session
// and --no-close-session (and the explicit =true form).
func TestTaskAssignKeepsTheWorkingCloseSessionSpellings3064(t *testing.T) {
	for _, verb := range []string{"assign", "reassign"} {
		for _, flags := range [][]string{nil, {"--close-session"}, {"--close-session=true"}, {"--no-close-session"}, {"--no-close-session=true"}} {
			argv := append([]string{"team", "task", verb, "AT1.2", "--assignee", "worker:Iris"}, flags...)
			t.Run(verb+" "+strings.Join(flags, " "), func(t *testing.T) {
				if _, _, err := runTeamTask(t, closeSessionResponses(true), argv...); err != nil {
					t.Fatalf("%v: %v", flags, err)
				}
			})
		}
	}
}
