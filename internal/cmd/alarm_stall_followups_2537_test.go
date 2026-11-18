package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2537 (from #2529's review): a success answer whose selected read is
// null (no errors) is a malformed answer — a command error naming what is
// missing, never a panic. Same class as the set command's null guard
// (#2505 F2) and the events page guard (cli#2477): genqlient types the
// selected field as a pointer, and `get` read it without checking.
func TestTeamStallScheduleGetNullResultIsCommandError2537(t *testing.T) {
	r := stallResponses()
	r["TeamStallSchedule"] = `{"data":{"agentTaskStallSchedule":null}}`
	_, out, err := runTeamTask(t, r, "team", "stall-schedule", "get")
	if err == nil {
		t.Fatalf("a null schedule must fail, not succeed; stdout=%q", out)
	}
	if exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "no stall schedule") {
		t.Fatalf("want exit %d naming the missing schedule, got %d: %v", exitcode.Error, exitCodeFor(err), err)
	}
	if out != "" {
		t.Fatalf("nothing renders on a failed read; stdout=%q", out)
	}
}
