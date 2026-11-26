package memory

import (
	"strings"
	"testing"
)

// cli#3244: maskSyncText's floor, pinned directly (the command tests only see
// it through a surface).
func TestMaskSyncText3244(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain text", "nothing to see", "nothing to see"},
		{"url userinfo", "https://user:pw@host/repo", "https://[redacted]@host/repo"},
		{"userinfo with an @ in the password", "https://u:p@ss@host/r", "https://[redacted]@host/r"},
		{"each prefix", "gho_ABCDEFGH ghu_ABCDEFGH ghs_ABCDEFGH ghr_ABCDEFGH ghp_ABCDEFGH", "[redacted] [redacted] [redacted] [redacted] [redacted]"},
		{"github_pat and glpat", "github_pat_ABCDEFGH glpat-ABCDEFGH", "[redacted] [redacted]"},
		{"body of exactly 8", "ghp_12345678", "[redacted]"},
		{"body under 8 is not a token", "ghp_1234567", "ghp_1234567"},
		{"token at the start and end", "ghp_ABCDEFGH mid glpat-ABCDEFGH", "[redacted] mid [redacted]"},
		{"repeated tokens", "ghp_ABCDEFGH ghp_ABCDEFGH", "[redacted] [redacted]"},
		{"word boundary", "xghp_ABCDEFGH", "xghp_ABCDEFGH"},
		{"token and url together", "https://u:p@h/x ghp_ABCDEFGH", "https://[redacted]@h/x [redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := maskSyncText(tc.in)
			if got != tc.want {
				t.Errorf("maskSyncText(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := maskSyncText(got); again != got {
				t.Errorf("not idempotent: %q then %q", got, again)
			}
			if tc.want != tc.in && strings.Contains(got, "ABCDEFGH") {
				t.Errorf("leaked: %q", got)
			}
		})
	}
}
