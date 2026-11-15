package cmd

import (
	"strings"
	"testing"
)

func TestCancelledChildWakeLabel2107(t *testing.T) {
	for _, event := range []string{"CHILD_CANCELLED", "FUTURE_CHILD_STATE", strings.Repeat("FUTURE_", 50)} {
		for _, jsonOutput := range []bool{false, true} {
			wake := strings.ReplaceAll(c1279TickWake, "ASSIGNED", event)
			args := []string{"team", "wakes", "due"}
			if jsonOutput {
				args = append(args, "--json")
			}
			calls, out, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, wake)), args...)
			if err != nil {
				t.Fatal(err)
			}
			want := event
			if event == "CHILD_CANCELLED" && !jsonOutput {
				want = "TASK AT1.4 t1 CHILD_CANCELLED"
			}
			if !strings.Contains(out, want) {
				t.Fatalf("event=%s json=%v want %q in %s", event, jsonOutput, want, out)
			}
			if got := stopCalls(calls, "TeamDueWakes"); len(got) != 1 || got[0].Session != "" {
				t.Fatalf("wake calls %+v", got)
			}
		}
	}
}
