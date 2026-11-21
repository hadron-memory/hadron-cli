package team

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// cli#2786: pins for the cli#2404 private-child guards. Everything here runs a
// private copy of THIS test binary (never a real host Orca) under a hard bound,
// and every refusal case passes `-test.run=^$` as its argv so a guard that
// fails open falls into an empty test run and exits instead of recursing into
// the suite or hanging.

const ambientTrapEnv2786 = "HADRON_TEST_2786_TRAP_MARKER"

// runAmbientTrap2786 makes a copy of the test binary named orca/orca.exe, in a
// directory that is not the private inert one, behave as an "ambient" Orca: it
// records that it was reached and answers a terminal. It is the Windows-capable
// stand-in for the shell-script trap of the !windows test.
func runAmbientTrap2786() {
	marker := os.Getenv(ambientTrapEnv2786)
	exe, err := os.Executable()
	if marker == "" || err != nil || strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe") != "orca" {
		return
	}
	_ = os.WriteFile(marker, []byte("x"), 0o600)
	_, _ = os.Stdout.WriteString(`{"ok":true,"result":{"terminals":[{"handle":"path-trap"}]}}`)
	os.Exit(0)
}

func orcaName2786() string {
	if runtime.GOOS == "windows" {
		return "orca.exe"
	}
	return "orca"
}

// privateOrca2786 installs a copy of the test binary as <dir>/orca[.exe] in a
// directory the inert guard recognizes by prefix, the way the installer does.
func privateOrca2786(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hadron-test-orca-2404-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, orcaName2786())
	if err := os.Link(exe, bin); err != nil {
		if err := copyTestOrca2404(exe, bin); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

type child2786 struct {
	code           int
	stdout, stderr string
}

// runChild2786 runs bin with args, with the path-marker env set to marker (not
// set at all when marker is nil), under a hard 15s bound.
func runChild2786(t *testing.T, bin string, marker *string, args ...string) child2786 {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, inertSweepOrcaEnv2404+"=") {
			env = append(env, kv)
		}
	}
	if marker != nil {
		env = append(env, inertSweepOrcaEnv2404+"="+*marker)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the private child did not exit within its bound (re-entered the runner?)\n%s%s", out.String(), errb.String())
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the private child: %v", err)
	}
	return child2786{code: code, stdout: out.String(), stderr: errb.String()}
}

func TestPrivateInertOrcaAnswersOnlyTheOneListCall2786(t *testing.T) {
	bin := privateOrca2786(t)
	got := runChild2786(t, bin, &bin, "terminal", "list", "--json")
	if got.code != 0 || strings.TrimSpace(got.stdout) != `{"ok":true,"result":{"terminals":[]}}` {
		t.Fatalf("the sentinel must answer an empty world: %+v", got)
	}
}

func TestPrivateInertOrcaRefusesAMissingPathMarker2786(t *testing.T) {
	bin := privateOrca2786(t)
	got := runChild2786(t, bin, nil, "-test.run=^$")
	if got.code != 2 || !strings.Contains(got.stderr, "path marker is missing or mismatched") || strings.Contains(got.stdout+got.stderr, "PASS") {
		t.Fatalf("want exit 2 and the marker refusal, never the runner: %+v", got)
	}
	// An empty marker is the same as none.
	empty := ""
	got = runChild2786(t, bin, &empty, "-test.run=^$")
	if got.code != 2 || !strings.Contains(got.stderr, "path marker is missing or mismatched") {
		t.Fatalf("an empty marker must refuse too: %+v", got)
	}
}

func TestPrivateInertOrcaRefusesAMismatchedPathMarker2786(t *testing.T) {
	bin := privateOrca2786(t)
	other := filepath.Join(filepath.Dir(bin), "not-"+orcaName2786())
	got := runChild2786(t, bin, &other, "-test.run=^$")
	if got.code != 2 || !strings.Contains(got.stderr, "path marker is missing or mismatched") || strings.Contains(got.stdout+got.stderr, "PASS") {
		t.Fatalf("want exit 2 and the marker refusal, never the runner: %+v", got)
	}
}

func TestPrivateInertOrcaRefusesUnsupportedArgv2786(t *testing.T) {
	bin := privateOrca2786(t)
	for _, args := range [][]string{
		{"-test.run=^$"},
		{"terminal", "list"},
		{"terminal", "list", "--json", "extra"},
		{"terminal", "send", "--json"},
		{},
	} {
		got := runChild2786(t, bin, &bin, args...)
		if got.code != 2 || !strings.Contains(got.stderr, "only supports terminal list --json") || got.stdout != "" {
			t.Errorf("args %q: want exit 2, the argv refusal and no answer, got %+v", args, got)
		}
	}
}

// The default dupe-pass client must reach the private inert Orca and never one
// found on the ambient PATH. Unlike the !windows shell-trap test, the trap here
// is a copy of the test binary, so this runs natively on Windows too.
func TestHealthDupeDefaultNeverExecutesAmbientOrcaNatively2786(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	trap := filepath.Join(dir, orcaName2786())
	if err := os.Link(exe, trap); err != nil {
		if err := copyTestOrca2404(exe, trap); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHealthDupeDefaultChild2786$")
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "PATH="+dir, "HADRON_TEST_2786_CHILD=1", ambientTrapEnv2786+"="+marker)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the unconfigured dupe client must see the inert world: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("an ambient PATH Orca was reached (stat: %v)", err)
	}
}

func TestHealthDupeDefaultChild2786(t *testing.T) {
	if os.Getenv("HADRON_TEST_2786_CHILD") != "1" {
		t.Skip("only run in the controlled-PATH subprocess")
	}
	client, terms := sweepOrcaClient(context.Background(), &hostpoller.Config{})
	if len(terms) != 0 {
		t.Fatalf("the unconfigured default must be inert, got %+v", terms)
	}
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("the inert executable must answer successfully: %v", err)
	}
}
