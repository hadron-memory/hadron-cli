package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2896 (companion to hadron-server#3819 slice 1): `team utilization` renders a null (open)
// family and shows lastMatchFailure. The server's SDL: family is null when the failed task left
// the family open (no family named, or some excluded); lastMatchFailure is {at, reason?}, the
// latest no-worker match outcome inside the window, null otherwise (a later match clears it);
// saturated is set by a no-worker outcome too. The null word is the one the CLI prints for a null
// tier ("—"), so the two read alike; the rows print in the server's order.

const utilization2896 = `{"data":{"teamUtilization":{"appId":"capp100000000000000000000","windowHours":24,"computedAt":"2026-11-22T06:00:00Z","combinations":[
 {"family":null,"tier":"high","role":"qa","eligibleWorkers":0,"busyWorkers":0,"utilization":1,"saturated":true,"lastMatchFailure":{"at":"2026-11-22T05:59:00Z","reason":"REQUIREMENTS_EXCLUDED"}},
 {"family":"claude","tier":"mid","role":"cli-engineer","eligibleWorkers":4,"busyWorkers":4,"utilization":1,"saturated":true,"lastMatchFailure":{"at":"2026-11-22T05:30:00Z","reason":null}},
 {"family":"codex","tier":null,"role":null,"eligibleWorkers":2,"busyWorkers":1,"utilization":0.5,"saturated":false,"lastMatchFailure":null}]}}}`

// A row with family null prints "—" (the word a null tier prints) and "family": null in --json, never "".
func TestUtilizationNullFamilyIsExplicit2896(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var row string
	for _, l := range lines {
		if strings.Contains(l, "REQUIREMENTS_EXCLUDED") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("the open-family row is missing:\n%s", out)
	}
	if f := strings.Fields(row); f[0] != "—" {
		t.Fatalf("an open family reads like a null tier ('—'), got %q in %q", f[0], row)
	}
	_, js, err := runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Combinations []map[string]json.RawMessage `json:"combinations"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatal(err)
	}
	if raw, ok := dto.Combinations[0]["family"]; !ok || string(raw) != "null" {
		t.Fatalf(`an open family is "family": null in --json (never ""), got %s (present=%v)`, raw, ok)
	}
	if string(dto.Combinations[1]["family"]) != `"claude"` {
		t.Fatalf("a named family stays a string: %s", dto.Combinations[1]["family"])
	}
}

// lastMatchFailure prints its time and reason exactly as served; a null reason prints the time alone;
// an absent one prints "—"; --json is the object (reason null when null) or null.
func TestUtilizationLastMatchFailureIsShownAsServed2896(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LAST MATCH FAILURE") {
		t.Fatalf("the new column needs its header:\n%s", out)
	}
	for _, want := range []string{"2026-11-22T05:59:00Z REQUIREMENTS_EXCLUDED", "2026-11-22T05:30:00Z"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "05:30:00Z") && (strings.Contains(l, "NO_ROLE") || strings.Contains(l, "null")) {
			t.Fatalf("a null reason prints the time alone (nothing invented): %q", l)
		}
		if strings.HasPrefix(strings.TrimSpace(l), "codex") && !strings.HasSuffix(strings.TrimSpace(l), "—") {
			t.Fatalf("a row without a failure prints '—' in the new column: %q", l)
		}
	}
	_, js, err := runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Combinations []struct {
			LastMatchFailure *struct {
				At     string  `json:"at"`
				Reason *string `json:"reason"`
			} `json:"lastMatchFailure"`
		} `json:"combinations"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatal(err)
	}
	c := dto.Combinations
	if c[0].LastMatchFailure == nil || c[0].LastMatchFailure.At != "2026-11-22T05:59:00Z" || c[0].LastMatchFailure.Reason == nil || *c[0].LastMatchFailure.Reason != "REQUIREMENTS_EXCLUDED" {
		t.Fatalf("first row: %+v", c[0].LastMatchFailure)
	}
	if c[1].LastMatchFailure == nil || c[1].LastMatchFailure.Reason != nil {
		t.Fatalf("a null reason stays null in --json: %+v", c[1].LastMatchFailure)
	}
	if c[2].LastMatchFailure != nil {
		t.Fatalf("no failure is null in --json: %+v", c[2].LastMatchFailure)
	}
	if !strings.Contains(js, `"lastMatchFailure": null`) && !strings.Contains(js, `"lastMatchFailure":null`) {
		t.Fatalf("the key must be present as null, not omitted:\n%s", js)
	}
}

// A reason the CLI does not know (a future server's) is printed as served, control characters
// neutralised at render; the CLI never translates a reason into a promise.
func TestUtilizationUnknownReasonPassesThroughSafely2896(t *testing.T) {
	body := `{"data":{"teamUtilization":{"appId":"a","windowHours":24,"computedAt":"2026-11-22T06:00:00Z","combinations":[
	 {"family":"claude","tier":"high","role":"qa","eligibleWorkers":1,"busyWorkers":1,"utilization":1,"saturated":true,"lastMatchFailure":{"at":"2026-11-22T05:00:00Z","reason":"SOMETHING_NEW\u001b[31m"}}]}}}`
	_, out, err := runTeamTask(t, utilResponses(body), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SOMETHING_NEW") || strings.ContainsRune(out, 0x1b) {
		t.Fatalf("an unknown reason is shown as served, without a raw ESC:\n%q", out)
	}
}

// Existing columns and their cells are unchanged (the new column is appended): the old fixture's rows
// print exactly the cells they always did.
func TestUtilizationExistingRowsKeepTheirCells2896(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n")[2:] {
		rows = append(rows, strings.Fields(l))
	}
	want := [][]string{
		{"claude", "high", "cli-engineer", "4", "1", "25%", "no"},
		{"codex", "mid", "—", "2", "2", "100%", "no"},
		{"glm", "—", "qa", "0", "0", "100%", "yes"},
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %v", rows)
	}
	for i := range want {
		if strings.Join(rows[i][:7], " ") != strings.Join(want[i], " ") {
			t.Errorf("row %d: %v, want the cells %v first", i, rows[i], want[i])
		}
	}
}

// The help says what an open family means, what saturated means (the MOST RECENT match, a later match
// clears it), what lastMatchFailure is, and that rows are in the server's order.
func TestUtilizationHelpSaysWhatTheNewFieldsMean2896(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "utilization", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"no family named, or some excluded",
		"most recent match",
		"a later match clears it",
		"has workers and none eligible",
		"LAST MATCH FAILURE is",
		"server's order",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help must say %q:\n%s", want, help)
		}
	}
	for _, stale := range []string{"In this slice", "failure-only saturation arrives", "zero eligible workers in this slice"} {
		if strings.Contains(help, stale) {
			t.Errorf("help still carries the slice-1 wording %q", stale)
		}
	}
}

// --fail-on-saturated still reads the served saturated flag (a no-worker outcome sets it too).
func TestUtilizationFailOnSaturatedReadsTheServedFlag2896(t *testing.T) {
	_, _, err := runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all", "--fail-on-saturated")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("a saturated row (open family included) exits 5: %v (%d)", err, exitCodeFor(err))
	}
}
