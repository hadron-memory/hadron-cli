package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
)

// cli#3179: the started-projects line follows cor:agt:150 R4 and Ruling B
// (tasks counted at any depth under the nearest marked project), never "direct
// children", and never promises the rows add up to all tasks. Help and the
// agentic-usage twin say the same words.
func TestMetricsStartedProjectsWording3179(t *testing.T) {
	_, help, err := runTeamTask(t, nil, "team", "metrics", "--help")
	if err != nil {
		t.Fatal(err)
	}
	var twin string
	for _, l := range strings.Split(agentic.Doc(), "\n") {
		if strings.HasPrefix(l, "hadron team metrics ") {
			twin = l
		}
	}
	if twin == "" {
		t.Fatal("no `hadron team metrics` line in agentic-usage")
	}
	for name, text := range map[string]string{"help": help, "agentic-usage": twin} {
		flat := strings.Join(strings.Fields(text), " ")
		if strings.Contains(flat, "direct child") || strings.Contains(flat, "top-level") {
			t.Errorf("%s still says direct children or top-level: %q", name, flat)
		}
		for _, want := range []string{"at any depth", "nearest marked project", "do not add up to all tasks"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
}
