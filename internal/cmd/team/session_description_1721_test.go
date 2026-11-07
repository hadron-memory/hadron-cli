package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestDescribeSession1721Lifecycle(t *testing.T) {
	user, tool, host := "user-id", "codex", "fixture-host"
	ended := "2026-11-07T09:00:00Z"
	for _, tc := range []struct {
		name         string
		ended        *string
		want, absent string
	}{
		{"live", nil, "since 2026-11-07T08:00:00Z", "ended "},
		{"ended", &ended, "ended " + ended, "since 2026-11-07T08:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := describeSession(&gen.TeamSessionFields{Id: "session-id", UserId: &user, Tool: &tool, Host: &host, StartedAt: "2026-11-07T08:00:00Z", EndedAt: tc.ended})
			for _, want := range []string{tc.want, "user-id", "via codex", "on fixture-host", "session session-id"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			if strings.Contains(got, tc.absent) {
				t.Errorf("unexpected %q in %q", tc.absent, got)
			}
		})
	}
}
