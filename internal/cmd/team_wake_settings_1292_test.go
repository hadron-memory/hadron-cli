package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1292 (CHECKED T1, cor:agt:080:03 / :21 / cor:api:240:02): `team
// wake-settings get|set --worker <ref>` reads and changes one worker's wake
// settings. `set` sends only the flags given: an omitted field keeps, so the
// request carries no null and no field the caller did not name.

const c1292Worker = "hrn:worker:acme.com:ops:iris"

const c1292Stored = `{"worker":{"id":"w1","name":"Iris","urn":"` + c1292Worker + `"},"mentions":"OFF","pausedUntil":"2026-11-01T00:00:00Z",` +
	`"subscriptions":[{"id":"sub1","ticker":{"id":"tk1","urn":"hrn:ticker:acme.com:ops:standup","name":"standup","cadenceSeconds":1800,"enabled":true},"viaGrant":true,"createdAt":"2026-10-30T00:00:00Z"}],` +
	`"effectiveCadenceSeconds":1800,"dormant":true,"isolated":true,"isolatedAt":"2026-10-30T01:00:00Z","isolatedByHandle":"holger","isolatedUntil":"2026-10-31T00:00:00Z","canManageIsolation":true}`

const c1292Defaults = `{"worker":{"id":"w1","name":"Iris","urn":null},"mentions":"IMMEDIATE","pausedUntil":null,"subscriptions":[],` +
	`"effectiveCadenceSeconds":null,"dormant":false,"isolated":false,"isolatedAt":null,"isolatedByHandle":null,"isolatedUntil":null,"canManageIsolation":false}`

func c1292Get(body string) map[string]string {
	return map[string]string{"WorkerWakeSettings": `{"data":{"workerWakeSettings":` + body + `}}`}
}

func c1292Set(body string) map[string]string {
	return map[string]string{"SetWorkerWakeSettings": `{"data":{"setWorkerWakeSettings":` + body + `}}`}
}

