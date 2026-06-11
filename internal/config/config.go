// Package config reads and writes ~/.config/hadron/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/viper"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// DefaultServer is the hosted Hadron platform's API server (the
// GraphQL + OAuth host; hadronmemory.com itself is the portal).
const DefaultServer = "https://srv.hadronmemory.com"

// Keys lists the settings hadron config get/set accepts.
var Keys = map[string]string{
	"server": "Hadron server base URL",
	"app":    "default App URN sent with requests (set via hadron app use)",
}

type Config struct {
	v    *viper.Viper
	path string
}

// Load reads the config file if it exists; a missing file yields an
// all-defaults config.
func Load() (*Config, error) {
	path, err := File()
	if err != nil {
		return nil, err
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("toml")
	v.SetDefault("server", DefaultServer)
	if err := v.ReadInConfig(); err != nil {
		// A missing config file means all-defaults; anything else
		// (permissions, parse errors) is a real failure.
		var notFound viper.ConfigFileNotFoundError
		if !os.IsNotExist(err) && !errors.As(err, &notFound) {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
	}
	return &Config{v: v, path: path}, nil
}

// Server returns the configured server base URL, with the
// HADRON_SERVER environment variable taking precedence.
func (c *Config) Server() string {
	if env := os.Getenv("HADRON_SERVER"); env != "" {
		return env
	}
	return c.v.GetString("server")
}

// App returns the default App URN, or "" for no App context.
func (c *Config) App() string { return c.v.GetString("app") }

// Get returns a known key's value.
func (c *Config) Get(key string) (string, error) {
	if _, ok := Keys[key]; !ok {
		return "", exitcode.Newf(exitcode.Usage, "unknown config key %q (known keys: %s)", key, knownKeys())
	}
	return c.v.GetString(key), nil
}

// Set updates a known key and persists the file with 0600 perms.
func (c *Config) Set(key, value string) error {
	if _, ok := Keys[key]; !ok {
		return exitcode.Newf(exitcode.Usage, "unknown config key %q (known keys: %s)", key, knownKeys())
	}
	c.v.Set(key, value)
	return c.write()
}

// Unset removes a key and persists the file.
func (c *Config) Unset(key string) error {
	if _, ok := Keys[key]; !ok {
		return exitcode.Newf(exitcode.Usage, "unknown config key %q (known keys: %s)", key, knownKeys())
	}
	// viper has no delete; rebuild the settings map without the key.
	settings := map[string]any{}
	for k := range Keys {
		if k == key {
			continue
		}
		if c.v.IsSet(k) && c.v.GetString(k) != "" {
			settings[k] = c.v.Get(k)
		}
	}
	fresh := viper.New()
	fresh.SetConfigFile(c.path)
	fresh.SetConfigType("toml")
	fresh.SetDefault("server", DefaultServer)
	for k, val := range settings {
		fresh.Set(k, val)
	}
	c.v = fresh
	return c.write()
}

// All returns every known key with its effective value.
func (c *Config) All() map[string]string {
	out := map[string]string{}
	for k := range Keys {
		out[k] = c.v.GetString(k)
	}
	return out
}

func (c *Config) write() error {
	if _, err := EnsureDir(); err != nil {
		return err
	}
	if err := c.v.WriteConfigAs(c.path); err != nil {
		return err
	}
	return os.Chmod(c.path, 0o600)
}

func knownKeys() string {
	keys := make([]string, 0, len(Keys))
	for k := range Keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
