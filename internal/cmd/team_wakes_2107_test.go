package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCancelledChildWakeLabel2107(t *testing.T) {
	for _, event := range []string{"CHILD_CANCELLED", "FUTURE_CHILD_STATE", strings.Repeat("FUTURE_", 50), "X\x1b[31m\nINJECT"} {
		for _, jsonOutput := range []bool{false, true} {
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			wake := strings.Replace(c1279TickWake, `"event":"ASSIGNED"`, `"event":`+string(encoded), 1)
			args := []string{"team", "wakes", "due"}
			if jsonOutput {
				args = append(args, "--json")
			}
			calls, out, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, wake)), args...)
			if err != nil {
				t.Fatal(err)
			}
			if jsonOutput {
				var payload struct {
					Wakes []struct {
						Reasons []struct {
							Task *struct {
								Event string `json:"event"`
							} `json:"task"`
						} `json:"reasons"`
					} `json:"wakes"`
				}
				if err := json.Unmarshal([]byte(out), &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload.Wakes) != 1 || len(payload.Wakes[0].Reasons) != 2 || payload.Wakes[0].Reasons[1].Task == nil || payload.Wakes[0].Reasons[1].Task.Event != event {
					t.Fatalf("decoded event mismatch: %s", out)
				}
			} else {
				wantEvent := event
				if event == "X\x1b[31m\nINJECT" {
					wantEvent = "X [31m INJECT"
				}
				want := "Iris: TICK standup, TASK AT1.4 t1 " + wantEvent
				found := false
				for _, line := range strings.Split(out, "\n") {
					if line == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("want exact line %q in %s", want, out)
				}
			}
			if got := stopCalls(calls, "TeamDueWakes"); len(got) != 1 || got[0].Session != "" {
				t.Fatalf("wake calls %+v", got)
			}
		}
	}
}
