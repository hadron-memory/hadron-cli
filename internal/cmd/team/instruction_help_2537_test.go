package team

import (
	"strings"
	"testing"
)

// cli#2537 (from #2505 F1 and Jonas's #2529 review): pin the derivation
// itself, not just the rendered command help. The swap must replace the
// shared revision wherever it appears, keep every other byte, keep the
// older-server failure-mode sentence, and panic — not silently no-op — when
// the shared text no longer names the original revision.
func TestInstructionHelpForSwapsRevision2537(t *testing.T) {
	got := instructionHelpFor("ba077810")
	if got == instructionHelp {
		t.Fatal("derived help is the shared text unchanged")
	}
	if !strings.Contains(got, "ba077810") || strings.Contains(got, instructionHelpMinServer) {
		t.Fatalf("derived help must name ba077810 and not %s", instructionHelpMinServer)
	}
	if want := strings.ReplaceAll(instructionHelp, instructionHelpMinServer, "ba077810"); got != want {
		t.Fatal("derived help must be the shared text with only the revision swapped")
	}
	if !strings.Contains(got, "refusal is passed through, never retried without it") {
		t.Fatal("derived help lost the shared text's older-server refusal sentence (#2505 F5c doc side)")
	}
}

func TestSwapMinServerPanicsWhenOriginalMissing2537(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a shared text without the original revision must panic, not no-op")
		}
	}()
	swapMinServer("no revision here", instructionHelpMinServer, "ba077810")
}
