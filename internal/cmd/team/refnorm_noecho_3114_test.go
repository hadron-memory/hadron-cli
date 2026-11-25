package team

import (
	"strings"
	"testing"
)

// The session's recorded repo was typed as --repo, so a malformed one is not shown either (#3181 BLIND F1).
func TestNormalizeArtifactRefHidesTheSessionRepo3114(t *testing.T) {
	for kind, raw := range map[string]string{"pr": "7", "commit": "0123456789abcdef", "branch": "feature/x"} {
		_, _, err := normalizeArtifactRef(kind, raw, "hush-hush")
		if err == nil || strings.Contains(err.Error(), "hush") || !strings.Contains(err.Error(), "session repo (not shown) is not owner/repo") {
			t.Errorf("%s: %v", kind, err)
		}
	}
}
