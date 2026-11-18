package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// alarmStallCase2451 looks one case up by name, so a test does not couple to
// the slice's order (#2505 F5e: [0]/[1] silently meant ack/set).
func alarmStallCase2451(t *testing.T, name string) taskInstructionCase2422 {
	t.Helper()
	for _, c := range alarmStallCases2451() {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no alarm/stall case named %q", name)
	return taskInstructionCase2422{}
}

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

// cli#2537 F4 (from #2505): warnings can arrive on a plain (non-instructed)
// ack too — the server files task warnings for acknowledgeAlarm regardless of
// instruction (taskWarningsPlugin, server #2198; the gate's read of
// resolvers.agentTaskAlarms.ts:263: alarm.ack.outside-chain for an App
// manager outside the chain). The plain path had no sink, so they were
// dropped. Text mode now mirrors the instructed path (stderr); plain --json
// keeps its byte-identical shape — the warnings key is the instructed
// shape's alone (#2505 F5a).
func TestAlarmAckPlainPathSurfacesWarnings2537(t *testing.T) {
	c := alarmStallCase2451(t, "ack")
	r := stallResponses()
	warning := `{"path":"acknowledgeAlarm","code":"alarm.ack.outside-chain","text":"Outside chain.","register":null}`
	r[c.plainOp] = strings.TrimSuffix(c.response, "}") + `,"extensions":{"warnings":[` + warning + `]}}`
	args := append([]string{}, c.args...)
	out, stderr, err := runTeamTaskStreams(t, r, args...)
	if err != nil || !strings.Contains(out, "acknowledged alarm") || !strings.Contains(stderr, "warning: [alarm.ack.outside-chain] Outside chain.") {
		t.Fatalf("plain ack must surface server warnings on stderr like the instructed path: out=%q stderr=%q err=%v", out, stderr, err)
	}
	_, out, err = runTeamTask(t, r, append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, out)
	}
	if _, ok := dto["warnings"]; ok {
		t.Fatalf("plain --json must not carry a warnings key (byte identity): %s", out)
	}
}
