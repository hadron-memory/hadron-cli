package team

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

func TestTaskWakeEvents2107(t *testing.T) {
	for event, want := range map[string]string{
		"CHILD_CANCELLED":         "child cancelled",
		"CHILD_FINISHED":          "child finished",
		"FUTURE_CHILD_STATE":      "FUTURE_CHILD_STATE",
		"X\x1b[31m\nINJECT":       "X [31m INJECT",
		"line\u2028sep\u0085next": "line sep next",
		"ÉVÉNEMENT_日本語":           "ÉVÉNEMENT_日本語",
	} {
		t.Run(event, func(t *testing.T) {
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Replace(wakePage2, `"event":"CHILD_FINISHED"`, `"event":`+string(encoded), 1)
			// The fixture mutation is SCOPED to the one event field (a single
			// occurrence replaced) and decodes back to exactly the original value.
			if strings.Count(wakePage2, `"event":"CHILD_FINISHED"`) != 1 {
				t.Fatal("the fixture must hold exactly one event to mutate")
			}
			var decoded struct {
				Data struct {
					DueWakes struct {
						Wakes []struct {
							Reasons []struct {
								Task struct {
									Event string `json:"event"`
								} `json:"task"`
							} `json:"reasons"`
						} `json:"wakes"`
					} `json:"dueWakes"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.Data.DueWakes.Wakes[0].Reasons[0].Task.Event != event {
				t.Fatalf("the mutated page must decode to the original event %q: %v", event, err)
			}
			s, calls := deliveryFake(t, map[string][]string{"DueWakes": {body}})
			page, err := s.Wakes(context.Background())
			if err != nil || len(page.Wakes) != 1 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			wake := page.Wakes[0]
			if err := delivery.CheckWake(wake); err != nil {
				t.Fatal(err)
			}
			if got := delivery.RenderWake(wake.Reasons); got != "[hadron task] "+want+": Child (AT1.1.2, t2)" {
				t.Fatalf("rendered %q", got)
			}
			if got := calls(); len(got) != 1 || got[0].op != "DueWakes" || got[0].session != "" {
				t.Fatalf("calls %+v", got)
			}
		})
	}
}

// Every TaskWakeEvent the schema has TODAY must have deliberate delivery wording
// (a lower-case, spaced phrase), and an event this build does not know stays
// verbatim: a new enum member fails here until someone words it on purpose
// (cli#2141). The raw token stays what `team wakes due` shows.
func TestEveryCurrentTaskWakeEventHasDeliberateDeliveryWording2141(t *testing.T) {
	if len(gen.AllTaskWakeEvent) == 0 {
		t.Fatal("no TaskWakeEvent members: the generated list moved")
	}
	for _, ev := range gen.AllTaskWakeEvent {
		raw := string(ev)
		want := strings.ToLower(strings.ReplaceAll(raw, "_", " "))
		if got := wakeEventWords(raw); got != want || got == raw {
			t.Errorf("%s: wording %q, want %q (a member with no wording would read raw)", raw, got, want)
		}
	}
	for _, unknown := range []string{"FUTURE_EVENT", "", "assigned", "Assigned", "ASSIGNED "} {
		if got := wakeEventWords(unknown); got != unknown {
			t.Errorf("unknown/near-miss %q must stay verbatim, got %q", unknown, got)
		}
	}
}
