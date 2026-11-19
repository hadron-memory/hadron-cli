package cmd

import (
	"strings"
	"testing"
)

// cli#2611: `team pr schedule` and `team pr readiness` say how to read the
// schedule before a head is frozen. Both reads already exist, so the pages
// must name them, and the named read must really show review.schedule.
func TestPRPagesNameTheScheduleReads2611(t *testing.T) {
	for _, page := range []string{"schedule", "readiness"} {
		t.Run(page, func(t *testing.T) {
			help := flatHelp1053S3(t, "team", "pr", page)
			for _, w := range []string{
				"hadron team task get <build-task>", "review.schedule",
				"hadron team events --task <build-task> --type review.scheduled",
			} {
				if !strings.Contains(help, w) {
					t.Errorf("help lacks %q", w)
				}
			}
		})
	}
	help := flatHelp1053S3(t, "team", "events")
	if !strings.Contains(help, "--task") || !strings.Contains(help, "--type") {
		t.Errorf("team events lacks --task or --type")
	}
}

func TestTaskGetShowsReviewScheduleProperty2611(t *testing.T) {
	task := strings.Replace(taskFieldsJSON, `"result":null}`, `"result":null,"properties":{"review.schedule":["local","blind","qa"]}}`, 1)
	_, out, err := run1831(t, map[string]string{"AgentTaskGet": `{"data":{"agentTask":` + task + `}}`}, "team", "task", "get", "AT0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `properties: {"review.schedule":["local","blind","qa"]}`) {
		t.Errorf("task get does not show the schedule property:\n%s", out)
	}
}
