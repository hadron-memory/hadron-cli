package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#911 slice B2 (MCP cut C2, CHECKED T2; cor:agt:080:29): `team isolation
// set|clear --worker <id-or-URN>`. Ruling R1 (Holger, 2026-10-31): ship against
// the inert wire — until S7b (server#2130) `set` is refused
// WORKER_ISOLATION_UNAVAILABLE (exit 5) and `clear` works. Both are the
// person's write: a bound worktree's worker session never rides them.

func c911Isolate(body string) map[string]string {
	return map[string]string{"IsolateWorker": `{"data":{"isolateWorker":` + body + `}}`}
}

func c911Clear(body string) map[string]string {
	return map[string]string{"ClearWorkerIsolation": `{"data":{"clearWorkerIsolation":` + body + `}}`}
}

func TestC911IsolationSetSendsOnlyTheWorkerWithoutATimeFlag(t *testing.T) {
	calls, out, err := runTeamTask(t, c911Isolate(c1292Defaults), "team", "isolation", "set", "--worker", c1292Worker)
	if err != nil {
		t.Fatal(err)
	}
	sets := stopCalls(calls, "IsolateWorker")
	if len(sets) != 1 {
		t.Fatalf("set calls %+v", *calls)
	}
	// No until key at all: the server applies its 2-hour default.
	if !reflect.DeepEqual(sets[0].Vars, map[string]any{"workerRef": c1292Worker}) {
		t.Fatalf("vars %v, want exactly {workerRef} (an omitted until is the server's default, never null)", sets[0].Vars)
	}
	if !strings.Contains(out, "you may manage isolation") {
		t.Fatalf("set must print the returned settings:\n%s", out)
	}
}

func TestC911IsolationSetSendsUntilAsUTC(t *testing.T) {
	calls, _, err := runTeamTask(t, c911Isolate(c1292Defaults), "team", "isolation", "set", "--worker", c1292Worker, "--until", "2999-01-01T12:00:00+02:00")
	if err != nil {
		t.Fatal(err)
	}
	sets := stopCalls(calls, "IsolateWorker")
	if len(sets) != 1 || sets[0].Vars["until"] != "2999-01-01T10:00:00Z" {
		t.Fatalf("set calls %+v, want until 2999-01-01T10:00:00Z", sets)
	}
}

func TestC911IsolationSetForIsNowPlusTheDurationInUTC(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Second)
	calls, _, err := runTeamTask(t, c911Isolate(c1292Defaults), "team", "isolation", "set", "--worker", c1292Worker, "--for", "90m")
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	sets := stopCalls(calls, "IsolateWorker")
	if len(sets) != 1 {
		t.Fatalf("set calls %+v", sets)
	}
	raw, _ := sets[0].Vars["until"].(string)
	if !strings.HasSuffix(raw, "Z") {
		t.Fatalf("until %q is not sent as UTC", raw)
	}
	got, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("until %q: %v", raw, err)
	}
	if got.Before(before.Add(90*time.Minute)) || got.After(after.Add(90*time.Minute)) {
		t.Fatalf("until %s is not now+90m (between %s and %s)", got, before.Add(90*time.Minute), after.Add(90*time.Minute))
	}
}

func TestC911IsolationClearSendsOnlyTheWorker(t *testing.T) {
	calls, out, err := runTeamTask(t, c911Clear(c1292Defaults), "team", "isolation", "clear", "--worker", c1292Worker, "--json")
	if err != nil {
		t.Fatal(err)
	}
	clears := stopCalls(calls, "ClearWorkerIsolation")
	if len(clears) != 1 || !reflect.DeepEqual(clears[0].Vars, map[string]any{"workerRef": c1292Worker}) {
		t.Fatalf("clear calls %+v", clears)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	// One JSON shape for get, set and clear: the wake-settings DTO.
	for _, key := range []string{"worker", "mentions", "pausedUntil", "subscriptions", "effectiveCadenceSeconds", "dormant",
		"isolated", "isolatedAt", "isolatedByHandle", "isolatedUntil", "canManageIsolation"} {
		if _, ok := dto[key]; !ok {
			t.Errorf("--json lacks %q: %s", key, out)
		}
	}
	if subs, ok := dto["subscriptions"].([]any); !ok || len(subs) != 0 {
		t.Errorf("subscriptions must be [], got %v", dto["subscriptions"])
	}
	if dto["isolated"] != false || dto["canManageIsolation"] != false {
		t.Errorf("values changed on the way out: %s", out)
	}
}

func TestC911IsolationUsageErrorsSendNoRequest(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "isolation", "set", "--worker", c1292Worker, "--until", "2999-01-01T00:00:00Z", "--for", "1h"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--until", "2000-01-01T00:00:00Z"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--until", "2999-01-01T00:00:00.5Z"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--until", "2999-01-01T00:00:00"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--until", "tomorrow"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--for", "0"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--for", "-1h"},
		{"team", "isolation", "set", "--worker", c1292Worker, "--for", "soon"},
		{"team", "isolation", "set"},
		{"team", "isolation", "set", "--worker", "Iris"},
		{"team", "isolation", "clear"},
		{"team", "isolation", "clear", "--worker", "Iris"},
	} {
		responses := c911Isolate(c1292Defaults)
		responses["ClearWorkerIsolation"] = `{"data":{"clearWorkerIsolation":` + c1292Defaults + `}}`
		calls, _, err := runTeamTask(t, responses, argv...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: err %v, want exit %d", argv, err, exitcode.Usage)
		}
		if n := len(stopCalls(calls, "IsolateWorker")) + len(stopCalls(calls, "ClearWorkerIsolation")); n != 0 {
			t.Errorf("%v: reached the server", argv)
		}
	}
}

