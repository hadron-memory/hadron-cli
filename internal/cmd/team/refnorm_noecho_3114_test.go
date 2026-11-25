package team

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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

// cli#3189 item 1: a relative --bin is refused without the path the person typed, on both installers.
func TestInstallBinRefusalNeverShowsThePath3189(t *testing.T) {
	const canary = "rel-bin-canary-3189/hadron"
	for name, run := range map[string]func(f *cmdutil.Factory) (string, string, error){
		"usage": func(f *cmdutil.Factory) (string, string, error) {
			return usageRun(f, "install", "--config", usageConfigFile(t), "--interval", "15m", "--bin", canary)
		},
		"health": func(f *cmdutil.Factory) (string, string, error) {
			return healthRun(f, "install", "--config", hostConfig(t, ""), "--interval", "5m", "--bin", canary)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			out, errOut, err := run(f)
			if err == nil || exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), "--bin must be an absolute path") {
				t.Fatalf("want a Usage refusal naming --bin: %v", err)
			}
			if all := err.Error() + out + errOut; strings.Contains(all, "canary") {
				t.Errorf("shows the typed path: %q", all)
			}
		})
	}
}
