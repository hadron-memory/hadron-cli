package config

import (
	"errors"
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
// It creates nothing.
func StateDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "hadron"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "hadron"), nil
}

// ErrBadHostID refuses a host id that is not one (empty, "..", a path
// separator, or anything else outside the host id rule), so a state path can
// never leave <state dir>/hosts.
var ErrBadHostID = errors.New("not a host id")

// HostStateDir returns <state dir>/hosts/<hostID>, the runtime state of one
// host this machine runs (hadron-cli#3239). The id must be a host id
// (internal/hostid); on Windows, whose file names cannot hold ":", an id with
// one is refused too. It creates nothing: EnsureHostStateDir does.
func HostStateDir(hostID string) (string, error) {
	if !hostid.Valid(hostID) || (runtime.GOOS == "windows" && strings.Contains(hostID, ":")) {
		return "", ErrBadHostID
	}
	base, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hosts", hostID), nil
}

// EnsureHostStateDir creates HostStateDir(hostID) and its parents at 0700 and
// returns it.
func EnsureHostStateDir(hostID string) (string, error) {
	dir, err := HostStateDir(hostID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