func TestC911IsolationRefusalsKeepTheirCodeAndExit(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"WORKER_ISOLATION_UNAVAILABLE", exitcode.Conflict},
		{"WORKER_ISOLATION_FORBIDDEN", exitcode.Forbidden},
		{"WORKER_NOT_FOUND", exitcode.NotFound},
		{"FORBIDDEN", exitcode.Forbidden},
		{"BAD_USER_INPUT", exitcode.Usage},
	} {
		for _, argv := range [][]string{
			{"team", "isolation", "set", "--worker", c1292Worker},
			{"team", "isolation", "clear", "--worker", c1292Worker},
		} {
			_, out, err := runTeamTask(t, map[string]string{
				"IsolateWorker":        c1291Errors(tc.code),
				"ClearWorkerIsolation": c1291Errors(tc.code),
			}, argv...)
			if err == nil || !strings.HasPrefix(err.Error(), tc.code+": ") {
				t.Errorf("%v: err %v, want %s prefix", argv, err, tc.code)
			}
			if got := exitCodeFor(err); got != tc.exit {
				t.Errorf("%v: exit %d, want %d", argv, got, tc.exit)
			}
			if out != "" {
				t.Errorf("%v: a refusal printed output:\n%s", argv, out)
			}
		}
	}
}

// The manager or holder acts as themselves (cor:agt:080:29): the server refuses
// any worker session WORKER_ISOLATION_FORBIDDEN, so on a BOUND worktree neither
// write carries X-Hadron-Session. The control proves the harness attaches one
// to an authored write, so the absence is not vacuous.
func TestC911IsolationCarriesNoWorkerSession(t *testing.T) {
	control, _, err := runTeamTask(t, map[string]string{
		"GetWorker":          `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"SetWorkerReportsTo": `{"data":{"setWorkerReportsTo":` + irisWorkerJSON + `}}`,
	}, "team", "worker", "set-reports-to", "hrn:worker:acme.com:eng-team:iris", "--clear")
	if err != nil {
		t.Fatal(err)
	}
	if c2Mutation(t, control, "SetWorkerReportsTo").Session == "" {
		t.Fatal("control: an authored write on this bound worktree carries no session, so the absence below proves nothing")
	}
	for _, tc := range []struct {
		op   string
		argv []string
	}{
		{"IsolateWorker", []string{"team", "isolation", "set", "--worker", c1292Worker}},
		{"ClearWorkerIsolation", []string{"team", "isolation", "clear", "--worker", c1292Worker}},
	} {
		responses := c911Isolate(c1292Defaults)
		responses["ClearWorkerIsolation"] = `{"data":{"clearWorkerIsolation":` + c1292Defaults + `}}`
		calls, _, err := runTeamTask(t, responses, tc.argv...)
		if err != nil {
			t.Fatal(err)
		}
		got := stopCalls(calls, tc.op)
		if len(got) != 1 || got[0].Session != "" {
			t.Errorf("%s calls %+v: must be sent once, with no worker session", tc.op, got)
		}
	}
}

func TestC911IsolationHelpStatesTheInertWireAndTheAudience(t *testing.T) {
	for _, sub := range []string{"set", "clear"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetOut(out)
		root.SetArgs([]string{"team", "isolation", sub, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := out.String()
		for _, want := range []string{"--worker", "cor:agt:080:29", "WORKER_ISOLATION_UNAVAILABLE", "never shows the worker isolated",
			"whoever manages the App", "holder", "bound worker session", "team wake-settings get"} {
			if !strings.Contains(help, want) {
				t.Errorf("%s --help lacks %q", sub, want)
			}
		}
	}
}
