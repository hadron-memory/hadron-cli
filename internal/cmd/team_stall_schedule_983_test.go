package cmd

// hadron-cli#983 — `team stall-schedule get|set` carry the two schedule fields
// the server already has: sessionEndGraceMinutes (cor:agt:050:10; 1–1440,
// default 10) and hostReportTimeoutMinutes (cor:agt:070:02; 5–1440, default
// 60). Rows are named for the CHECKED acceptance table (H/G/R/M/D/P). Unlike
// the windows, whose range stays the server's rule, each new field carries its
// own range client-side, so an invalid value — alone or in a mixed set — sends
// nothing.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func runStallSet(t *testing.T, argv ...string) (*[]attnCall, string, error) {
	t.Helper()
	return runTeamTask(t, stallResponses(), append([]string{"team", "stall-schedule", "set"}, argv...)...)
}

func assertNoSetCall(t *testing.T, calls *[]attnCall) {
	t.Helper()
	for _, c := range *calls {
		if c.Op == "TeamStallScheduleSet" {
			t.Fatalf("a local refusal must not reach the server: %v", c.Vars)
		}
	}
}

// H1–H3, G1: in-range values and `default` go on the wire as given.
func TestC983SetSendsBoundsAndDefault(t *testing.T) {
	cases := []struct {
		flag, value, key string
		want             any // float64 for a number, nil for an explicit null
	}{
		{"--host-report-timeout-minutes", "5", "hostReportTimeoutMinutes", float64(5)},
		{"--host-report-timeout-minutes", "1440", "hostReportTimeoutMinutes", float64(1440)},
		{"--host-report-timeout-minutes", "default", "hostReportTimeoutMinutes", nil},
		{"--session-end-grace-minutes", "1", "sessionEndGraceMinutes", float64(1)},
		{"--session-end-grace-minutes", "1440", "sessionEndGraceMinutes", float64(1440)},
		{"--session-end-grace-minutes", "default", "sessionEndGraceMinutes", nil},
	}
	for _, tc := range cases {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			calls, _, err := runStallSet(t, tc.flag, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			in := stallSetInput(t, calls)
			got, present := in[tc.key]
			if !present || got != tc.want {
				t.Fatalf("%s = %v (present %v), want %v; input %v", tc.key, got, present, tc.want, in)
			}
			if len(in) != 1 {
				t.Fatalf("only %s may be sent, got %v", tc.key, in)
			}
		})
	}
}

// H4, G2: a flag left out is a key left out — the server keeps the value.
func TestC983OmittedFlagsAreAbsentKeys(t *testing.T) {
	calls, _, err := runStallSet(t, "--assignment-minutes", "30")
	if err != nil {
		t.Fatal(err)
	}
	in := stallSetInput(t, calls)
	for _, k := range []string{"sessionEndGraceMinutes", "hostReportTimeoutMinutes"} {
		if _, present := in[k]; present {
			t.Fatalf("%s sent though its flag was omitted: %v", k, in)
		}
	}
}

// H5, G3, R1: out of range, non-integer and empty are refused before any call,
// with the field's own range in the message. R1 rows are values another
// field's range would accept (2 is a valid lapse count, 5000 a valid window).
func TestC983SetRefusesOutOfRangeBeforeAnyCall(t *testing.T) {
	cases := []struct {
		flag, value, want string
	}{
		{"--host-report-timeout-minutes", "4", "from 5 to 1440"},
		{"--host-report-timeout-minutes", "1441", "from 5 to 1440"},
		{"--host-report-timeout-minutes", "-5", "from 5 to 1440"},
		{"--host-report-timeout-minutes", "1.5", "from 5 to 1440"},
		{"--host-report-timeout-minutes", "abc", "from 5 to 1440"},
		{"--host-report-timeout-minutes", "", "from 5 to 1440"},
		{"--host-report-timeout-minutes", "2", "from 5 to 1440"},
		{"--session-end-grace-minutes", "0", "from 1 to 1440"},
		{"--session-end-grace-minutes", "1441", "from 1 to 1440"},
		{"--session-end-grace-minutes", "1.5", "from 1 to 1440"},
		{"--session-end-grace-minutes", "abc", "from 1 to 1440"},
		{"--session-end-grace-minutes", "", "from 1 to 1440"},
		{"--session-end-grace-minutes", "5000", "from 1 to 1440"},
	}
	for _, tc := range cases {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			calls, _, err := runStallSet(t, tc.flag, tc.value)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want a usage refusal, got %v (exit %d)", err, exitCodeFor(err))
			}
			if !strings.Contains(err.Error(), tc.flag) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal should name %s and %q, got %v", tc.flag, tc.want, err)
			}
			assertNoSetCall(t, calls)
		})
	}
}

