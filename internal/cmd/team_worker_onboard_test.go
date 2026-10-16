package cmd

import (
	"strings"
	"testing"
)

func TestTeamWorkerOnboardHelp837(t *testing.T) {
	_, out, err := runTeamTask(t, hostResponses(), "team", "worker", "onboard", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Orca-hosted workers only", "--apply", "--host", "orca1", "dry-run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("onboard help missing %q:\n%s", want, out)
		}
	}
}
