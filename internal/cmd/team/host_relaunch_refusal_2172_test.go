package team

import (
	"strings"
	"testing"
)

// cli#2172 (Jibril's BLIND P2 and Rodolfo's §10 P3 on #2276): the local
// diagnostics show a refusal code only when it is one of the server's closed
// set (#2771: WORKER_NOT_FOUND, TERMINAL_REPORT_STALE, REPAIR_NOT_DARK,
// REPAIR_ATTEMPT_INVALID, REPAIR_REASON_INVALID). Upper-case text that is not
// one of them (an id, a name) never reaches the error.
func TestRefusalCodeDiagnosticsShowOnlyTheClosedSet2172(t *testing.T) {
	for _, code := range []string{"WORKER_NOT_FOUND", "TERMINAL_REPORT_STALE", "REPAIR_NOT_DARK", "REPAIR_ATTEMPT_INVALID", "REPAIR_REASON_INVALID"} {
		err := confirmRepairStored("worker", []string{code}, nil)
		if err == nil || !strings.Contains(err.Error(), code) {
			t.Errorf("the closed-set code %s is lost: %v", code, err)
		}
	}
	for name, code := range map[string]string{
		"upper-case id":      "PRIVATE_WORKER_123",
		"upper-case name":    "IRIS",
		"a future code":      "SOMETHING_NEW",
		"lower case":         "terminal_report_stale",
		"with a space":       "TERMINAL REPORT_STALE",
		"punctuation":        "TERMINAL_REPORT_STALE!",
		"oversized":          strings.Repeat("A", 65),
		"allowed plus extra": "TERMINAL_REPORT_STALE_123",
		"empty":              "",
	} {
		t.Run(name, func(t *testing.T) {
			err := confirmRepairStored("worker", []string{code}, nil)
			if err == nil {
				t.Fatal("a refused row must not confirm")
			}
			if code != "" && strings.Contains(err.Error(), code) {
				t.Errorf("the server string %q reached the local error: %v", code, err)
			}
			if !strings.Contains(err.Error(), "(not a known refusal code)") {
				t.Errorf("an unknown code must read as the placeholder: %v", err)
			}
		})
	}
}
