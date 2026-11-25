package team

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#3045 item 4 (from the #3043 review): `host run` says a load warning once at start; the sweep timer
// loads the same file every minute and must say nothing, or the warning repeats each tick. The call-site
// pin lives with host run; this one pins the sweep's silence on a file that DOES carry a warning.
func TestHostSweepSaysNothingOfALoadWarning3045(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "synthetic-manager-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"h1","sweepMode":"OBSERVE","workers":[]},"hostSetup":{"source":"HOST","assignments":[]}}}`)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	for name, body := range map[string]string{"key": "synthetic-host-key", "hadron-host-state.json": `{"hostId":"h1"}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(dir, "host.yaml")
	raw := fmt.Sprintf("server: %s\napp: hrn:app:example.com:team\nkey_file: key\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [claude-worker]\n    launcher_profile: {family: claude, mode: long, account: acct}\n    disk_tmp: %s\naccount_profiles:\n  acct: {bin: claude}\norca_launch:\n  orca_bin: /usr/bin/orca\n", srv.URL, dir, filepath.Join(dir, "tmp"))
	if err := os.WriteFile(cfg, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := hostpoller.LoadConfig(cfg)
	if err != nil || len(loaded.Warnings) == 0 {
		t.Fatalf("the fixture must carry a load warning for this pin to mean anything: %v %q", err, loadedWarnings(loaded))
	}

	old := sweepDaemonReady
	t.Cleanup(func() { sweepDaemonReady = old })
	sweepDaemonReady = func(context.Context, *hostpoller.Config) error { return hostpoller.ErrOrcaDown }
	streams, out, errOut := output.Test()
	f := &cmdutil.Factory{IOStreams: streams, ServerFlag: srv.URL, AppFlag: "hrn:app:example.com:team"}
	c := newCmdTeamHostSweep(f)
	c.SetArgs([]string{"--config", cfg})
	c.SetContext(context.Background())
	// The pass must have run to its end: an unavailable daemon is a completed sweep (nil error) that
	// prints the host-up result for the registered host. Silence from a sweep that failed earlier
	// (config, key, state file) would prove nothing.
	if err := c.Execute(); err != nil {
		t.Fatalf("the sweep must complete on the unavailable observation, got %v", err)
	}
	if !strings.Contains(out.String(), "h1") {
		t.Fatalf("the sweep never reported its pass for the registered host h1; stdout:\n%s", out.String())
	}
	for name, got := range map[string]string{"stdout": out.String(), "stderr": errOut.String()} {
		if strings.Contains(got, "warning:") {
			t.Errorf("the sweep must not repeat a load warning each run; %s:\n%s", name, got)
		}
	}
}

func loadedWarnings(c *hostpoller.Config) []string {
	if c == nil {
		return nil
	}
	return c.Warnings
}
