package hostpoller

import (
	"os"
	"regexp"
	"testing"
)

// hostReportedReasons mirrors hadron-server's HOST_REPORTED_REASONS
// (src/lib/contextReset/types.ts at aa4263ab): the only leading codes a
// FAILED report of a live reset may carry. Anything else is refused
// BAD_CONTEXT_RESET_INPUT and the operation is re-delivered.
var hostReportedReasons = map[string]bool{
	"DIRTY_TREE": true, "UNPUSHED_HEAD": true, "BUSY": true, "DRAFT_OR_MENU": true, "TWO_AGENTS": true,
	"NOT_IDLE": true, "PROCESS_CHANGED": true, "CHECKPOINT_FAILED": true, "STAGING_FAILED": true,
	"RELAUNCH_UNVERIFIED": true, "RELAUNCH_BLOCKED": true,
}

// reasonExceptions are reported codes outside that set, each with why.
var reasonExceptions = map[string]string{
	"HOST_UNSUPPORTED":    "awaiting the spec owner (hadron-cli#1043 comment 6595271707)",
	"BAD_OPERATION":       "awaiting the spec owner (hadron-cli#1043 comment 6595271707)",
	"CONTEXT_RESET_ENDED": "sent only for an ended reset, which takes any code",
}

// TestResetReportsOnlyReportableReasons scans the handler for every code it
// reports FAILED with (refuse("X", …) and "X: …" reasons) and requires each
// to be one the server takes, or a listed exception.
func TestResetReportsOnlyReportableReasons(t *testing.T) {
	codes := regexp.MustCompile(`refuse\("([A-Z_]+)"|"([A-Z_]{4,}): |"([A-Z_]{4,})"\}?\)?$|Reason: "([A-Z_]+)"|endedWithoutReason = "([A-Z_]+)"|, "([A-Z_]{4,})", "`)
	seen := 0
	for _, f := range []string{"reset_handler.go", "reset_forward.go", "reset_recovery.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range codes.FindAllStringSubmatch(string(src), -1) {
			for _, c := range m[1:] {
				if c == "" || c == "CONTEXT_RESET_NOT_FOUND" || c == "HOST_OPERATION_NOT_COLLECTED" {
					continue // read outcomes, never reported
				}
				seen++
				if !hostReportedReasons[c] && reasonExceptions[c] == "" {
					t.Errorf("%s reports %s, which the server refuses on a live reset", f, c)
				}
			}
		}
	}
	if seen < 10 {
		t.Fatalf("found only %d reported codes: the scan no longer matches the handler", seen)
	}
}
