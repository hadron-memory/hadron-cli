package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the capture example itself, so its status and filesystem effects
// cannot drift from the help a user copies (cli#1313). All keys are synthetic.
func TestAuthTokenCaptureExample(t *testing.T) {
	for _, tool := range []string{"bash", "jq", "mktemp", "ln"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("capture-example prerequisite %s: %v", tool, err)
		}
	}
	f, _ := testFactory(t)
	leaf, _, err := NewRootCmd(f).Find([]string{"auth", "token", "create"})
	if err != nil {
		t.Fatal(err)
	}
	_, script, ok := strings.Cut(leaf.Example, "\n")
	if !ok {
		t.Fatal("missing capture example")
	}
	for _, mode := range []string{"success", "refusal", "refusal-json", "invalid-json", "existing", "dangling", "directory", "racing-file", "racing-directory", "racing-directory-link"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			tools := filepath.Join(dir, "tools")
			if err := os.Mkdir(tools, 0o700); err != nil {
				t.Fatal(err)
			}
			stub := `#!/usr/bin/env bash
printf 'called\n' >> "$CAPTURE_CALLS"
[ "$*" = "auth token create --label ci --json" ] || exit 99
case "$CAPTURE_MODE" in
refusal) exit 3 ;;
refusal-json) printf '{"rawKey":"synthetic-key"}\n'; exit 8 ;;
invalid-json) printf 'not JSON\n'; exit 0 ;;
racing-file) printf 'foreign\n' > ci-token ;;
racing-directory) mkdir ci-token ;;
racing-directory-link) ln -s foreign-dir ci-token ;;
esac
printf '{"rawKey":"synthetic-key"}\n'
`
			if err := os.WriteFile(filepath.Join(tools, "hadron"), []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(dir, "ci-token")
			switch mode {
			case "existing":
				if err := os.WriteFile(dest, []byte("foreign\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "dangling":
				if err := os.Symlink("missing-target", dest); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(dest, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			foreignDir := filepath.Join(dir, "foreign-dir")
			if mode == "racing-directory-link" {
				if err := os.Mkdir(foreignDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(foreignDir, "sentinel"), []byte("foreign\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			calls := filepath.Join(dir, "calls")
			cmd := exec.Command("bash", "-c", strings.TrimSpace(script))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "CAPTURE_MODE="+mode, "CAPTURE_CALLS="+calls, "TMPDIR="+dir)
			out, runErr := cmd.CombinedOutput()
			if strings.Contains(string(out), "synthetic-key") {
				t.Error("capture example echoed key")
			}
			count, _ := os.ReadFile(calls)
			if mode == "existing" || mode == "dangling" || mode == "directory" {
				if runErr == nil || len(count) != 0 {
					t.Errorf("existing destination: err=%v calls=%q", runErr, count)
				}
			} else if string(count) != "called\n" {
				t.Errorf("want one mint call, got %q", count)
			}
			switch mode {
			case "success":
				if runErr != nil {
					t.Fatalf("capture: %v (%s)", runErr, out)
				}
				b, err := os.ReadFile(dest)
				if err != nil || string(b) != "synthetic-key\n" {
					t.Errorf("captured value: %q err=%v", b, err)
				}
				info, err := os.Stat(dest)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o600 {
					t.Errorf("mode %o, want 600", info.Mode().Perm())
				}
			case "existing", "racing-file":
				b, err := os.ReadFile(dest)
				if runErr == nil || err != nil || string(b) != "foreign\n" {
					t.Errorf("foreign file changed: run=%v read=%v body=%q", runErr, err, b)
				}
			case "dangling":
				target, err := os.Readlink(dest)
				if err != nil || target != "missing-target" {
					t.Errorf("symlink changed: %q %v", target, err)
				}
			case "racing-directory", "racing-directory-link":
				if runErr == nil {
					t.Error("racing directory reported success")
				}
				if mode == "racing-directory-link" {
					target, err := os.Readlink(dest)
					if err != nil || target != "foreign-dir" {
						t.Errorf("foreign link changed: %q %v", target, err)
					}
					b, err := os.ReadFile(filepath.Join(foreignDir, "sentinel"))
					if err != nil || string(b) != "foreign\n" {
						t.Errorf("foreign sentinel changed: %q %v", b, err)
					}
				}
				entries, err := os.ReadDir(dest)
				want := 0
				if mode == "racing-directory-link" {
					want = 1
				}
				if err != nil || len(entries) != want {
					t.Errorf("foreign directory changed: %v %v", entries, err)
				}
			case "directory":
				entries, err := os.ReadDir(dest)
				if err != nil || len(entries) != 0 {
					t.Errorf("foreign directory changed: %v %v", entries, err)
				}
			default:
				if runErr == nil {
					t.Error("failed mint/parse reported success")
				}
				if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Errorf("failed mint left destination: %v", err)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "ci-token.") {
					t.Errorf("temporary capture left behind: %s", e.Name())
				}
			}
		})
	}
}
