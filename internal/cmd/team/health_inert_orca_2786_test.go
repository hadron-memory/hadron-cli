package team

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
// private copy of THIS test binary (never a real host Orca) under a hard bound.
// Every direct guard probe carries noReentryEnv2786 (the one exception is the
// integration subprocess of the ambient-PATH test, which must run a selected test): if a guard regresses and lets the
// process fall through to the test runner, TestMain (failIfUnclaimed2786) exits
// 3 before m.Run, so a fail-open guard FAILS a pin quickly instead of recursing
// into the suite. The bound is a backstop for a child that hangs.

const (
	noReentryEnv2786 = "HADRON_TEST_2786_NO_REENTRY"
	// The ambient trap recognises itself, and records that it was reached, by
	// files beside it, never by environment: the real launch scrubs HADRON_* from
	// what an Orca receives, and a marker carried that way would be lost exactly
	// when the trap is reached through the production launch path.
	trapSentinelFile2786 = "ambient-trap-2786"
	trapCalledFile2786   = "called"
)

// failIfUnclaimed2786 runs in TestMain after every guard: a process started with
// the no-reentry sentinel that reaches it was claimed by no guard, so it must
// not run the suite.
func failIfUnclaimed2786() {
	if os.Getenv(noReentryEnv2786) == "1" {
		fmt.Fprintln(os.Stderr, "no guard claimed this private child; refusing to re-enter the test runner")
		os.Exit(3)
	}
}

// disablePrimaryGuardEnv2786 makes runInertSweepOrca2404 return at once. It
// stands for a regression that removes or breaks that guard, so the test below
// can show the SECOND, independent guard still stops a private inert Orca.
const disablePrimaryGuardEnv2786 = "HADRON_TEST_2786_DISABLE_PRIMARY_GUARD"

// failIfPrivateOrca2786 is the second guard, keyed on the executable's own
// directory and nothing else: a test binary that is running from a private
// inert-Orca directory (hadron-test-orca-2404-*) is a hard link made for the
// sweep client to exec, and must never run the suite. Without it a regression in
// the primary guard (or a seeded fault removing it) lets every private copy run
// the 2404 tests, each of which makes and execs the next copy: a fork loop that
// outlives the test, leaks a temp directory per generation, and filled the host
// disk. It runs after the primary guard, so a healthy run never reaches it.
func failIfPrivateOrca2786() {
	exe, err := os.Executable()
	if err != nil || !strings.HasPrefix(filepath.Base(filepath.Dir(exe)), inertSweepOrcaDirPrefix2404) {
		return
	}
	fmt.Fprintln(os.Stderr, "a private inert Orca must never run the test suite; the primary guard did not claim this process")
	os.Exit(3)
}

// cleanEnv2786 is the parent's environment without anything that could point a
// child at a real host Orca or carry a credential (HADRON_*, ORCA_*).
func cleanEnv2786() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HADRON_") || strings.HasPrefix(kv, "ORCA_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// runAmbientTrap2786 makes a copy of the test binary named orca/orca.exe, in a
// directory that is not the private inert one and holds the trap sentinel file,
// behave as an "ambient" Orca: it records that it was reached (a file beside it)
// and answers a terminal. It is the Windows-capable
// stand-in for the shell-script trap of the !windows test.
func runAmbientTrap2786() {
	exe, err := os.Executable()
	if err != nil || strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe") != "orca" ||
		strings.HasPrefix(filepath.Base(filepath.Dir(exe)), inertSweepOrcaDirPrefix2404) { // never the private inert Orca
		return
	}
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, trapSentinelFile2786)); err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, trapCalledFile2786), []byte("x"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "ambient trap could not record that it was reached:", err)
		os.Exit(4)
	}
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
	dir, err := os.MkdirTemp("", inertSweepOrcaDirPrefix2404)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := linkTestOrca2404(exe, dir, orcaName2786())
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

type child2786 struct {
	code           int
	stdout, stderr string
}

