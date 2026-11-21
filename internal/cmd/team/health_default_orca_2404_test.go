package team

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const inertSweepOrcaEnv2404 = "HADRON_TEST_INERT_SWEEP_ORCA"

// inertSweepOrcaDirPrefix2404 is what the guard recognises a private inert
// Orca's directory by; the cli#2786 tests build their private copies with it too.
const inertSweepOrcaDirPrefix2404 = "hadron-test-orca-2404-"

// The private PATH executable is this test binary, reached through a hard link
// (or a copy across filesystems). Recognize only that exact installed path,
// before TestMain initializes anything or enters the test runner again.
func runInertSweepOrca2404() {
	installed := os.Getenv(inertSweepOrcaEnv2404)
	// exec.Command("orca", ...) resolves PATH into Cmd.Path but keeps the
	// original short name in argv[0]. Compare the actual executable path.
	exe, err := os.Executable()
	if err != nil || !strings.HasPrefix(filepath.Base(filepath.Dir(exe)), inertSweepOrcaDirPrefix2404) {
		return
	}
	// A private copy must never re-enter m.Run, even if its environment marker
	// was lost. Failing closed here bounds a broken fixture too.
	if installed == "" || filepath.Clean(exe) != filepath.Clean(installed) {
		fmt.Fprintln(os.Stderr, "inert test Orca path marker is missing or mismatched")
		os.Exit(2)
	}
	if strings.Join(os.Args[1:], " ") != "terminal list --json" {
		fmt.Fprintln(os.Stderr, "inert test Orca only supports terminal list --json")
		os.Exit(2)
	}
	fmt.Println(`{"ok":true,"result":{"terminals":[]}}`)
	os.Exit(0)
}

// Install a default only in this test process. Production sweepOrcaClient
// still uses ExecEnv and ScrubbedEnv, and an explicitly configured executable
// still runs normally. A missing/broken default fails the suite, not open.
func installInertSweepOrca2404() (func(), error) {
	dir, err := os.MkdirTemp("", inertSweepOrcaDirPrefix2404)
	if err != nil {
		return nil, err
	}
	oldPath := os.Getenv("PATH")
	oldMarker, hadMarker := os.LookupEnv(inertSweepOrcaEnv2404)
	cleanup := func() {
		_ = os.Setenv("PATH", oldPath)
		if hadMarker {
			_ = os.Setenv(inertSweepOrcaEnv2404, oldMarker)
		} else {
			_ = os.Unsetenv(inertSweepOrcaEnv2404)
		}
		_ = os.RemoveAll(dir)
	}
	exe, err := os.Executable()
	if err != nil {
		cleanup()
		return nil, err
	}
	name := "orca"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin, err := linkTestOrca2404(exe, dir, name)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Setenv(inertSweepOrcaEnv2404, bin); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

// linkTestOrca2404 puts the test binary at dir/name by hard link, falling back
// to a copy across filesystems: the one way every private Orca is installed, by
// the installer and by the cli#2786 fixtures alike.
func linkTestOrca2404(exe, dir, name string) (string, error) {
	bin := filepath.Join(dir, name)
	if err := os.Link(exe, bin); err != nil {
		if err := copyTestOrca2404(exe, bin); err != nil {
			return "", err
		}
	}
	return bin, nil
}

func copyTestOrca2404(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
