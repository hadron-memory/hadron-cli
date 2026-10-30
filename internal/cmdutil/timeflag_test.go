package cmdutil

import "testing"

func TestParseTimeFlagStrict(t *testing.T) {
	for _, ok := range []string{"2026-10-22T00:00:00Z", "2026-10-22T00:00:00.123Z", "2026-10-22T00:00:00+23:59", "2026-10-22T00:00:00-05:30", " 2026-10-22T00:00:00Z "} {
		if v, err := ParseTimeFlag("since", ok); err != nil || v == nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "2026-10-22", "2026-10-22T00:00:00", "yesterday", "2026-10-22T00:00:00+24:00", "2026-10-22T00:00:00+00:60",
		"2026-10-22T00:00:00,5Z", "2026-10-22T0:00:00Z", "2026-10-22T00:00:00+0200", "2026-10-22T00:00:00+5:00"} {
		if v, err := ParseTimeFlag("since", bad); err == nil || v != nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