// M1: a mixed valid set sends exactly the three keys, with their values.
func TestC983MixedValidSendsExactlyThoseKeys(t *testing.T) {
	calls, _, err := runStallSet(t,
		"--session-end-grace-minutes", "15",
		"--host-report-timeout-minutes", "default",
		"--no-progress-minutes", "90")
	if err != nil {
		t.Fatal(err)
	}
	in := stallSetInput(t, calls)
	want := map[string]any{"sessionEndGraceMinutes": float64(15), "hostReportTimeoutMinutes": nil, "noProgressMinutes": float64(90)}
	if len(in) != len(want) {
		t.Fatalf("input %v, want exactly %v", in, want)
	}
	for k, v := range want {
		if got, present := in[k]; !present || got != v {
			t.Fatalf("%s = %v (present %v), want %v", k, got, present, v)
		}
	}
}

// M2: one invalid value in a mixed set sends nothing — no partial write.
func TestC983MixedWithOneInvalidSendsNothing(t *testing.T) {
	calls, _, err := runStallSet(t,
		"--session-end-grace-minutes", "15",
		"--host-report-timeout-minutes", "4")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("want a usage refusal, got %v", err)
	}
	assertNoSetCall(t, calls)
}

// D1: get shows the values the server returned, in text.
func TestC983GetRendersServerReturnedValues(t *testing.T) {
	_, out, err := runTeamTask(t, stallResponses(), "team", "stall-schedule", "get")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session-end grace", "10 min", "cor:agt:050:10", "host report timeout", "60 min", "cor:agt:070:02"} {
		if !strings.Contains(out, want) {
			t.Fatalf("get text missing %q:\n%s", want, out)
		}
	}
}

// D2: --json carries both keys as integers, alongside every existing key.
func TestC983GetJSONCarriesBothFields(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "stall-schedule", "get", "--json"},
		{"team", "stall-schedule", "set", "--session-end-grace-minutes", "10", "--json"},
	} {
		_, out, err := runTeamTask(t, stallResponses(), argv...)
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		want := map[string]any{"sessionEndGraceMinutes": float64(10), "hostReportTimeoutMinutes": float64(60)}
		for k, v := range want {
			if dto[k] != v {
				t.Fatalf("%v: %s = %v, want %v", argv, k, dto[k], v)
			}
		}
		for _, k := range []string{"appId", "assignmentMinutes", "notStartedMinutes", "noProgressMinutes", "ownerReactionMinutes", "hostLapseCheckIns", "updatedAt"} {
			if _, ok := dto[k]; !ok {
				t.Fatalf("%v: existing key %q missing", argv, k)
			}
		}
		if len(dto) != 9 {
			t.Fatalf("%v: want exactly the 7 existing keys plus 2, got %v", argv, dto)
		}
	}
}

// P3: the no-values refusal lists the two new flags too.
func TestC983NoValuesRefusalListsNewFlags(t *testing.T) {
	calls, _, err := runStallSet(t)
	if err == nil {
		t.Fatal("set with no flags must refuse")
	}
	for _, want := range []string{"--session-end-grace-minutes", "--host-report-timeout-minutes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal should list %s: %v", want, err)
		}
	}
	assertNoSetCall(t, calls)
}

// Jane's and Anne's #986 P3: the platform defaults (10, 60) are also what a
// hard-coded mapping would print, so pin values only the server could have
// supplied — get text, get --json and set --json all render them.
func TestC983RendersNonDefaultServerValues(t *testing.T) {
	r := stallResponses()
	nondefault := strings.NewReplacer(`"sessionEndGraceMinutes":10`, `"sessionEndGraceMinutes":37`,
		`"hostReportTimeoutMinutes":60`, `"hostReportTimeoutMinutes":77`)
	for _, op := range []string{"TeamStallSchedule", "TeamStallScheduleSet"} {
		r[op] = nondefault.Replace(r[op])
	}
	_, out, err := runTeamTask(t, r, "team", "stall-schedule", "get")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"37 min", "77 min"} {
		if !strings.Contains(out, want) {
			t.Fatalf("get text missing %q:\n%s", want, out)
		}
	}
	for _, argv := range [][]string{
		{"team", "stall-schedule", "get", "--json"},
		{"team", "stall-schedule", "set", "--session-end-grace-minutes", "37", "--json"},
	} {
		_, out, err := runTeamTask(t, r, argv...)
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if dto["sessionEndGraceMinutes"] != float64(37) || dto["hostReportTimeoutMinutes"] != float64(77) {
			t.Fatalf("%v: want the server's 37/77, got %v/%v", argv, dto["sessionEndGraceMinutes"], dto["hostReportTimeoutMinutes"])
		}
	}
}
