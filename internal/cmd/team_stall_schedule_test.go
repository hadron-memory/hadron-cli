package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #864 over server #1706/#1707 and #1863 (cor:agt:050:14–:17, cor:agt:070:01):
// read and set an App's stall schedule. Wire semantics (the SDL's own words):
// an OMITTED field keeps its value; an explicit null returns it to the
// platform default.

const stallScheduleJSON = `{"appId":"capp100000000000000000000",
	"assignmentMinutes":30,"notStartedMinutes":60,"noProgressMinutes":120,
	"ownerReactionMinutes":240,"hostLapseCheckIns":5,
	"sessionEndGraceMinutes":10,"hostReportTimeoutMinutes":60,"updatedAt":"2026-10-13T20:00:00Z"}`

func stallResponses() map[string]string {
	r := taskResponses()
	r["TeamStallSchedule"] = `{"data":{"agentTaskStallSchedule":` + stallScheduleJSON + `}}`
	r["TeamStallScheduleSet"] = `{"data":{"setAgentTaskStallSchedule":` + stallScheduleJSON + `}}`
	return r
}

func stallSetInput(t *testing.T, calls *[]attnCall) map[string]any {
	t.Helper()
	for _, c := range *calls {
		if c.Op == "TeamStallScheduleSet" {
			if c.Vars["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v", c.Vars["appRef"])
			}
			in, ok := c.Vars["input"].(map[string]any)
			if !ok {
				t.Fatalf("input %#v", c.Vars["input"])
			}
			return in
		}
	}
	t.Fatal("no TeamStallScheduleSet call")
	return nil
}

func TestTeamStallScheduleGetRenders(t *testing.T) {
	calls, out, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "get")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "TeamStallSchedule" {
			if c.Vars["appRef"] != "capp100000000000000000000" {
				t.Fatalf("appRef %v", c.Vars["appRef"])
			}
			if c.Session != "" {
				t.Fatalf("read attributed: %q", c.Session)
			}
		}
	}
	for _, want := range []string{"app: ", "assignment", "30 min", "not started", "60 min",
		"no progress", "120 min", "owner reaction", "240 min", "host lapse", "5 check-ins", "2026-10-13T20:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestTeamStallScheduleGetJSON(t *testing.T) {
	_, out, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := map[string]any{"appId": "capp100000000000000000000", "assignmentMinutes": 30.0,
		"notStartedMinutes": 60.0, "noProgressMinutes": 120.0, "ownerReactionMinutes": 240.0,
		"hostLapseCheckIns": 5.0, "sessionEndGraceMinutes": 10.0, "hostReportTimeoutMinutes": 60.0,
		"updatedAt": "2026-10-13T20:00:00Z"}
	if len(got) != len(want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v\n%s", k, got[k], v, out)
		}
	}
}

// An omitted flag keeps the value: only the flags passed reach the input.
func TestTeamStallScheduleSetSendsOnlyPassedFields(t *testing.T) {
	calls, out, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "set",
		"--host-lapse-check-ins", "5", "--no-progress-minutes", "120")
	if err != nil {
		t.Fatal(err)
	}
	in := stallSetInput(t, calls)
	if len(in) != 2 || in["hostLapseCheckIns"] != 5.0 || in["noProgressMinutes"] != 120.0 {
		t.Fatalf("input %v, want exactly hostLapseCheckIns=5 and noProgressMinutes=120", in)
	}
	if !strings.Contains(out, "✓") || !strings.Contains(out, "5 check-ins") {
		t.Fatalf("receipt %q", out)
	}
}

// Every field is reachable, each under its own flag.
func TestTeamStallScheduleSetEveryField(t *testing.T) {
	calls, _, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "set",
		"--assignment-minutes", "1", "--not-started-minutes", "2", "--no-progress-minutes", "3",
		"--owner-reaction-minutes", "10080", "--host-lapse-check-ins", "10")
	if err != nil {
		t.Fatal(err)
	}
	in := stallSetInput(t, calls)
	want := map[string]any{"assignmentMinutes": 1.0, "notStartedMinutes": 2.0, "noProgressMinutes": 3.0,
		"ownerReactionMinutes": 10080.0, "hostLapseCheckIns": 10.0}
	if len(in) != len(want) {
		t.Fatalf("input %v, want %v", in, want)
	}
	for k, v := range want {
		if in[k] != v {
			t.Fatalf("%s = %v, want %v", k, in[k], v)
		}
	}
}

// `default` sends an explicit null — the server's "return to the platform
// default" — and must not be confused with omitting the flag.
func TestTeamStallScheduleSetDefaultSendsNull(t *testing.T) {
	calls, _, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "set",
		"--host-lapse-check-ins", "default", "--assignment-minutes", "DEFAULT")
	if err != nil {
		t.Fatal(err)
	}
	in := stallSetInput(t, calls)
	if len(in) != 2 {
		t.Fatalf("input %v, want exactly two keys", in)
	}
	for _, k := range []string{"hostLapseCheckIns", "assignmentMinutes"} {
		v, present := in[k]
		if !present || v != nil {
			t.Fatalf("%s: present=%v value=%v, want an explicit null", k, present, v)
		}
	}
}

