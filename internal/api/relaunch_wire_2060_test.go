package api_test

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"testing"
)

// Regenerating after #4247 must not send its optional field to an old server
// from an ordinary health report that did not ask for a repair outcome.
func TestUnusedRepairReasonIsOmittedFromLegacyHealth2060(t *testing.T) {
	raw, err := json.Marshal(gen.WorkerTerminalReportInput{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["repairReason"]; present {
		t.Fatalf("new input leaked to legacy health: %s", raw)
	}
}
