package config

import (
	"os"
	"path/filepath"
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
