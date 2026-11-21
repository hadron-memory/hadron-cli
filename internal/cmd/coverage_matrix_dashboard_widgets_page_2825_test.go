package cmd

import (
	"os"
	"strings"
	"testing"
)

// cli#2825 (child of #2787): Query.dashboardWidgetsPage is the paged spelling of
// dashboardWidgets, display layout for the portal's 12-column dashboard grid, and is
// EXCLUDED as graphical (cor:api:240:02) with the row text Petra ruled; no command,
// flag or help change. The two [Query] rows state the same reason, and the pair sits
// in the [Query] section, not in [Arguments] or [Filters].
func TestCoverageMatrixExcludesDashboardWidgetsPage2825(t *testing.T) {
	b, err := os.ReadFile("../api/coverage-matrix.txt")
	if err != nil {
		t.Fatal(err)
	}
	const want = "dashboardWidgetsPage  excluded graphical: places portal dashboard widgets on a 12-column grid (position, span, rowSpan); the paged spelling of dashboardWidgets, which is excluded for the same reason"
	section := ""
	found := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "[") {
			section = l[:strings.Index(l, "]")+1]
			continue
		}
		if f := strings.Fields(l); len(f) > 0 && f[0] == "dashboardWidgetsPage" {
			found++
			if section != "[Query]" || l != want {
				t.Errorf("matrix row in %s is %q, want it in [Query] as %q", section, l, want)
			}
		}
	}
	if found != 1 {
		t.Fatalf("dashboardWidgetsPage rows = %d, want exactly one", found)
	}
}
