//go:build !windows

package hostpoller

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The exec half of #1381: run a real child with a fully scrubbed environment and
// show it sees NOTHING of the parent's. The parent carries a credential and a
// sentinel that is not scrubbed, plus PATH; if ScrubbedEnv returned nil,
// exec.Cmd.Env would inherit all of them and the child would print them.

// The helper is the test binary itself, re-executed with "--" and a marker
// argument so it needs no environment variable to find its role. It reports
// variable NAMES only: a failing run (or a mutant) must never print the real
// parent environment's values, which can hold unrelated credentials.
func TestScrubbedEnvChildHelper1381(t *testing.T) {
	if args := flag.Args(); len(args) != 1 || args[0] != "env-helper-1381" {
		t.Skip("helper process only")
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		fmt.Println(name)
	}
	os.Exit(0)
}

// runEnvChild1381 returns the NAMES of the variables the child sees.
func runEnvChild1381(t *testing.T, env []string) map[string]bool {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestScrubbedEnvChildHelper1381$", "--", "env-helper-1381")
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, errOut.String())
	}
	names := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func TestScrubbedEnvChildInheritsNothing1381(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "leaked-token-1381")
	t.Setenv("INHERIT_SENTINEL_1381", "leaked-sentinel")
	// Prove the control first: a nil Env makes the child inherit the parent's
	// variables, so a leak would be visible to this very helper.
	inherited := runEnvChild1381(t, nil)
	if !inherited["INHERIT_SENTINEL_1381"] || !inherited["PATH"] {
		t.Fatalf("control failed: a nil Env must inherit the parent's variables (child saw %d variables; sentinel=%v PATH=%v)", len(inherited), inherited["INHERIT_SENTINEL_1381"], inherited["PATH"])
	}
	scrubbed := ScrubbedEnv([]string{"HADRON_TOKEN=leaked-token-1381"}, "k")
	if got := runEnvChild1381(t, scrubbed); len(got) != 0 {
		// Names and values of the real environment are never printed: counts and
		// the markers only.
		t.Fatalf("a fully scrubbed environment must reach the child as NOTHING, child saw %d variables (sentinel=%v HADRON_TOKEN=%v PATH=%v)", len(got), got["INHERIT_SENTINEL_1381"], got["HADRON_TOKEN"], got["PATH"])
	}
}
