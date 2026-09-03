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

// run invokes the script with the command in the ENVIRONMENT — the only way it
// accepts one — and returns combined output plus the exit code.
//
// `fragment` is the shell text; pass "" to test the missing-command refusal.
func run(t *testing.T, fragment string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script(t)}, args...)...)
	cmd.Env = append(os.Environ(), "WRITE_IF_PRODUCED_CMD="+fragment)
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
	out, code := run(t, `printf "new snapshot\n"`, dest)
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
	out, code := run(t, "exit 1", dest)
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
	out, code := run(t, "true", dest)
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
	out, code := run(t, "pwd", dest, "-C", dir)
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
	out, code := run(t, `printf "install chatter\n" 1>&2 && printf "REAL SDL\n"`, dest)
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
	out, code := run(t, `false && printf "never\n"`, dest)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := mustRead(t, dest); got != before {
		t.Errorf("destination must be UNCHANGED, got %q", got)
	}
}

// No command in the environment is a USAGE error, not an empty write. This is
// the shape a caller hits after the argv form was retired, so it has to say
// what to do rather than silently produce nothing.
func TestRefusesWhenNoCommandIsGiven(t *testing.T) {
	dest := seed(t, "keep\n")
	out, code := run(t, "", dest)
	if code != 2 {
		t.Fatalf("a missing command is a usage error, got exit %d: %s", code, out)
	}
	if !strings.Contains(out, "WRITE_IF_PRODUCED_CMD") {
		t.Errorf("the usage must name the variable that carries the command: %s", out)
	}
	if got := mustRead(t, dest); got != "keep\n" {
		t.Errorf("destination must be untouched, got %q", got)
	}
}

// QUOTES INSIDE THE FRAGMENT SURVIVE (@codex P2, second round).
//
// The previous draft passed the command as `-- "$(SDL_EXPORT)"` in the recipe:
// make expands, then the SHELL reparses and strips the inner quotes. Measured
// with GNU Make 3.81 —
// `printf "%s\n" "type Query { quoted: String }"` arrived as
// `printf %sn type Query { quoted: String }` and wrote
// `typenQueryn{nquoted:nStringn}n` into the snapshot.
//
// A plausible-looking, wrong artifact: exactly what this script exists to
// prevent, produced by the script itself.
func TestPreservesQuotesInsideTheFragment(t *testing.T) {
	dest := seed(t, "old\n")
	out, code := run(t, `printf "%s\n" "type Query { quoted: String }"`, dest)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := mustRead(t, dest); got != "type Query { quoted: String }\n" {
		t.Errorf("inner quotes and backslashes must survive verbatim, got %q", got)
	}
}

// THE TEMP FILE IS STAGED BESIDE THE DESTINATION (@codex P2 + @copilot,
// independently), so the final `mv` is a same-filesystem rename.
//
// From $TMPDIR it can be a cross-device copy-then-unlink, and a copy failing
// partway leaves the destination truncated — the guard against a zero-byte
// artifact producing one itself, on exactly the machines least like a
// developer laptop.
//
// Driven rather than asserted about the source: the generator lists its own
// destination directory, so the file it sees IS the staging file.
func TestStagesTheTempFileBesideTheDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "committed.txt")
	if err := os.WriteFile(dest, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Runs while the temp file exists, and reports what is next to the target.
	out, code := run(t, "ls -a "+dir, dest)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := mustRead(t, dest); !strings.Contains(got, ".write-if-produced.") {
		t.Errorf("the staging file must live in the destination's directory, listing was:\n%s", got)
	}
	// And a TMPDIR far from the destination must not change that — the choice
	// has to come from the destination, not from the environment.
	dir2 := t.TempDir()
	dest2 := filepath.Join(dir2, "committed.txt")
	if err := os.WriteFile(dest2, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script(t), dest2)
	cmd.Env = append(os.Environ(),
		"WRITE_IF_PRODUCED_CMD=ls -a "+dir2,
		"TMPDIR="+t.TempDir())
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v (%s)", err, b)
	}
	if got := mustRead(t, dest2); !strings.Contains(got, ".write-if-produced.") {
		t.Errorf("TMPDIR must not move the staging file, listing was:\n%s", got)
	}
}

// Nothing is left behind — a crash-leftover in `schema/` would show up in
// `git status` and read as a stray file nobody can explain.
func TestLeavesNoStagingFileBehind(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "committed.txt")
	for _, fragment := range []string{`printf "ok\n"`, "exit 1", "true"} {
		if _, code := run(t, fragment, dest); code > 2 {
			t.Fatalf("unexpected exit for %q: %d", fragment, code)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".write-if-produced.") {
				t.Errorf("after %q the staging file %s was left behind", fragment, e.Name())
			}
		}
	}
}

// A destination that does not exist yet is created — the first refresh in a
// fresh checkout must not need the file to already be there.
func TestCreatesAMissingDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "fresh.txt")
	if _, code := run(t, `printf "x\n"`, dest); code != 0 {
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
		{"no destination", nil},
		{"-C with no directory", []string{dest, "-C"}},
		// The retired argv shapes. Both are a caller still using the form that
		// LOST the quotes, so answering them would produce a plausible wrong
		// artifact — the failure this whole script exists to prevent.
		{"the retired -- separator", []string{dest, "--"}},
		{"a command in argv", []string{dest, "--", "printf x"}},
		{"a stray trailing argument", []string{dest, "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := run(t, `printf "x\n"`, tc.args...)
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
