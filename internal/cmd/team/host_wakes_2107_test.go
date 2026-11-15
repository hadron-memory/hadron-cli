package team

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

func TestTaskWakeEvents2107(t *testing.T) {
	for event, want := range map[string]string{
		"CHILD_CANCELLED":    "child cancelled",
		"CHILD_FINISHED":     "child finished",
		"FUTURE_CHILD_STATE": "FUTURE_CHILD_STATE",
		"X\x1b[31m\nINJECT":  "X [31m INJECT",
	} {
		t.Run(event, func(t *testing.T) {
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Replace(wakePage2, `"event":"CHILD_FINISHED"`, `"event":`+string(encoded), 1)
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
