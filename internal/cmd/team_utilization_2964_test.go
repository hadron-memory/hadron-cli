package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2964: follow-ups to the cli#2896 reviews. The help and the agent contract name the server
// floor (the query now selects lastMatchFailure, so an older server rejects the whole document), say
// that an open-family row is reachable only with --all, and say a null reason prints the time alone and an
// empty one prints "(empty)" after it (cli#2986); the long help lines follow the 78-column habit; and the 2896 tests pin the exact cell,
// the empty-string cases and --fail-on-saturated exit 0 on an unsaturated set.

func utilHelpLong2964(t *testing.T) string {
	t.Helper()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	c, _, err := root.Find([]string{"team", "utilization"})
	if err != nil || c == nil {
		t.Fatalf("find team utilization: %v", err)
	}
	return c.Long
}

func TestUtilizationHelpNamesTheServerFloorAndTheNotes2964(t *testing.T) {
	help := flat2906(utilHelpLong2964(t))
	for _, want := range []string{
		"Needs server 2026-11-21 or later",
		"--family cannot select an open-family row",
		"shown only with --all",
		"a null reason prints the time alone",
		`an empty reason prints the time and "(empty)"`,
	} {
		if !strings.Contains(help, want) {
			t.Errorf("team utilization --help must say %q:\n%s", want, help)
		}
	}
}

func TestAgenticUsageUtilizationNamesTheFloorAndTheNotes2964(t *testing.T) {
	contract := flat2906(string(readContract(t)))
	i := strings.Index(contract, "hadron team utilization (--all")
	if i < 0 {
		t.Fatal("agentic-usage has no team utilization entry")
	}
	entry := contract[i:]
	if j := strings.Index(entry[10:], "\nhadron "); j > 0 {
		entry = entry[:j+10]
	}
	if end := i + 6000; end < len(contract) && len(entry) > 6000 {
		entry = entry[:6000]
	}
	for _, want := range []string{
		"needs server 2026-11-21 or later",
		"--family cannot select an open-family row",
		"the time alone for a null reason",
		`"(empty)" after the time for an empty one`,
	} {
		if !strings.Contains(strings.ToLower(entry), strings.ToLower(want)) {
			t.Errorf("the team utilization entry must say %q", want)
		}
	}
}

// The 78-column habit: no help line is longer than 78 bytes.
func TestUtilizationHelpLinesFollowTheColumnHabit2964(t *testing.T) {
	for n, l := range strings.Split(utilHelpLong2964(t), "\n") {
		if len(l) > 78 {
			t.Errorf("help line %d is %d bytes (>78): %q", n+1, len(l), l)
		}
	}
}

const utilization2964 = `{"data":{"teamUtilization":{"appId":"capp100000000000000000000","windowHours":24,"computedAt":"2026-11-22T06:00:00Z","combinations":[
 {"family":"","tier":"high","role":"qa","eligibleWorkers":1,"busyWorkers":0,"utilization":0,"saturated":false,"lastMatchFailure":{"at":"2026-11-22T05:59:00Z","reason":""}},
 {"family":"claude","tier":"mid","role":"cli-engineer","eligibleWorkers":4,"busyWorkers":1,"utilization":0.25,"saturated":false,"lastMatchFailure":{"at":"2026-11-22T05:30:00Z","reason":null}}]}}}`

// The null reason's cell is exactly the time (the line ends with it: nothing was recorded). An EMPTY
// reason is a different state, a reason the server served as "", so it prints the time and the
// placeholder word "(empty)" (cli#2986), never a bare time or a trailing blank.
func TestUtilizationReasonCellsAreExact2964(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilization2964), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	var sawEmpty, sawNull bool
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "05:59:00Z"):
			sawEmpty = true
			if !strings.HasSuffix(l, "2026-11-22T05:59:00Z (empty)") {
				t.Errorf("an empty reason prints the time and the (empty) placeholder: %q", l)
			}
		case strings.Contains(l, "05:30:00Z"):
			sawNull = true
			if !strings.HasSuffix(l, "2026-11-22T05:30:00Z") {
				t.Errorf("a null reason prints the time alone: %q", l)
			}
		}
	}
	if !sawEmpty || !sawNull {
		t.Fatalf("rows missing:\n%s", out)
	}
}

// An empty-string family reads like a null one in the table: the null word, never a blank cell.
func TestUtilizationEmptyFamilyPrintsTheNullWord2964(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilization2964), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "05:59:00Z") {
			if f := strings.Fields(l); len(f) == 0 || f[0] != "—" {
				t.Fatalf("an empty family prints '—': %q", l)
			}
			return
		}
	}
	t.Fatalf("row missing:\n%s", out)
}

// --fail-on-saturated exits 0 when nothing shown is saturated (the flag is read, not assumed).
func TestUtilizationFailOnSaturatedExitsZeroOnAnUnsaturatedSet2964(t *testing.T) {
	_, _, err := runTeamTask(t, utilResponses(utilization2964), "team", "utilization", "--all", "--fail-on-saturated")
	if err != nil {
		t.Fatalf("no saturated row: exit 0, got %v (%d)", err, exitCodeFor(err))
	}
	_, _, err = runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all", "--fail-on-saturated")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("a saturated row still exits 5: %v", err)
	}
}

// cli#2986: the placeholder is a table word only. --json carries the reason exactly as served: an
// empty string stays "" (not null, not "(empty)") and a null stays null.
func TestUtilizationEmptyReasonJSONIsAsServed2986(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilization2964), "team", "utilization", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Combinations []struct {
			LastMatchFailure *struct {
				At     string  `json:"at"`
				Reason *string `json:"reason"`
			} `json:"lastMatchFailure"`
		} `json:"combinations"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	var sawEmpty, sawNull bool
	for _, c := range d.Combinations {
		m := c.LastMatchFailure
		if m == nil {
			continue
		}
		switch m.At {
		case "2026-11-22T05:59:00Z":
			sawEmpty = true
			if m.Reason == nil || *m.Reason != "" {
				t.Errorf("an empty reason stays the empty string in --json: %+v", m.Reason)
			}
		case "2026-11-22T05:30:00Z":
			sawNull = true
			if m.Reason != nil {
				t.Errorf("a null reason stays null in --json: %q", *m.Reason)
			}
		}
	}
	if !sawEmpty || !sawNull {
		t.Fatalf("rows missing:\n%s", out)
	}
	if strings.Contains(out, "(empty)") {
		t.Fatalf("the placeholder must never reach --json:\n%s", out)
	}
}
