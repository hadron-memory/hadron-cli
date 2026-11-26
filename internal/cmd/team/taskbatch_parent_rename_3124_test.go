package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-server#5080 renamed the task object's parentTaskId to parentTaskRef
// with no alias (cli#3124). The gate's binding condition: a stale batch file is
// refused with a message that names the new key, at the item's 1-based
// position, as a usage error; the new key is accepted.
func TestParseTaskBatchRefusesRetiredParentTaskId3124(t *testing.T) {
	stale := `[{"task":{"title":"a"}},{"task":{"title":"b","parentTaskId":"AT0.1"}}]`
	_, err := parseTaskBatch([]byte(stale))
	if err == nil {
		t.Fatal("the retired parentTaskId key must be refused")
	}
	for _, want := range []string{"task 2:", `"parentTaskId" was renamed to "parentTaskRef"`, `use "parentTaskRef"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not contain %q", err.Error(), want)
		}
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage (%d)", got, exitcode.Usage)
	}
}

func TestParseTaskBatchAcceptsParentTaskRef3124(t *testing.T) {
	items, err := parseTaskBatch([]byte(`[{"task":{"title":"b","parentTaskRef":"AT0.1"}}]`))
	if err != nil {
		t.Fatalf("parentTaskRef must be accepted: %v", err)
	}
	if len(items) != 1 || !strings.Contains(string(items[0].Task), `"parentTaskRef"`) {
		t.Errorf("the key must reach the request as written, got %v", items)
	}
}
