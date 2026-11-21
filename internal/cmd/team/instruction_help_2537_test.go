package team

import (
	"strings"
	"testing"
)

// cli#2537 (from #2505 F1 and Jonas's #2529 review): pin the derivation
// itself, not just the rendered command help. The swap must replace the
// shared floor wherever it appears, keep every other byte, keep the
// older-server failure-mode sentence, and panic — not silently no-op — when
// the shared text no longer names the original floor.
func TestInstructionHelpForSwapsFloor2537(t *testing.T) {
	got := instructionHelpFor("2026-11-04")
	if got == instructionHelp {
		t.Fatal("derived help is the shared text unchanged")
	}
	if !strings.Contains(got, "2026-11-04") || strings.Contains(got, instructionHelpFloor) {
		t.Fatalf("derived help must name 2026-11-04 and not %s", instructionHelpFloor)
	}
	if want := strings.ReplaceAll(instructionHelp, instructionHelpFloor, "2026-11-04"); got != want {
		t.Fatal("derived help must be the shared text with only the floor swapped")
	}
	if !strings.Contains(got, "refusal is passed through, never retried without it") {
		t.Fatal("derived help lost the shared text's older-server refusal sentence (#2505 F5c doc side)")
	}
}

func TestSwapMinServerPanicsWhenOriginalMissing2537(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a shared text without the original floor must panic, not no-op")
		}
		// The panic names the function that raises it, not its caller (#2552 item 3).
		if msg, _ := r.(string); !strings.HasPrefix(msg, "swapFloor:") {
			t.Fatalf("panic message must name swapFloor: %v", r)
		}
	}()
	swapFloor("no floor here", instructionHelpFloor, "2026-11-04")
}
