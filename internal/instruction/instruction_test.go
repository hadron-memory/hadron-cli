package instruction

import (
	"strings"
	"testing"
)

func TestFlagUsageForServer1602(t *testing.T) {
	for _, minimum := range []string{"17ef5905", "c3ca05b5", "future-server"} {
		got := FlagUsageForServer(minimum)
		if !strings.HasSuffix(got, "Needs server "+minimum+" or later") {
			t.Fatalf("minimum %q: %s", minimum, got)
		}
		if strings.TrimSuffix(got, minimum+" or later") != strings.TrimSuffix(FlagUsage, "17ef5905 or later") {
			t.Fatalf("syntax diverged for %q: %s", minimum, got)
		}
	}
}