// The request bytes as sent, not a re-marshal of their decode: the explicit
// null must be on the wire as `null`, and an unset field must be absent (not
// null). The input is a bound struct, so its keys go out in field order.
func TestTeamStallScheduleSetWireShape(t *testing.T) {
	calls, _, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "set",
		"--owner-reaction-minutes", "default", "--not-started-minutes", "45")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op != "TeamStallScheduleSet" {
			continue
		}
		var vars map[string]json.RawMessage
		if err := json.Unmarshal(c.RawVars, &vars); err != nil {
			t.Fatalf("raw variables %s: %v", c.RawVars, err)
		}
		if got := string(vars["input"]); got != `{"notStartedMinutes":45,"ownerReactionMinutes":null}` {
			t.Fatalf("wire input %s", got)
		}
		return
	}
	t.Fatal("no TeamStallScheduleSet call")
}

// set renders through the same DTO as get; its --json is pinned on its own.
func TestTeamStallScheduleSetJSON(t *testing.T) {
	_, out, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "set", "--host-lapse-check-ins", "5", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := map[string]any{"appId": "capp100000000000000000000", "assignmentMinutes": 30.0,
		"notStartedMinutes": 60.0, "noProgressMinutes": 120.0, "ownerReactionMinutes": 240.0,
		"hostLapseCheckIns": 5.0, "sessionEndGraceMinutes": 10.0, "hostReportTimeoutMinutes": 60.0,
		"updatedAt": "2026-10-13T20:00:00Z"}
	if len(got) != len(want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v\n%s", k, got[k], v, out)
		}
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("--json output carries the human receipt:\n%s", out)
	}
}

func TestTeamStallScheduleSetLocalRefusals(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"no flags", nil, "name at least one"},
		{"non-integer", []string{"--host-lapse-check-ins", "abc"}, "--host-lapse-check-ins takes a whole number or default"},
		{"fraction", []string{"--assignment-minutes", "1.5"}, "--assignment-minutes takes a whole number or default"},
		{"empty", []string{"--no-progress-minutes", ""}, "--no-progress-minutes takes a whole number or default"},
		{"blank", []string{"--not-started-minutes", "  "}, "--not-started-minutes takes a whole number or default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := append([]string{"team", "stall-schedule", "set"}, tc.argv...)
			calls, _, err := runTeamTask(t, stallResponses(), argv...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a usage refusal naming %q, got %v (exit %d)", tc.want, err, exitCodeFor(err))
			}
			for _, c := range *calls {
				if c.Op == "TeamStallScheduleSet" {
					t.Fatal("a local refusal must not reach the server")
				}
			}
		})
	}
}

// Range is the server's rule (1–10080 minutes, 2–10 check-ins); the CLI
// forwards the value and surfaces the typed refusal.
func TestTeamStallScheduleSetServerRefusals(t *testing.T) {
	cases := []struct {
		name  string
		argv  []string
		op    string
		code  string
		want  int
		msgIn string
	}{
		{"lapse out of range", []string{"set", "--host-lapse-check-ins", "11"}, "TeamStallScheduleSet", "BAD_USER_INPUT", exitcode.Usage,
			"hostLapseCheckIns is a whole number of check-ins from 2 to 10, or null for the default (3)"},
		{"window out of range", []string{"set", "--assignment-minutes", "0"}, "TeamStallScheduleSet", "BAD_USER_INPUT", exitcode.Usage,
			"assignmentMinutes is a whole number of minutes from 1 to 10080 (a week), or null for the default"},
		{"not the manager", []string{"set", "--host-lapse-check-ins", "4"}, "TeamStallScheduleSet", "APP_MANAGER_REQUIRED", exitcode.Forbidden,
			"Only whoever manages the App sets its stall windows"},
		{"set unknown app", []string{"set", "--host-lapse-check-ins", "4"}, "TeamStallScheduleSet", "APP_NOT_FOUND", exitcode.NotFound, "App not found"},
		{"get unknown app", []string{"get"}, "TeamStallSchedule", "APP_NOT_FOUND", exitcode.NotFound, "App not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := stallResponses()
			r[tc.op] = `{"errors":[{"message":"` + tc.msgIn + `.","extensions":{"code":"` + tc.code + `"}}]}`
			_, out, err := runTeamTask(t, r, append([]string{"team", "stall-schedule"}, tc.argv...)...)
			if err == nil {
				t.Fatalf("the %s refusal must surface", tc.code)
			}
			if got := exitCodeFor(err); got != tc.want {
				t.Fatalf("exit code = %d, want %d (%s)", got, tc.want, tc.code)
			}
			if !strings.Contains(err.Error(), tc.msgIn) {
				t.Fatalf("the server's message should surface, got %v", err)
			}
			if strings.Contains(out, "✓") {
				t.Fatalf("no success receipt may precede a refusal, got %q", out)
			}
		})
	}
}

// Setting the schedule is the App manager's call, and the server's
// actorManagesApp answers no for any worker actor. Attributing the write to a
// bound worktree's worker would refuse every manager working from one — so
// this write is the person's, never the session's.
func TestTeamStallScheduleSetNotAttributedToWorker(t *testing.T) {
	calls, _, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "set", "--host-lapse-check-ins", "4")
	if err != nil {
		t.Fatal(err)
	}
	// The witness is required: a run that never sent the mutation must not
	// pass as "no session was sent".
	seen := false
	for _, c := range *calls {
		if c.Op != "TeamStallScheduleSet" {
			continue
		}
		seen = true
		if c.Session != "" {
			t.Fatalf("stall-schedule set carried worker session %q; the server refuses a worker actor APP_MANAGER_REQUIRED", c.Session)
		}
	}
	if !seen {
		t.Fatal("no TeamStallScheduleSet call observed")
	}
}
