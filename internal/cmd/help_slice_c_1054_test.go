package cmd

import (
	"strings"
	"testing"
)

// cli#1054 slice C round 1 (Gemma, AT0.8581.1): the two failing pages. The
// usage page must show the host YAML's accounts: key and a sample; the sweep
// page must show a first invocation and say what its --config holds.
func TestSliceCRound1Fails1054(t *testing.T) {
	for _, tc := range []struct {
		page []string
		want []string
		ban  []string
	}{
		{[]string{"team", "usage"}, []string{
			"under accounts:", "accounts: - label: claude-main", "family: codex", "family (claude, codex or glm)",
		}, nil},
		{[]string{"team", "health", "sweep"}, []string{
			"hadron team health sweep --config ./host.yaml",
			"hadron team health sweep --config ./host.yaml --dry-run=false",
			"same host YAML as for \"team host run\"",
			"key_file", "worktrees_root",
		}, []string{"(the default; the status batch"}},
	} {
		help := flatHelp1053S3(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
		for _, b := range tc.ban {
			if strings.Contains(help, b) {
				t.Errorf("%v help still has %q", tc.page, b)
			}
		}
	}
}
