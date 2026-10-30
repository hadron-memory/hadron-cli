package team

import (
	"errors"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

// The public task refusal code must reach the human sink: the root renderer
// prints the innermost HumanMessage, so the prefix has to live inside the
// terminal-safe wrapper (cli#1131 merge P2, Jane).
func TestMapTaskErrorKeepsCodeAtHumanSink(t *testing.T) {
	for _, code := range []string{"TASK_BLOCKED", "TASK_BLOCKER_CYCLE", "TASK_NOT_FOUND", "TASK_NOT_OWNER", "TASK_INVALID_TRANSITION", "TASK_HANDLE_QUALIFY", "BAD_USER_INPUT",
		"TASK_ENDED", "TASK_NOT_ASSIGNEE", "TASK_CHILD_OWNER_MISMATCH", "TASK_OWNER_INVALID", "TASK_ASSIGNEE_INVALID", "TASK_STOPPED"} {
		raw := gqlerror.List{&gqlerror.Error{Message: "refused \x1b[31m now", Extensions: map[string]any{"code": code}}}
		err := mapTaskError(raw)
		var h interface{ HumanMessage() string }
		if !errors.As(err, &h) {
			t.Fatalf("%s: no HumanMessage", code)
		}
		human := h.HumanMessage()
		if !strings.HasPrefix(human, code+": ") {
			t.Errorf("%s: human sink lost the code: %q", code, human)
		}
		if strings.Contains(human, "\x1b") {
			t.Errorf("%s: control reached the human sink: %q", code, human)
		}
		if !strings.Contains(err.Error(), code) || !strings.Contains(err.Error(), "\x1b") {
			t.Errorf("%s: raw error must keep code and original text: %q", code, err.Error())
		}
	}
}
