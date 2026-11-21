package team

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// The real entry point uses os.Executable. Re-enter this test binary as the
// status command so no executable seam can bypass production key scrubbing.
//
// cli#2342: under test the TITLE PASS's Orca defaults are INERT — every
// caller that resolves its binary through orcaRunner(healthSweepOrcaBin),
// which is healthTitles and the version probe. A bare "orca" — the PATH
// fallback a host config with no delivery.orca_bin resolves to — never
// reaches a real binary: the lookup fails closed, and a runner built for
// the bare name answers an empty world. A test that stages a binary
// (delivery.orca_bin in its host config) still gets the real exec, so the
// fixtures' fake orcas and the fail-closed error paths run through it. The
// dupe pass builds its own ExecEnv client: cli#2404 supplies a private inert
// PATH executable too, without changing that client's credential scrubbing.
// Explicit delivery.orca_bin fixtures still execute normally.
func TestMain(m *testing.M) {
	runInertSweepOrca2404()
	failIfPrivateOrca2786()
	runAmbientTrap2786()
	failIfUnclaimed2786()
	if os.Getenv("HADRON_TEST_HEALTH_CHILD") == "1" && len(os.Args) > 1 && os.Args[1] == "orca" {
		const key = "SYNTHETIC_ENTRY_KEY_1181"
		for _, value := range os.Environ() {
			if strings.Contains(value, key) {
				os.Exit(41)
			}
		}
		for _, name := range []string{"HADRON_TOKEN", "HADRON_HOST_KEY", "HADRON_LAUNCH_GRANT"} {
			if os.Getenv(name) != "" {
				os.Exit(42)
			}
		}
		if os.Getenv("PATH") != "SYNTHETIC_PATH_1181" {
			os.Exit(43)
		}
		if len(os.Args) != 8 || strings.Join(os.Args[1:7], " ") != "orca terminal status --all --json --config" || !filepath.IsAbs(os.Args[7]) {
			os.Exit(44)
		}
		fmt.Fprint(os.Stderr, "PRIVATE_SCREEN_FROM_CHILD")
		fmt.Print(health1181Status)
		os.Exit(0)
	}
	healthLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	realRunner := orcaRunner
	orcaRunner = func(bin string) orcaterm.Runner {
		if bin == "orca" {
			return inertOrcaRunner2342
		}
		return realRunner(bin)
	}
	cleanup, err := installInertSweepOrca2404()
	if err != nil {
		fmt.Fprintln(os.Stderr, "installing inert test Orca:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// inertOrcaRunner2342 is the default Orca world an unstubbed TITLE-PASS test
// sees (cli#2342): every call answers ok with no terminals, so the title pass
// has nothing to rename. With no terminals listed, no rename is ever issued,
// so the one shape answers every verb.
var inertOrcaRunner2342 orcaterm.Runner = func(context.Context, []string) orcaterm.Result {
	return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
}

func TestHealth1181ProductionStatusEntry(t *testing.T) {
	t.Setenv("HADRON_TEST_HEALTH_CHILD", "1")
	t.Setenv("HADRON_TOKEN", "AMBIENT_DECOY")
	t.Setenv("HADRON_HOST_KEY", "SYNTHETIC_ENTRY_KEY_1181")
	t.Setenv("HADRON_LAUNCH_GRANT", "GRANT_DECOY")
	t.Setenv("CONTAINS_KEY", "prefix-SYNTHETIC_ENTRY_KEY_1181-suffix")
	t.Setenv("PATH", "SYNTHETIC_PATH_1181")
	d := t.TempDir()
	key := filepath.Join(d, "key")
	if err := os.WriteFile(key, []byte("SYNTHETIC_ENTRY_KEY_1181"), 0600); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(d, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = stderr
	defer func() { os.Stderr = original; _ = stderr.Close() }()
	raw, code, err := readHealthStatusCommand(context.Background(), filepath.Join(d, "host.yaml"), &hostpoller.Config{KeyFile: key})
	if err != nil || code != 0 || string(raw) != health1181Status {
		t.Fatalf("exit %d err %v", code, err)
	}
	captured, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Fatalf("child stderr escaped: %q", captured)
	}
}
