package team

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/accountusage"
)

func win(at string, before bool) accountusage.WindowView {
	return accountusage.WindowView{ExhaustsAt: &at, ExhaustsBeforeReset: &before}
}

func TestRunsOutIsTheEarliestExhaustionAcrossWindows(t *testing.T) {
	cases := []struct {
		name string
		a    accountusage.AccountView
		want string
	}{
		{"seven-day first", accountusage.AccountView{FiveHour: win("2026-10-31T15:00:00Z", true), SevenDay: win("2026-10-31T13:00:00Z", true)}, "2026-10-31T13:00:00Z"},
		{"five-hour first", accountusage.AccountView{FiveHour: win("2026-10-31T13:00:00Z", true), SevenDay: win("2026-10-31T15:00:00Z", true)}, "2026-10-31T13:00:00Z"},
		{"earlier one resets first", accountusage.AccountView{FiveHour: win("2026-10-31T15:00:00Z", true), SevenDay: win("2026-10-31T13:00:00Z", false)}, "2026-10-31T15:00:00Z"},
		{"neither", accountusage.AccountView{FiveHour: win("2026-10-31T15:00:00Z", false)}, "—"},
		{"unknown", accountusage.AccountView{}, "—"},
	}
	for _, c := range cases {
		if got := runsOut(c.a); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestRunsOutRanksCodexAndClaudeWindowsOnOneAxis(t *testing.T) {
	// A 90%-used window with no burn (no exhaustion) does not outrank a window that exhausts.
	noBurn := accountusage.WindowView{}
	a := accountusage.AccountView{FiveHour: noBurn, Primary: &accountusage.WindowView{ExhaustsAt: strp("2026-11-04T13:00:00Z"), ExhaustsBeforeReset: boolp(true)}, Secondary: &accountusage.WindowView{ExhaustsAt: strp("2026-11-04T12:30:00Z"), ExhaustsBeforeReset: boolp(true)}}
	if got := runsOut(a); got != "2026-11-04T12:30:00Z" {
		t.Fatalf("got %s", got)
	}
	if got := runsOut(accountusage.AccountView{SourceState: "no-local-source"}); got != "—" {
		t.Fatalf("got %s", got)
	}
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
