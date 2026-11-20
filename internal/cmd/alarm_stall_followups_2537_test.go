package cmd

import (
	"encoding/json"
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
	r[c.plainOp] = withWarning2552(t, c.response, warning)
	args := append([]string{}, c.args...)
	out, stderr, err := runTeamTaskStreams(t, r, args...)
	if err != nil || !strings.Contains(out, "acknowledged alarm") || !strings.Contains(stderr, "warning: [alarm.ack.outside-chain] Outside chain.") {
		t.Fatalf("plain ack must surface server warnings on stderr like the instructed path: out=%q stderr=%q err=%v", out, stderr, err)
	}
	_, out, err = runTeamTask(t, r, append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	// Byte identity, pinned on the whole output: the plain --json a warning
	// rides on equals the one without it (#2552 item 6).
	r[c.plainOp] = c.response
	_, bare, err := runTeamTask(t, r, append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	if out != bare {
		t.Fatalf("plain --json must be byte-identical with and without a warning:\nwith:    %s\nwithout: %s", out, bare)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, out)
	}
	if _, ok := dto["warnings"]; ok {
		t.Fatalf("plain --json must not carry a warnings key (byte identity): %s", out)
	}
}

// cli#2537 (#2505 F5c, test half): a server older than ba077810 rejects the
// instruction argument itself — schema validation, not a semantic refusal.
// The CLI passes that refusal through and never retries the call plain; the
// shared help's sentence ("an older server's refusal is passed through,
// never retried without it") is pinned in the instructionHelpFor unit test.
func TestAlarmStallInstructionOldServerRejectsArgument2537(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		t.Run(c.name, func(t *testing.T) {
			r := stallResponses()
			field := map[string]string{"ack": "acknowledgeAlarm", "set": "setAgentTaskStallSchedule"}[c.name]
			if field == "" {
				t.Fatalf("no mutation field recorded for case %q", c.name)
			}
			r[c.instrOp] = `{"errors":[{"message":"Unknown argument \"instruction\" on field \"Mutation.` + field + `\"."}]}`
			calls, out, err := runTeamTask(t, r, append(append([]string{}, c.args...), "--instruction", "live-chat:opaque")...)
			if err == nil || !strings.Contains(err.Error(), "Unknown argument") || out != "" {
				t.Fatalf("err=%v out=%q", err, out)
			}
			// Passed through MapErrorKeepingCode: a schema-validation error
			// carries no GraphQL error code, so it keeps the generic failure
			// exit (1), not a usage or not-found one (#2552 item 7).
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("old-server rejection exit = %d, want %d: %v", got, exitcode.Error, err)
			}
			oneCall2422(t, calls, c.instrOp)
			for _, call := range *calls {
				if call.Op == c.plainOp {
					t.Fatal("an old server's rejection must not retry plain")
				}
			}
		})
	}
}
