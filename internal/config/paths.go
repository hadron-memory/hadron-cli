package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/hostid"
)

// Dir returns the hadron config directory: $XDG_CONFIG_HOME/hadron,
// defaulting to ~/.config/hadron (gh-style, on every platform).
func Dir() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "hadron"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "hadron"), nil
}

// EnsureDir creates the config directory with owner-only permissions.
func EnsureDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// File returns the path of the main config file.
func File() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// AuthFile returns the path of the fallback token file used when no
// OS keychain is available.
func AuthFile() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth.json"), nil
}

// StateDir returns the directory for runtime state the CLI owns (locks,
// records of what it launched), never a setting: $XDG_STATE_HOME/hadron,
// defaulting to ~/.local/state/hadron, on every platform (hadron-cli#3239).
// It creates nothing. The rule itself is StateDirFrom, which team host doctor
// reads too (hostfacts.Facts.StateDir), so the two cannot drift.
func StateDir() (string, error) {
	if os.Getenv("XDG_STATE_HOME") != "" {
		return StateDirFrom(os.Getenv, ""), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return StateDirFrom(os.Getenv, home), nil
}

// StateDirFrom is the state-directory rule with its inputs passed in:
// getenv("XDG_STATE_HOME")/hadron when that is set, else
// <home>/.local/state/hadron. Callers with test seams (hostfacts) use it
// directly; everyone else calls StateDir.
func StateDirFrom(getenv func(string) string, home string) string {
	if base := getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "hadron")
	}
	return filepath.Join(home, ".local", "state", "hadron")
}

// ErrBadHostID refuses a host id that is not one (empty, "..", a path
// separator, or anything else outside the host id rule), so a state path can
// never leave <state dir>/hosts.
var ErrBadHostID = errors.New("not a host id")

// HostStateDir returns <state dir>/hosts/<hostID>, the runtime state of one
// host this machine runs (hadron-cli#3239). The id must be a host id
// (internal/hostid); on Windows, whose file names cannot hold ":", an id with
// one is refused too, as is a reserved device name or a trailing dot (hadron-cli#3288 F8). It creates
// nothing: EnsureHostStateDir does.
func HostStateDir(hostID string) (string, error) {
	if !hostid.Valid(hostID) || (runtime.GOOS == "windows" && (strings.Contains(hostID, ":") || windowsUnsafeName(hostID))) {
		return "", ErrBadHostID
	}
	base, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hosts", hostID), nil
}

// EnsureHostStateDir creates HostStateDir(hostID) and its parents at 0700 and
// returns it. MkdirAll keeps whatever an existing directory already is, so the
// result is checked (hadron-cli#3288 F8): hosts and hosts/<id> must not be
// symlinks (a file there already fails MkdirAll), and on Unix hosts and hosts/<id> are tightened to 0700 when
// they are wider (as hostcred's store directory is checked).
func EnsureHostStateDir(hostID string) (string, error) {
	dir, err := HostStateDir(hostID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		fi, err := os.Lstat(d)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink; hadron keeps a host's state only in a directory of its own: remove the link", d)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(d, 0o700); err != nil {
				return "", fmt.Errorf("%s is accessible to group or others (mode %o) and cannot be tightened: %w; run chmod 700 %s", d, fi.Mode().Perm(), err, d)
			}
		}
	}
	return dir, nil
}

// windowsUnsafeName reports a name Windows does not keep as written (hadron-cli#3288 F8): a reserved device
// name (CON, PRN, AUX, NUL, COM1-9, LPT1-9, in any case and with any extension) opens the device, and a
// trailing dot or space is dropped, so "box." would be "box"'s directory.
func windowsUnsafeName(name string) bool {
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return true
	}
	stem := strings.ToUpper(name)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}
