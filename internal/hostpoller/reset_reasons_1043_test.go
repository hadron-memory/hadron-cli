package hostpoller

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// hostReportedReasons mirrors hadron-server's HOST_REPORTED_REASONS
// (src/lib/contextReset/types.ts at f5aa10e2): the leading codes a FAILED
// report of a live reset may carry after the claim. Anything else is
// refused BAD_CONTEXT_RESET_INPUT and the operation is re-delivered.
var hostReportedReasons = map[string]bool{
	"DIRTY_TREE": true, "UNPUSHED_HEAD": true, "BUSY": true, "DRAFT_OR_MENU": true, "TWO_AGENTS": true,
	"NOT_IDLE": true, "PROCESS_CHANGED": true, "CHECKPOINT_FAILED": true, "STAGING_FAILED": true,
	"RELAUNCH_UNVERIFIED": true, "RELAUNCH_BLOCKED": true,
}

// preclaimOnlyReasons mirrors the extra codes in the server's
// PRECLAIM_HOST_REPORTED_REASONS (same file; hadron-server#3309,
// cor:agt:050:27 part 11): reportable only while the reset is requested.
var preclaimOnlyReasons = map[string]bool{"HOST_UNSUPPORTED": true}

// reasonExceptions are reported codes outside those sets, each with why.
var reasonExceptions = map[string]string{
	"CONTEXT_RESET_ENDED": "sent only for an ended reset, which takes any code",
}

// claimBoundary is where each scanned file's code stops being pre-claim:
// the handler's checks end where it hands over to runReset (relaunchAgent,
// later in the file, runs after the stop), runReset claims at its
// ClaimReset call, and recovery always starts from a phase the server
// already holds. A boundary that cannot be found fails the scan, never
// widens it.
var claimBoundary = map[string]func(src string) int{
	"reset_handler.go":  func(src string) int { return strings.Index(src, "p.runReset(") },
	"reset_forward.go":  func(src string) int { return strings.Index(src, "ClaimReset(") },
	"reset_recovery.go": func(string) int { return 0 },
}

// TestResetReportsOnlyReportableReasons scans the handler for every code it
// reports FAILED with (refuse("X", …) and "X: …" reasons) and requires each
// to be one the server takes in the phase it is sent from, or a listed
// exception.
func TestResetReportsOnlyReportableReasons(t *testing.T) {
	codes := regexp.MustCompile(`refuse\("([A-Z_]+)"|"([A-Z_]{4,}): |"([A-Z_]{4,})"\}?\)?$|Reason: "([A-Z_]+)"|endedWithoutReason = "([A-Z_]+)"|, "([A-Z_]{4,})", "`)
	seen, preclaim := 0, 0
	for _, f := range []string{"reset_handler.go", "reset_forward.go", "reset_recovery.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		boundary := claimBoundary[f](src)
		if boundary < 0 {
			t.Fatalf("%s: the claim boundary is gone: the scan no longer matches runReset", f)
		}
		for _, loc := range codes.FindAllStringSubmatchIndex(src, -1) {
			for g := 2; g < len(loc); g += 2 {
				if loc[g] < 0 {
					continue
				}
				c := src[loc[g]:loc[g+1]]
				if c == "CONTEXT_RESET_NOT_FOUND" || c == "HOST_OPERATION_NOT_COLLECTED" {
					continue // read outcomes, never reported
				}
				seen++
				switch {
				case hostReportedReasons[c], reasonExceptions[c] != "":
				case preclaimOnlyReasons[c] && loc[g] < boundary:
					preclaim++
				case preclaimOnlyReasons[c]:
					t.Errorf("%s reports %s after the claim, which the server refuses there", f, c)
				default:
					t.Errorf("%s reports %s, which the server refuses on a live reset", f, c)
				}
			}
		}
	}
	if seen < 10 || preclaim == 0 {
		t.Fatalf("found only %d reported codes (%d pre-claim): the scan no longer matches the handler", seen, preclaim)
	}
}
