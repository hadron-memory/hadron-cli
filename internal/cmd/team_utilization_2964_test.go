package cmd

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2964: follow-ups to the cli#2896 reviews. The help and the agent contract name the server
// floor (the query now selects lastMatchFailure, so an older server rejects the whole document), say
// that an open-family row is reachable only with --all, and say what a null
// and an empty reason print (the pins moved to team_utilization_2986_test.go
// with cli#2986); the long help lines follow the 78-column habit; and the 2896
// tests pin the empty-string cases and --fail-on-saturated exit 0.

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

// utilContractEntry2964 is the agent contract's team utilization entry. The entry is ONE line, so the
// line is the whole entry and no pin can be met by a neighbouring command's text.
func utilContractEntry2964(t *testing.T) string {
	t.Helper()
	for _, l := range strings.Split(string(readContract(t)), "\n") {
		if strings.HasPrefix(l, "hadron team utilization (--all") {
			return flat(l)
		}
	}
	t.Fatal("agentic-usage has no team utilization entry")
	return ""
}

// cli#2987: the contract states what the CLI does (family as served), not a server guarantee.
func TestAgenticUsageUtilizationPromisesNoServerFamilyShape2987(t *testing.T) {
	if entry := utilContractEntry2964(t); strings.Contains(entry, "never `\"\"`") {
		t.Error(`the CLI passes family through as served, so the entry must not promise it is never ""`)
	}
}

// cli#2987: the release note words the floor like the help, and its empty-reason cell is the one the
// table prints since cli#2986.
func TestUtilizationReleaseNoteFloorAndEmptyReason2987(t *testing.T) {
	b, err := os.ReadFile("../../docs/release-notes/team-utilization-2896.md")
	if err != nil {
		t.Fatal(err)
	}
	note := flat(string(b))
	for _, want := range []string{
		"needs server 2026-11-21 or later (the day it merged)",
		"a null reason prints the time alone, an empty or blank one the time and `(empty)`",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the release note must say %q", want)
		}
	}
}

func TestUtilizationHelpNamesTheServerFloorAndTheNotes2964(t *testing.T) {
	help := flat(utilHelpLong2964(t))
	for _, want := range []string{
		"Needs server 2026-11-21 or later (the day it merged)",
		"--family cannot select an open-family row",
		"shown only with --all",
		"a null reason prints the time alone",
		`an empty or blank reason prints the time and "(empty)"`,
	} {
		if !strings.Contains(help, want) {
			t.Errorf("team utilization --help must say %q:\n%s", want, help)
		}
	}
}

func TestAgenticUsageUtilizationNamesTheFloorAndTheNotes2964(t *testing.T) {
	entry := utilContractEntry2964(t)
	for _, want := range []string{
		"needs server 2026-11-21 or later (the day it merged)",
		"`family` is as served: the server sends null",
		"--family cannot select an open-family row",
		"the time alone for a null reason",
		"after the time for an empty or blank one",
	} {
		if !strings.Contains(strings.ToLower(entry), strings.ToLower(want)) {
			t.Errorf("the team utilization entry must say %q", want)
		}
	}
}

// The 78-column habit: no help line is longer than 78 characters (runes: an em dash is 3 bytes).
func TestUtilizationHelpLinesFollowTheColumnHabit2964(t *testing.T) {
	for n, l := range strings.Split(utilHelpLong2964(t), "\n") {
		if w := utf8.RuneCountInString(l); w > 78 {
			t.Errorf("help line %d is %d characters (>78): %q", n+1, w, l)
		}
	}
}

// utilization2964 holds the cell cases. Every row is a state the server can serve (cor:agt:060:13):
// a row with a lastMatchFailure had no worker at its latest match, so it is saturated.
const utilization2964 = `{"data":{"teamUtilization":{"appId":"capp100000000000000000000","windowHours":24,"computedAt":"2026-11-22T06:00:00Z","combinations":[
 {"family":"","tier":"high","role":"qa","eligibleWorkers":1,"busyWorkers":0,"utilization":0,"saturated":true,"lastMatchFailure":{"at":"2026-11-22T05:59:00Z","reason":""}},
 {"family":"claude","tier":"mid","role":"cli-engineer","eligibleWorkers":4,"busyWorkers":1,"utilization":0.25,"saturated":true,"lastMatchFailure":{"at":"2026-11-22T05:30:00Z","reason":null}}]}}}`

// utilizationUnsaturated2964 has no saturated row, so no row carries a lastMatchFailure.
const utilizationUnsaturated2964 = `{"data":{"teamUtilization":{"appId":"capp100000000000000000000","windowHours":24,"computedAt":"2026-11-22T06:00:00Z","combinations":[
 {"family":"claude","tier":"mid","role":"cli-engineer","eligibleWorkers":4,"busyWorkers":1,"utilization":0.25,"saturated":false,"lastMatchFailure":null}]}}}`

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
	_, _, err := runTeamTask(t, utilResponses(utilizationUnsaturated2964), "team", "utilization", "--all", "--fail-on-saturated")
	if err != nil {
		t.Fatalf("no saturated row: exit 0, got %v (%d)", err, exitCodeFor(err))
	}
	_, _, err = runTeamTask(t, utilResponses(utilization2896), "team", "utilization", "--all", "--fail-on-saturated")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("a saturated row still exits 5: %v", err)
	}
}
