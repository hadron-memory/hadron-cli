package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const usage1205Empty = `{"data":{"usageEvents":[]}}`

func usage1205Vars(t *testing.T, calls map[string]json.RawMessage) map[string]any {
	t.Helper()
	var vars map[string]any
	if err := json.Unmarshal(calls["UsageEvents"], &vars); err != nil {
		t.Fatal(err)
	}
	return vars
}

// cli#1205: --since / --until reach the server verbatim (Z and explicit
// offset), each alone or together; an omitted bound is omitted on the wire
// (never null).
func TestUsageEventsWindowBoundsSent(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		since, until any
	}{
		{"neither bound", nil, nil, nil},
		{"since only", []string{"--since", "2026-10-22T00:00:00Z"}, "2026-10-22T00:00:00Z", nil},
		{"until only", []string{"--until", "2026-10-23T00:00:00Z"}, nil, "2026-10-23T00:00:00Z"},
		{"fractional seconds and extreme valid offsets", []string{"--since", "2026-10-22T00:00:00.123Z", "--until", "2026-10-23T00:00:00+23:59"}, "2026-10-22T00:00:00.123Z", "2026-10-23T00:00:00+23:59"},
		{"window with offsets", []string{"--since", "2026-10-22T00:00:00+02:00", "--until", "2026-10-23T00:00:00-05:00"}, "2026-10-22T00:00:00+02:00", "2026-10-23T00:00:00-05:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"usage", "events", "--json"}, c.args...)
			out, stderr, code, calls := usage65Run(t, usage1205Empty, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, stderr)
			}
			vars := usage1205Vars(t, calls)
			for k, want := range map[string]any{"since": c.since, "until": c.until} {
				got, present := vars[k]
				if want == nil {
					if present {
						t.Errorf("%s must be omitted when not given, got %v", k, got)
					}
				} else if got != want {
					t.Errorf("%s = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// cli#1205: offset-less / date-only / malformed bounds and since >= until are
// usage errors (exit 2) with NOTHING sent to the server.
func TestUsageEventsWindowRefusedBeforeAnyCall(t *testing.T) {
	for _, args := range [][]string{
		{"--since", "2026-10-22T00:00:00"},
		{"--since", "2026-10-22"},
		{"--until", "2026-10-22T00:00:00"},
		{"--until", "2026-10-22"},
		{"--since", "yesterday"},
		{"--since", ""},
		{"--until", " "},
		// Malformed values Go's RFC 3339 parser alone would let through (LOCAL P2).
		{"--since", "2026-10-22T00:00:00+24:00"},
		{"--until", "2026-10-22T00:00:00+24:00"},
		{"--since", "2026-10-22T00:00:00+00:60"},
		{"--until", "2026-10-22T00:00:00+00:60"},
		{"--since", "2026-10-22T00:00:00,5Z"},
		{"--until", "2026-10-22T00:00:00,5Z"},
		{"--since", "2026-10-22T5:00:00Z"},
		{"--until", "2026-10-22T5:00:00Z"},
		{"--since", "2026-10-22T00:00:00+5:00"},
		{"--until", "2026-10-22T00:00:00+0200"},
		{"--since", "2026-10-22T00:00:00Z", "--until", "2026-10-22T00:00:00Z"},
		{"--since", "2026-10-23T00:00:00Z", "--until", "2026-10-22T00:00:00Z"},
		{"--since", "2026-10-22T02:00:00+02:00", "--until", "2026-10-21T23:00:00Z"},
	} {
		_, stderr, code, calls := usage65Run(t, `{}`, append([]string{"usage", "events"}, args...)...)
		if code != 2 || len(calls) != 0 {
			t.Errorf("%v: want exit 2 and no call, got %d %v (%s)", args, code, calls, stderr)
		}
	}
}

// cli#1205: the help states the window semantics, the offset requirement and
// the difference from `team events --since`.
func TestUsageEventsHelpStatesWindow(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"usage", "events", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"inclusive", "exclusive", "team events", "offset"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help must mention %q", want)
		}
	}
	if strings.Contains(out.String(), "no filter by date") {
		t.Error("help still says there is no date filter")
	}
}
