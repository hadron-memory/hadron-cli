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
			flag  string
			extra []string // another flag given with it: the =false refusal wins over "contradict"
			want  string
		}{
			{"--close-session=false", nil, "--no-close-session"},
			{"--no-close-session=false", nil, "--close-session"},
			{"--close-session=false", []string{"--no-close-session"}, "--no-close-session"},
			{"--no-close-session=false", []string{"--close-session"}, "--close-session"},
		} {
			t.Run(verb+" "+tc.flag+" "+strings.Join(tc.extra, " "), func(t *testing.T) {
				argv := append([]string{"team", "task", verb, "AT1.2", "--assignee", "worker:Iris", tc.flag}, tc.extra...)
				calls, _, err := runTeamTask(t, closeSessionResponses(true), argv...)
				if err == nil || exitCodeFor(err) != exitcode.Usage {
					t.Fatalf("want a usage error (exit 2), got %v", err)
				}
				if !strings.Contains(err.Error(), tc.flag) || !strings.Contains(err.Error(), "changes nothing") || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("refusal must name the flag and the spelling that works (%s): %v", tc.want, err)
				}
				// Refused before any request at all, not only before the write.
				if len(*calls) != 0 {
					t.Fatalf("a request was sent: %v", *calls)
				}
			})
		}
	}
}

// The spellings that already worked keep working AND keep sending the same
// value: nothing sends no choice (the stored one is kept), --close-session
// sends true, --no-close-session sends false.
func TestTaskAssignKeepsTheWorkingCloseSessionSpellings3064(t *testing.T) {
	for _, verb := range []string{"assign", "reassign"} {
		for _, tc := range []struct {
			label string
			flags []string
			want  any // nil: the variable is omitted
		}{
			{"no flag", nil, nil},
			{"--close-session", []string{"--close-session"}, true},
			{"--close-session=true", []string{"--close-session=true"}, true},
			{"--no-close-session", []string{"--no-close-session"}, false},
			{"--no-close-session=true", []string{"--no-close-session=true"}, false},
		} {
			t.Run(verb+" "+tc.label, func(t *testing.T) {
				argv := append([]string{"team", "task", verb, "AT1.2", "--assignee", "worker:Iris"}, tc.flags...)
				calls, _, err := runTeamTask(t, closeSessionResponses(true), argv...)
				if err != nil {
					t.Fatalf("%v: %v", tc.flags, err)
				}
				sent := false
				for _, c := range *calls {
					if !strings.HasPrefix(c.Op, "AgentTaskAssign") && !strings.HasPrefix(c.Op, "AgentTaskReassign") {
						continue
					}
					sent = true
					got, present := c.Vars["closeSessionWhenDone"]
					if tc.want == nil && present && got != nil {
						t.Fatalf("closeSessionWhenDone should be omitted, got %v", got)
					}
					if tc.want != nil && got != tc.want {
						t.Fatalf("closeSessionWhenDone = %v, want %v", got, tc.want)
					}
				}
				if !sent {
					t.Fatal("no assign/reassign request was sent")
				}
			})
		}
	}
}

// The help says the =false spelling is refused, and no longer says it "reads as
// absent and keeps the value" (the retired wording that documented the silent
// acceptance). The inventory hash alone would not catch a revert.
func TestTaskAssignHelpSaysFalseIsRefused3064(t *testing.T) {
	for _, verb := range []string{"assign", "reassign"} {
		_, out, err := runTeamTask(t, taskResponses(), "team", "task", verb, "--help")
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		flat := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(flat, "--close-session=false` (or --no-close-session=false) is refused with exit 2") {
			t.Errorf("%s help does not say the =false spelling is refused:\n%s", verb, out)
		}
		if !strings.Contains(flat, "say no with --no-close-session") {
			t.Errorf("%s help does not name the spelling that works:\n%s", verb, out)
		}
		if strings.Contains(flat, "reads as absent") {
			t.Errorf("%s help still has the retired wording:\n%s", verb, out)
		}
	}
}
