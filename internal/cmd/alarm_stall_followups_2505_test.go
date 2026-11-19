package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2505 F1: the instruction help these two commands print names the server
// revision their instructed path needs (ba077810), not the shared text's
// original revision. The pin renders the real help, so a shared text that drops
// the original revision (a silent no-op replace) or a command that stops
// deriving it fails here.
func TestAlarmStallHelpNamesTheirMinimumServer2505(t *testing.T) {
	for _, args := range [][]string{{"team", "alarm", "ack"}, {"team", "stall-schedule", "set"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, args...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		help := out.String()
		if !strings.Contains(help, "ba077810") || strings.Contains(help, "17ef5905") {
			t.Errorf("%v: help must name ba077810 and not 17ef5905:\n%s", args, help)
		}
	}
}

// cli#2505 F2: a success response whose mutation result is null (no errors) is a
// malformed answer — exit 1, naming the field and that the outcome is unknown —
// on the plain and the instructed path of both commands, never a panic or a
// success. Same class as cli#2477.
func TestAlarmStallNullResultIsMalformedAnswer2505(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		for _, instructed := range []bool{false, true} {
			name := c.name + "/plain"
			args := append([]string{}, c.args...)
			if instructed {
				name = c.name + "/instructed"
				args = append(args, "--instruction", "team-chat-message:"+taskInstructionRef2422)
			}
			t.Run(name, func(t *testing.T) {
				r := stallResponses()
				// cli#2537 (Jonas/Jorge): the lookup must be loud — a renamed
				// case used to yield "" and build a nonsense response key.
				field := map[string]string{"ack": "acknowledgeAlarm", "set": "setAgentTaskStallSchedule"}[c.name]
				if field == "" {
					t.Fatalf("no mutation field mapped for case %q — update the map", c.name)
				}
				null := `{"data":{"` + field + `":null}}`
				r[c.plainOp] = null
				r[c.instrOp] = null
				_, out, err := runTeamTask(t, r, args...)
				if err == nil {
					t.Fatalf("a null result must fail; stdout=%q", out)
				}
				if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "outcome is unknown") {
					t.Fatalf("want exit %d naming the unknown outcome, got %d: %v", exitcode.Error, exitCodeFor(err), err)
				}
				// The error names the mutation field and says what to inspect
				// before retrying (the per-command hint, cli#2537), and
				// nothing renders first.
				hint := map[string]string{"ack": "team events --alarms", "set": "team stall-schedule get"}[c.name]
				if hint == "" {
					t.Fatalf("no inspection hint mapped for case %q — update the map", c.name)
				}
				if !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), hint) {
					t.Fatalf("error must name %s and the inspection hint %q: %v", field, hint, err)
				}
				if out != "" {
					t.Fatalf("nothing renders before the malformed answer; stdout=%q", out)
				}
			})
		}
	}
}
