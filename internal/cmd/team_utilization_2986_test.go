package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2986: an empty lastMatchFailure reason prints a placeholder in the table; --json stays as served.

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
