package delivery

import (
	"strings"
	"testing"
)

// cli#2762 (server #4431 §3): an ANSWERED wake names the task handle and the
// answered question's code in fixed words, and never anything else the server
// might have attached: the title is not on this line, and the answer cannot be.
func TestRenderAnsweredWake2762(t *testing.T) {
	for _, tc := range []struct {
		name string
		task TaskReason
		want string
	}{
		{"handle and code", TaskReason{Event: "answered", ID: "01a1fcf4574672179bd8", Handle: "AT3.405", Title: "SECRET-TITLE", QuestionCode: "QA3"},
			"[hadron task] AT3.405 a question this task asked was answered: QA3"},
		{"no code falls back to the same sentence without it", TaskReason{Event: "answered", ID: "01a1fcf4574672179bd8", Handle: "AT3.405", Title: "SECRET-TITLE"},
			"[hadron task] AT3.405 a question this task asked was answered"},
		{"no handle falls back to the id", TaskReason{Event: "answered", ID: "01a1fcf4574672179bd8", Title: "SECRET-TITLE", QuestionCode: "QA3"},
			"[hadron task] 01a1fcf4574672179bd8 a question this task asked was answered: QA3"},
		{"a code is one line of fixed text", TaskReason{Event: "answered", ID: "i1", Handle: "AT1", QuestionCode: "QA3\x1b[31m\nINJECT"},
			"[hadron task] AT1 a question this task asked was answered: QA3 [31m INJECT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderWake([]WakeReason{{Kind: WakeTask, Task: &tc.task}})
			if got != tc.want {
				t.Fatalf("rendered %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "SECRET-TITLE") {
				t.Fatalf("the title leaked into the nudge: %q", got)
			}
		})
	}
}

// Every other event keeps its line: a QuestionCode on a non-ANSWERED reason is
// ignored, never printed.
func TestRenderNonAnsweredWakeUnchanged2762(t *testing.T) {
	task := TaskReason{Event: "assigned", ID: "i1", Handle: "AT1", Title: "Build", QuestionCode: "QA3"}
	got := RenderWake([]WakeReason{{Kind: WakeTask, Task: &task}})
	if want := "[hadron task] assigned: Build (AT1, i1)"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}