func TestC1292GetRendersTheFullShapeInJSON(t *testing.T) {
	calls, out, err := runTeamTask(t, c1292Get(c1292Stored), "team", "wake-settings", "get", "--worker", c1292Worker, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := stopCalls(calls, "WorkerWakeSettings"); len(got) != 1 || got[0].Vars["workerRef"] != c1292Worker {
		t.Fatalf("read calls %+v", got)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"worker", "mentions", "pausedUntil", "subscriptions", "effectiveCadenceSeconds", "dormant",
		"isolated", "isolatedAt", "isolatedByHandle", "isolatedUntil", "canManageIsolation"} {
		if _, ok := dto[key]; !ok {
			t.Errorf("--json lacks %q: %s", key, out)
		}
	}
	if dto["mentions"] != "OFF" || dto["pausedUntil"] != "2026-11-01T00:00:00Z" || dto["dormant"] != true || dto["canManageIsolation"] != true {
		t.Errorf("values changed on the way out: %s", out)
	}
	subs, _ := dto["subscriptions"].([]any)
	if len(subs) != 1 {
		t.Fatalf("subscriptions %v", dto["subscriptions"])
	}
}

func TestC1292GetShowsTheServersDefaultsAndEmptySubscriptionsAsAnArray(t *testing.T) {
	_, out, err := runTeamTask(t, c1292Get(c1292Defaults), "team", "wake-settings", "get", "--worker", c1292Worker, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"subscriptions": []`) && !strings.Contains(out, `"subscriptions":[]`) {
		t.Fatalf("empty subscriptions must be [], not null:\n%s", out)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if dto["mentions"] != "IMMEDIATE" || dto["pausedUntil"] != nil || dto["effectiveCadenceSeconds"] != nil || dto["isolated"] != false {
		t.Fatalf("defaults not shown as the server answered them: %s", out)
	}
}

func TestC1292GetTextNamesEveryField(t *testing.T) {
	_, out, err := runTeamTask(t, c1292Get(c1292Stored), "team", "wake-settings", "get", "--worker", c1292Worker)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Iris", "mentions", "OFF", "2026-11-01T00:00:00Z", "standup", "1800", "dormant", "isolated", "holger", "2026-10-31T00:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
}

func TestC1292SetSendsOnlyTheFlagsGiven(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want map[string]any
	}{
		{"mentions", []string{"--mentions", "off"}, map[string]any{"mentions": "OFF"}},
		{"mentions case", []string{"--mentions", "IMMEDIATE"}, map[string]any{"mentions": "IMMEDIATE"}},
		{"paused until", []string{"--paused-until", "2026-11-01T00:00:00Z"}, map[string]any{"pausedUntil": "2026-11-01T00:00:00Z"}},
		{"clear pause", []string{"--clear-pause"}, map[string]any{"clearPause": true}},
		{"mentions and pause", []string{"--mentions", "off", "--paused-until", "2026-11-01T00:00:00Z"},
			map[string]any{"mentions": "OFF", "pausedUntil": "2026-11-01T00:00:00Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := append([]string{"team", "wake-settings", "set", "--worker", c1292Worker}, tc.argv...)
			calls, out, err := runTeamTask(t, c1292Set(c1292Stored), argv...)
			if err != nil {
				t.Fatal(err)
			}
			sets := stopCalls(calls, "SetWorkerWakeSettings")
			if len(sets) != 1 || sets[0].Vars["workerRef"] != c1292Worker {
				t.Fatalf("set calls %+v", sets)
			}
			if got := sets[0].Vars["input"]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("input %v, want exactly %v (omission keeps; nothing is sent as null)", got, tc.want)
			}
			if !strings.Contains(out, "OFF") {
				t.Fatalf("set must print the returned settings:\n%s", out)
			}
		})
	}
}

func TestC1292SetJSONPrintsTheReturnedSettings(t *testing.T) {
	_, out, err := runTeamTask(t, c1292Set(c1292Stored), "team", "wake-settings", "set", "--worker", c1292Worker, "--mentions", "off", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["mentions"] != "OFF" {
		t.Fatalf("set --json: %v\n%s", err, out)
	}
}

func TestC1292UsageErrorsSendNoRequest(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "wake-settings", "set", "--worker", c1292Worker},
		{"team", "wake-settings", "set", "--worker", c1292Worker, "--clear-pause", "--paused-until", "2026-11-01T00:00:00Z"},
		{"team", "wake-settings", "set", "--worker", c1292Worker, "--mentions", "sometimes"},
		{"team", "wake-settings", "set", "--worker", c1292Worker, "--paused-until", "tomorrow"},
		{"team", "wake-settings", "set", "--mentions", "off"},
		{"team", "wake-settings", "set", "--worker", "Iris", "--mentions", "off"},
		{"team", "wake-settings", "get"},
		{"team", "wake-settings", "get", "--worker", "Iris"},
	} {
		calls, _, err := runTeamTask(t, c1292Set(c1292Stored), argv...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: err %v, want exit %d", argv, err, exitcode.Usage)
		}
		if n := len(stopCalls(calls, "SetWorkerWakeSettings")) + len(stopCalls(calls, "WorkerWakeSettings")); n != 0 {
			t.Errorf("%v: reached the server", argv)
		}
	}
}

func TestC1292RefusalsKeepTheirCodeAndExit(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"FORBIDDEN", exitcode.Forbidden},
		{"WORKER_NOT_FOUND", exitcode.NotFound},
	} {
		for _, argv := range [][]string{
			{"team", "wake-settings", "get", "--worker", c1292Worker},
			{"team", "wake-settings", "set", "--worker", c1292Worker, "--mentions", "off"},
		} {
			_, _, err := runTeamTask(t, map[string]string{
				"WorkerWakeSettings":    c1291Errors(tc.code),
				"SetWorkerWakeSettings": c1291Errors(tc.code),
			}, argv...)
			if err == nil || !strings.HasPrefix(err.Error(), tc.code+": ") {
				t.Errorf("%v: err %v, want %s prefix", argv, err, tc.code)
			}
			if got := exitCodeFor(err); got != tc.exit {
				t.Errorf("%v: exit %d, want %d", argv, got, tc.exit)
			}
		}
	}
}

func TestC1292HelpNamesTheFlagsAndTheOmissionRule(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"team", "wake-settings", "set", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--worker", "--mentions", "--paused-until", "--clear-pause", "Only the flags you give are sent", "cor:agt:080:21"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("set --help lacks %q", want)
		}
	}
}
