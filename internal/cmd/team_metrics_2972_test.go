package cmd

// hadron-cli#2972: polish from the #2967 reviews of `team metrics` (cli#1826). Red pins first.

import (
	"strings"
	"testing"
)

const metricsProjectsHead2972 = `{
 "appId":"capp100000000000000000000","asOf":"2026-11-22T08:00:00Z",
 "window":{"since":"2026-11-15T08:00:00Z","until":"2026-11-22T08:00:00Z"},
 "startedOwned":{"all":{"total":1,"ids":["t1"],"truncated":false},
  "byOwner":[{"owner":null,"tasks":{"total":1,"ids":["t1"],"truncated":false},"byProject":[],"projectsTruncated":false}],
  "ownersTruncated":false},
 "projects":{"items":[%s],"truncated":%s},
 "workers":{"active":{"total":0,"ids":[],"truncated":false},"idle":{"total":0,"ids":[],"truncated":false},
  "overdue":{"total":0,"ids":[],"truncated":false},"inop":{"total":0,"ids":[],"truncated":false}},
 "flow":{"completed":{"total":0,"ids":[],"truncated":false},"started":{"total":0,"ids":[],"truncated":false}}}`

func metricsProjectItem2972(handle string, children string) string {
	return `{"project":{"handle":"` + handle + `","noProject":false,"taskId":"p-` + handle + `","title":"Epic ` + handle + `"},"children":[` + children + `]}`
}

func metricsText2972(t *testing.T, items, truncated string) string {
	t.Helper()
	body := strings.Replace(strings.Replace(metricsProjectsHead2972, "%s", items, 1), "%s", truncated, 1)
	_, text, err := runTeamTask(t, metricsResponses1826(body), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// Item 1: the projects report carries no total, so the CLI says how many it shows, never a bare count that
// reads as the server's own; a truncated report says so too (cor:agt:150).
func TestTeamMetricsStartedProjectsSaysHowManyAreShown2972(t *testing.T) {
	one := metricsProjectItem2972("AT1", `{"status":"STARTED","tasks":{"total":1,"ids":["t1"],"truncated":false}}`)
	two := one + "," + metricsProjectItem2972("AT2", `{"status":"STARTED","tasks":{"total":1,"ids":["t2"],"truncated":false}}`)
	if text := metricsText2972(t, two, "false"); !strings.Contains(text, "Started projects: 2 shown\n") || strings.Contains(text, "more projects than shown") {
		t.Errorf("two projects, none withheld: want 'Started projects: 2 shown' and no truncation line:\n%s", text)
	}
	text := metricsText2972(t, two, "true")
	if !strings.Contains(text, "Started projects: 2 shown\n") || !strings.Contains(text, "(more projects than shown)") {
		t.Errorf("a truncated report reads '2 shown' plus the truncation line, never a bare 2:\n%s", text)
	}
	if strings.Contains(text, "Started projects: 2\n") {
		t.Errorf("a bare derived count reads as the server's total:\n%s", text)
	}
	if text := metricsText2972(t, "", "false"); !strings.Contains(text, "Started projects: 0 shown\n") {
		t.Errorf("no projects: '0 shown':\n%s", text)
	}
}

// Item 4: a started project with no child tasks is still printed, and an owner the SDL leaves null renders.
func TestTeamMetricsHumanRenderOfEmptyChildrenAndNullOwner2972(t *testing.T) {
	text := metricsText2972(t, metricsProjectItem2972("AT7", ""), "false")
	if !strings.Contains(text, "Started projects: 1 shown") || !strings.Contains(text, "AT7 Epic AT7: no child tasks\n") {
		t.Errorf("a project with no children is printed, with a plain word, not a dangling colon:\n%s", text)
	}
	if !strings.Contains(text, "(no owner shown): 1\n") {
		t.Errorf("a null owner renders as '(no owner shown)':\n%s", text)
	}
}

// The live server never sends an empty children list: a started project with no tasks comes back with one
// zero-count bucket per status (QA at e666a4b9). The project is printed with those buckets as returned (the
// command never regroups or hides a zero), and the "no child tasks" wording above is the defensive case for
// an empty list only.
func TestTeamMetricsProjectWithOnlyZeroBucketsIsPrintedAsReturned2972(t *testing.T) {
	var buckets []string
	for _, st := range []string{"BACKLOG", "PENDING", "STARTED", "FINISHED", "CANCELLED"} {
		buckets = append(buckets, `{"status":"`+st+`","tasks":{"total":0,"ids":[],"truncated":false}}`)
	}
	text := metricsText2972(t, metricsProjectItem2972("AT8", strings.Join(buckets, ",")), "false")
	want := "AT8 Epic AT8: BACKLOG 0, PENDING 0, STARTED 0, FINISHED 0, CANCELLED 0\n"
	if !strings.Contains(text, "Started projects: 1 shown") || !strings.Contains(text, want) {
		t.Errorf("the live empty-project shape prints the project with its zero buckets:\n%s", text)
	}
	if strings.Contains(text, "no child tasks") {
		t.Errorf("zero buckets are the server's answer, not an empty list:\n%s", text)
	}
}
