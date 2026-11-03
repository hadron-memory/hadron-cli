package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// The real entry point uses os.Executable. Re-enter this test binary as the
// status command so no executable seam can bypass production key scrubbing.
func TestMain(m *testing.M) {
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
	os.Exit(m.Run())
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
