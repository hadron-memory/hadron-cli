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
