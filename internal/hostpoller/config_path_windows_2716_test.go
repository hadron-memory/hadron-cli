//go:build windows

package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2716: native Windows evidence for the config-path routing of cli#2709.
// The pure-function table in config_path_2700_test.go uses stand-ins for the
// native resolvers; this drives the real filepath.VolumeName,
// os.IsPathSeparator and filepath.Abs through LoadConfig and the reload read,
// for separator-rooted and drive-relative paths beside ordinary relative
// controls, and checks that sourcePath still names the file LoadConfig read
// after the working directory moves.

const winConfig2716 = `app: hrn:app:acme.com:team
label: box1
poll_interval_seconds: 30
key_file: host.key
worktrees_root: %ROOT%
state_file: state.json
repos:
  acme/repo: %ROOT%/clone
workers:
  iris:
    family: claude
    launch: [claude-worker]
`

func TestLoadConfigNativeWindowsPaths2716(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Registered after TempDir, so it runs first (cleanups are LIFO): Windows
	// cannot remove a directory that is the process's working directory.
	t.Cleanup(func() { _ = os.Chdir(orig) })
	other := filepath.Join(dir, "elsewhere")
	for _, d := range []string{filepath.Join(dir, "clone"), other} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "host.key"), []byte("hdr_host_fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(dir, "host.yaml")
	text := strings.ReplaceAll(winConfig2716, "%ROOT%", filepath.ToSlash(dir))
	if err := os.WriteFile(cfgFile, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(cfgFile)
	if vol == "" {
		t.Fatalf("temp config %q has no volume", cfgFile)
	}
	rooted := strings.TrimPrefix(cfgFile, vol) // \dir\host.yaml

	for _, tc := range []struct {
		name, path string
	}{
		{"relative", "host.yaml"},
		{"relative-slash", "./host.yaml"},
		{"rooted-backslash", rooted},
		{"rooted-slash", filepath.ToSlash(rooted)},
		{"drive-relative", vol + "host.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			if filepath.IsAbs(tc.path) {
				t.Fatalf("%q must be non-absolute on Windows", tc.path)
			}
			c, err := LoadConfig(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if !filepath.IsAbs(c.sourcePath) {
				t.Fatalf("sourcePath %q is not absolute", c.sourcePath)
			}
			// The working directory moves: a reload must still read the
			// file LoadConfig read.
			if err := os.Chdir(other); err != nil {
				t.Fatal(err)
			}
			raw, info, err := readConfigFile(c.sourcePath)
			if err != nil {
				t.Fatalf("reload read of %q after chdir: %v", c.sourcePath, err)
			}
			if !os.SameFile(want, info) {
				t.Fatalf("sourcePath %q is a different file from %q", c.sourcePath, cfgFile)
			}
			if _, err := loadConfigBytes(c.sourcePath, raw); err != nil {
				t.Fatalf("reload parse of %q: %v", c.sourcePath, err)
			}
		})
	}
}
