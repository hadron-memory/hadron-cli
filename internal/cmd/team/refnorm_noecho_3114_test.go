package team

import (
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
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
// cli#3216 item 4: both installers read their machine seam from healthEnv (usageEnv wraps it), so the test
// replaces it with the fake scheduler under a throwaway home: a regression that let the install go on
// could never write a real systemd or launchd unit, and the test then also proves nothing was written or
// run. The whole typed path is searched for, in the error and both streams, and the Factory carries
// real streams.
func TestInstallBinRefusalNeverShowsThePath3189(t *testing.T) {
	const typed = "rel-bin-canary-3189/hadron"
	for name, run := range map[string]func(f *cmdutil.Factory) (string, string, error){
		"usage": func(f *cmdutil.Factory) (string, string, error) {
			return usageRun(f, "install", "--config", usageConfigFile(t), "--interval", "15m", "--bin", typed)
		},
		"health": func(f *cmdutil.Factory) (string, string, error) {
			return healthRun(f, "install", "--config", hostConfig(t, ""), "--interval", "5m", "--bin", typed)
		},
	} {
		t.Run(name, func(t *testing.T) {
			hf, _, home := healthHarness(t, "systemctl", "crontab", "logger")
			io, _, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: io}
			out, errOut, err := run(f)
			if err == nil || exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), "--bin must be an absolute path") {
				t.Fatalf("want a Usage refusal naming --bin: %v", err)
			}
			if all := err.Error() + out + errOut; strings.Contains(all, typed) || strings.Contains(all, "rel-bin-canary") {
				t.Errorf("shows the typed path: %q", all)
			}
			if len(hf.calls) != 0 {
				t.Errorf("a refused install ran scheduler commands: %v", hf.calls)
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Errorf("a refused install wrote under the home: %v", entries)
			}
		})
	}
}
