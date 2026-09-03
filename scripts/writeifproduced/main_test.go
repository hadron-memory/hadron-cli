// Package main holds no command — only the test that drives
// scripts/write-if-produced.sh (hadron-cli#555).
//
// A Go test rather than a shell harness because the repo has one test runner
// and `make test` is the gate; scripts/unboundops is the precedent for a Go
// package under scripts/. The script is the deliverable, and its whole promise
// is about what it does NOT do to a file — a promise no caller can observe
// until the day it matters, which is precisely when nobody is looking.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// script resolves the script under test from this package's directory.
func script(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(wd, "..", "write-if-produced.sh")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("script not found at %s: %v", p, err)
	}
	return p
}

// run invokes the script and returns combined output plus the exit code.
func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script(t)}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if err != nil {
		if !asExitError(err, &ee) {
			t.Fatalf("running the script: %v", err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// seed writes a destination file with known content and returns its path.
func seed(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "committed.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The happy path: output replaces the destination.
func TestWritesWhenTheGeneratorProduces(t *testing.T) {
	dest := seed(t, "old snapshot\n")
	out, code := run(t, dest, "--", `printf "new snapshot\n"`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := mustRead(t, dest); got != "new snapshot\n" {
		t.Errorf("destination = %q", got)
	}
}

// THE DEFECT #555 WAS FILED FOR. A bare `cmd > file` opens the redirect before
// the command runs, so a failing generator leaves the committed file at zero
// bytes and make reports only "Error 1".
func TestLeavesTheFileAloneWhenTheGeneratorFails(t *testing.T) {
	const before = "the real snapshot\n"
	dest := seed(t, before)
	out, code := run(t, dest, "--", "exit 1")
	if code != 1 {
		t.Fatalf("a failing generator must exit 1, got %d: %s", code, out)
	}
	if got := mustRead(t, dest); got != before {
		t.Errorf("the destination must be UNCHANGED, got %q", got)
	}
	// And it must SAY so. The original failure was survivable the moment you
	// knew the file was gone; nothing told you.
	if !strings.Contains(out, "UNCHANGED") {
		t.Errorf("the refusal must say the file was left alone: %s", out)
	}
}

// The same disaster wearing a green exit code — and the reason emptiness is
// refused rather than trusted. A silenced runner with missing dependencies
// exits 0 and prints nothing; an empty snapshot is committable and breaks
// somewhere far less obviously connected.
func TestRefusesAnEmptyResultEvenOnSuccess(t *testing.T) {
	const before = "the real snapshot\n"
	dest := seed(t, before)
	out, code := run(t, dest, "--", "true")
	if code != 1 {
		t.Fatalf("an empty result must be refused, got exit %d: %s", code, out)
	}
	if got := mustRead(t, dest); got != before {
		t.Errorf("the destination must be UNCHANGED, got %q", got)
	}
	if !strings.Contains(out, "produced nothing") {
		t.Errorf("the refusal must name the cause: %s", out)
	}
}

// -C runs the generator elsewhere without leaking the directory change, which
// is what `make schema` needs: the exporter runs inside the server checkout
// while the destination is relative to THIS repo.
func TestRunsTheGeneratorInAnotherDirectory(t *testing.T) {
	dest := seed(t, "old\n")
	dir := t.TempDir()
	out, code := run(t, dest, "-C", dir, "--", "pwd")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	// macOS resolves TempDir through /private, so compare the suffix rather
	// than the string — a test that fails on one platform for a reason
	// unrelated to the behaviour is worse than no test.
	if got := strings.TrimSpace(mustRead(t, dest)); !strings.HasSuffix(got, strings.TrimPrefix(dir, "/private")) {
		t.Errorf("the generator must run in -C's directory, got %q want suffix of %q", got, dir)
	}
}

// THE COMMAND IS A SHELL FRAGMENT, and this pins the exact shape that broke
// (@codex P1).
//
// `SDL_EXPORT` has always been shell-expanded, and the nightly schema-drift
// workflow passes `npm install --silent … 1>&2 && node_modules/.bin/tsx …`.
// The first draft ran the arguments as an argv list, so `1>&2` and `&&` became
// literal arguments to `npm`: the exporter never ran, the wrapper saw empty
// output, and every scheduled drift check would have failed for a reason
// unrelated to drift — on a `continue-on-error` job, so quietly.
//
// The two operators are tested together because that is how they arrive.
func TestRunsTheCommandThroughAShell(t *testing.T) {
	dest := seed(t, "old\n")
	// Faithful to the workflow: noise to stderr, `&&`, then the real producer.
	out, code := run(t, dest, "--", `printf "install chatter\n" 1>&2 && printf "REAL SDL\n"`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	got := mustRead(t, dest)
	if got != "REAL SDL\n" {
		t.Errorf("only the producer's STDOUT belongs in the file, got %q", got)
	}
	// The chatter must not land in the artifact — that is the other half of the
	// failure @codex described: if npm printed anything, its text replaced the
	// schema while the real SDL went to the job log.
	if strings.Contains(got, "install chatter") {
		t.Errorf("stderr must not reach the destination: %q", got)
	}
}

// …and a shell fragment that FAILS mid-pipeline still leaves the file alone.
// `&&` short-circuiting is the realistic failure: the install fails, the
// exporter never runs, and the old argv form could not even express this.
func TestShellFailureMidFragmentLeavesTheFileAlone(t *testing.T) {
	const before = "the real snapshot\n"
	dest := seed(t, before)
	out, code := run(t, dest, "--", `false && printf "never\n"`)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := mustRead(t, dest); got != before {
		t.Errorf("destination must be UNCHANGED, got %q", got)
	}
}

// A destination that does not exist yet is created — the first refresh in a
// fresh checkout must not need the file to already be there.
func TestCreatesAMissingDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "fresh.txt")
	if _, code := run(t, dest, "--", `printf "x\n"`); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := mustRead(t, dest); got != "x\n" {
		t.Errorf("destination = %q", got)
	}
	// mktemp's 0600 would leave a committed file unreadable to everything but
	// its writer; the mode has to be the one a repo file has.
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 644 — mktemp's 0600 must not survive the move", perm)
	}
}

// Malformed invocations are a USAGE error (exit 2), not a silent no-op that
// leaves the caller believing a refresh happened.
func TestRejectsMalformedInvocations(t *testing.T) {
	dest := seed(t, "keep\n")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no arguments at all", nil},
		{"destination but no command", []string{dest}},
		{"missing the -- separator", []string{dest, "printf x"}},
		{"-C with no directory", []string{dest, "-C"}},
		{"separator but no command", []string{dest, "--"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := run(t, tc.args...)
			if code != 2 {
				t.Errorf("usage errors exit 2, got %d: %s", code, out)
			}
			if len(tc.args) > 0 {
				if got := mustRead(t, dest); got != "keep\n" {
					t.Errorf("a usage error must not touch the destination, got %q", got)
				}
			}
		})
	}
}
