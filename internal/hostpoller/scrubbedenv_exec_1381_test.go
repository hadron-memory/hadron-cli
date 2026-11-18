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
// argument so it needs no environment variable to find its role.
func TestScrubbedEnvChildHelper1381(t *testing.T) {
	if args := flag.Args(); len(args) != 1 || args[0] != "env-helper-1381" {
		t.Skip("helper process only")
	}
	fmt.Print(strings.Join(os.Environ(), "\n"))
	os.Exit(0)
}

func runEnvChild1381(t *testing.T, env []string) []string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestScrubbedEnvChildHelper1381$", "--", "env-helper-1381")
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, errOut.String())
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestScrubbedEnvChildInheritsNothing1381(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "leaked-token-1381")
	t.Setenv("INHERIT_SENTINEL_1381", "leaked-sentinel")
	// Prove the control first: a nil Env makes the child inherit the parent's
	// variables, so a leak would be visible to this very helper.
	inherited := strings.Join(runEnvChild1381(t, nil), "\n")
	if !strings.Contains(inherited, "INHERIT_SENTINEL_1381=leaked-sentinel") || !strings.Contains(inherited, "PATH=") {
		t.Fatalf("control failed: a nil Env must inherit the parent's variables, child saw:\n%s", inherited)
	}
	scrubbed := ScrubbedEnv([]string{"HADRON_TOKEN=leaked-token-1381"}, "k")
	if got := runEnvChild1381(t, scrubbed); len(got) != 0 {
		t.Fatalf("a fully scrubbed environment must reach the child as NOTHING, child saw:\n%s", strings.Join(got, "\n"))
	}
}
