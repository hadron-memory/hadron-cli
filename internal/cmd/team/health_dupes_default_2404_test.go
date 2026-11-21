//go:build !windows

package team

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// The subprocess enters TestMain with ONLY a controlled PATH trap. Before the
// inert default is installed, sweepOrcaClient executes that trap, never a real
// host Orca. Afterward TestMain's private binary must take precedence.
func TestHealthDupeDefaultNeverExecutesAmbientOrca2404(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	trap := "#!/bin/sh\nprintf x > \"$HADRON_TEST_2404_MARKER\"\nprintf '%s' '{\"ok\":true,\"result\":{\"terminals\":[{\"handle\":\"path-trap\"}]}}'\n"
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte(trap), 0o700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHealthDupeDefaultChild2404$")
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "PATH="+dir, "HADRON_TEST_2404_CHILD=1", "HADRON_TEST_2404_MARKER="+marker)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unconfigured dupe client must see the inert world: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("ambient PATH Orca was reached (stat: %v)", err)
	}
}

func TestHealthDupeDefaultChild2404(t *testing.T) {
	if os.Getenv("HADRON_TEST_2404_CHILD") != "1" {
		t.Skip("only run in the controlled-PATH subprocess")
	}
	client, terms := sweepOrcaClient(context.Background(), &hostpoller.Config{})
	if len(terms) != 0 {
		t.Fatalf("the unconfigured default must be inert, got %+v", terms)
	}
	// sweepOrcaClient degrades an exec failure to no terminals. A direct read
	// distinguishes a real empty world from a missing/broken executable.
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("the inert executable must answer successfully: %v", err)
	}
}

func TestHealthDupeConfiguredOrcaKeepsScrubbedEnvironment2404(t *testing.T) {
	dir := t.TempDir()
	const key = "SYNTHETIC_HOST_KEY_2404"
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "configured-orca")
	script := `#!/bin/sh
if [ -n "$HADRON_TOKEN" ] || [ -n "$HADRON_HOST_KEY" ] || [ -n "$HADRON_LAUNCH_GRANT" ] || [ -n "$CARRIER_2404" ]; then exit 42; fi
if [ "$ORDINARY_2404" != keep ]; then exit 43; fi
printf '%s' '{"ok":true,"result":{"terminals":[{"handle":"configured-control"}]}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HADRON_TOKEN", "HADRON_HOST_KEY", "HADRON_LAUNCH_GRANT"} {
		t.Setenv(name, "SYNTHETIC_CREDENTIAL_2404")
	}
	t.Setenv("CARRIER_2404", "prefix-"+key+"-suffix")
	t.Setenv("ORDINARY_2404", "keep")
	cfg := &hostpoller.Config{KeyFile: keyFile}
	cfg.Delivery.OrcaBin = bin
	_, terms := sweepOrcaClient(context.Background(), cfg)
	if len(terms) != 1 || terms[0].Handle != "configured-control" {
		t.Fatalf("configured binary must execute with scrubbed credentials and ordinary env retained: %+v", terms)
	}
}