// runChild2786 runs bin with args, with the path-marker env set to marker (not
// set at all when marker is nil), under a hard 30s bound, with the no-reentry sentinel set.
func runChild2786(t *testing.T, bin string, marker *string, args ...string) child2786 {
	t.Helper()
	env := append(cleanEnv2786(), noReentryEnv2786+"=1")
	if marker != nil {
		env = append(env, inertSweepOrcaEnv2404+"="+*marker)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

const markerRefusal2786 = "path marker is missing or mismatched"

func TestPrivateInertOrcaAnswersTheOneListCall2786(t *testing.T) {
	bin := privateOrca2786(t)
	got := runChild2786(t, bin, &bin, "terminal", "list", "--json")
	if got.code != 0 || strings.TrimSpace(got.stdout) != `{"ok":true,"result":{"terminals":[]}}` || got.stderr != "" {
		t.Fatalf("the sentinel must answer an empty world: %+v", got)
	}
}

// The marker guard is judged with the ONE argv that would otherwise be
// answered, so a guard that answers regardless of the marker fails here; the
// answer is asserted absent, not just the exit code.
func TestPrivateInertOrcaRefusesAnUnrecognisedPathMarker2786(t *testing.T) {
	bin := privateOrca2786(t)
	empty := ""
	otherDir := privateOrca2786(t) // a second REAL private orca: right prefix, wrong path
	caseVariant := strings.ToUpper(bin)
	cases := map[string]*string{
		"no marker":                 nil,
		"an empty marker":           &empty,
		"a nonexistent sibling":     ptr2786(filepath.Join(filepath.Dir(bin), "not-"+orcaName2786())),
		"another private orca path": &otherDir,
	}
	if runtime.GOOS == "windows" {
		cases["a case variant (Windows paths are case-insensitive, the guard compares exactly)"] = &caseVariant
	}
	for name, marker := range cases {
		for _, args := range [][]string{{"terminal", "list", "--json"}, {"-test.run=^$"}} {
			got := runChild2786(t, bin, marker, args...)
			if got.code != 2 || !strings.Contains(got.stderr, markerRefusal2786) || got.stdout != "" {
				t.Errorf("%s, args %q: want exit 2, the marker refusal and no stdout, got %+v", name, args, got)
			}
		}
	}
}

func ptr2786(s string) *string { return &s }

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

// A child no guard claims must not run the suite: with the sentinel it exits 3.
// This is what makes every pin above fail fast, not recurse, if a guard goes.
// With the primary guard "removed", a private inert Orca is still stopped by the
// second guard: exit 3 at once, never the suite (which is what forks the next
// copy).
func TestAPrivateInertOrcaIsStoppedByTheSecondGuardWhenThePrimaryIsGone2786(t *testing.T) {
	bin := privateOrca2786(t)
	// Every argv carries -test.run=^$ so that, were this guard ever missing too, the
	// child would run no test (and fail this pin) instead of forking the next copy.
	for _, args := range [][]string{{"-test.run=^$"}, {"-test.run=^$", "terminal", "list", "--json"}} {
		env := append(cleanEnv2786(), noReentryEnv2786+"=1", disablePrimaryGuardEnv2786+"=1", inertSweepOrcaEnv2404+"="+bin)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		cancel()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 3 || !strings.Contains(errb.String(), "must never run the test suite") || out.String() != "" {
			t.Errorf("args %q: want exit 3 from the second guard and no output, got %v / %q / %q", args, err, errb.String(), out.String())
		}
	}
}

func TestAChildNoGuardClaimsDoesNotReenterTheRunner2786(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := runChild2786(t, exe, nil, "-test.run=^$") // not under the private prefix: no guard claims it
	if got.code != 3 || !strings.Contains(got.stderr, "no guard claimed") {
		t.Fatalf("want exit 3 before the runner, got %+v", got)
	}
}

// The trap is a real, reachable executable: run directly (with its marker env),
// it records that it was reached and answers a terminal. Without this the
// ambient-PATH pin below could pass by the trap never being able to fire.
func TestTheAmbientTrapCanFire2786(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, trapCalledFile2786)
	trap := installTrap2786(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, trap, "terminal", "list", "--json")
	cmd.Env = append(cleanEnv2786(), noReentryEnv2786+"=1")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "path-trap") {
		t.Fatalf("the trap must answer: %v %q", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the trap must record that it was reached: %v", err)
	}
}

func installTrap2786(t *testing.T, dir string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	trap, err := linkTestOrca2404(exe, dir, orcaName2786())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, trapSentinelFile2786), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return trap
}

// The default dupe-pass client must reach the private inert Orca and never one
// found on the ambient PATH. Unlike the !windows shell-trap test, the trap here
// is a copy of the test binary, so this runs natively on Windows too. The child
// test must be seen to PASS (not be skipped or renamed away).
func TestHealthDupeDefaultNeverExecutesAmbientOrcaNatively2786(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, trapCalledFile2786)
	installTrap2786(t, dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.v", "-test.run=^TestHealthDupeDefaultChild2786$")
	cmd.WaitDelay = time.Second
	cmd.Env = append(cleanEnv2786(), "PATH="+dir, "HADRON_TEST_2786_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the unconfigured dupe client must see the inert world: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestHealthDupeDefaultChild2786") {
		t.Fatalf("the child test did not run and pass (skipped or renamed?):\n%s", out)
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
	// sweepOrcaClient degrades an exec failure to no terminals; a direct read
	// tells a real empty world from a missing or broken executable.
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("the inert executable must answer successfully: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("the inert world is empty, got %+v", got)
	}
}
