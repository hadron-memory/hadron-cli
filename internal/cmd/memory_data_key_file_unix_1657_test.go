//go:build !windows

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The file-form tests that depend on POSIX modes and a readable key file. On
// Windows the file form is refused with its own message (a file's permissions
// cannot be checked there); see memory_data_key_file_windows_1657_test.go.

// --data-key-file f at 0600 sends exactly what --data-key - sends today: the
// same operations, the same bodies, the same header.
func TestDataKeyFileSendsTheSameRequestAsStdin1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			viaStdin := runDataKey1657(t, c, linkUserDataKey+"\n", "--data-key", "-")
			viaFile := runDataKey1657(t, c, "", "--data-key-file", keyFile1657(t, 0o600, linkUserDataKey+"\n"))
			if viaStdin.err != nil || viaFile.err != nil {
				t.Fatalf("stdin err=%v, file err=%v", viaStdin.err, viaFile.err)
			}
			if len(viaFile.reqs) == 0 || len(viaFile.reqs) != len(viaStdin.reqs) {
				t.Fatalf("request counts differ: file %d, stdin %d", len(viaFile.reqs), len(viaStdin.reqs))
			}
			for i := range viaFile.reqs {
				if viaFile.reqs[i] != viaStdin.reqs[i] {
					t.Fatalf("request %d differs:\nfile  %+v\nstdin %+v", i, viaFile.reqs[i], viaStdin.reqs[i])
				}
			}
			if viaFile.stdout != viaStdin.stdout {
				t.Fatalf("stdout differs:\nfile  %s\nstdin %s", viaFile.stdout, viaStdin.stdout)
			}
			if viaFile.stderr != "" {
				t.Fatalf("--data-key-file prints no warning: %q", viaFile.stderr)
			}
		})
	}
}

// A file any group or other user can access is refused before ANY request, with
// the readPassphraseFile rule and wording, and without echoing the path.
func TestDataKeyFileRefusesGroupOrOtherAccess1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666, 0o601} {
			t.Run(c.name+"/"+mode.String(), func(t *testing.T) {
				path := keyFile1657(t, mode, linkUserDataKey+"\n")
				r := runDataKey1657(t, c, "", "--data-key-file", path)
				want := "--data-key-file is accessible by group or others (mode " + fmtMode1657(mode) + "); run chmod 600 on it. Nothing was sent"
				if r.err == nil || exitCodeFor(r.err) != exitcode.Usage || r.err.Error() != want {
					t.Fatalf("err = %v (exit %d), want usage %q", r.err, exitCodeFor(r.err), want)
				}
				if len(r.reqs) != 0 {
					t.Fatalf("a refused file must send nothing: %+v", r.reqs)
				}
				if strings.Contains(r.err.Error(), path) || strings.Contains(r.stderr, linkUserDataKey) {
					t.Fatalf("the path or key was echoed: %v / %q", r.err, r.stderr)
				}
			})
		}
	}
}

func fmtMode1657(m os.FileMode) string {
	const digits = "01234567"
	v := uint32(m.Perm())
	return string([]byte{'0', digits[(v>>6)&7], digits[(v>>3)&7], digits[v&7]})
}

// 0400 (read-only for the owner) is private and accepted.
func TestDataKeyFileAcceptsOwnerOnlyModes1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		for _, mode := range []os.FileMode{0o600, 0o400} {
			t.Run(c.name+"/"+mode.String(), func(t *testing.T) {
				r := runDataKey1657(t, c, "", "--data-key-file", keyFile1657(t, mode, linkUserDataKey+"\n"))
				if r.err != nil || len(r.reqs) == 0 {
					t.Fatalf("err=%v reqs=%d", r.err, len(r.reqs))
				}
			})
		}
	}
}

// The other refusals, all before any request: not a regular file, missing, empty.
func TestDataKeyFileRefusals1657(t *testing.T) {
	dir := t.TempDir()
	empty := keyFile1657(t, 0o600, "  \n")
	for _, c := range dataKeyCases1657() {
		for name, tc := range map[string]struct {
			args []string
			want string
		}{
			"directory":      {[]string{"--data-key-file", dir}, "--data-key-file is not a regular file. Nothing was sent"},
			"missing":        {[]string{"--data-key-file", filepath.Join(dir, "absent")}, "reading --data-key-file:"},
			"blank-file":     {[]string{"--data-key-file", empty}, "--data-key-file holds no key. Nothing was sent"},
			"empty-value":    {[]string{"--data-key-file="}, "--data-key-file is empty: name a file, or - for stdin. Nothing was sent"},
			"with-data-key":  {[]string{"--data-key-file", empty, "--data-key", "x"}, "--data-key and --data-key-file are mutually exclusive"},
			"with-stdin-key": {[]string{"--data-key-file", empty, "--data-key", "-"}, "--data-key and --data-key-file are mutually exclusive"},
		} {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				r := runDataKey1657(t, c, "", tc.args...)
				if r.err == nil || exitCodeFor(r.err) != exitcode.Usage {
					t.Fatalf("want a usage refusal, got %v (exit %d)", r.err, exitCodeFor(r.err))
				}
				// The flag must exist: an "unknown flag" usage error is not the refusal.
				if strings.Contains(r.err.Error(), "unknown flag") || !strings.Contains(r.err.Error(), tc.want) {
					t.Fatalf("error = %q, want it to contain %q", r.err, tc.want)
				}
				if strings.Contains(r.err.Error(), dir) {
					t.Fatalf("the path must not be echoed: %q", r.err)
				}
				if len(r.reqs) != 0 {
					t.Fatalf("nothing may be sent: %+v", r.reqs)
				}
			})
		}
	}
}

// link-user validates the key's shape locally before any request when it comes
// from a mode-checked file too, and the refusal never echoes the key.
func TestLinkUserMalformedKeyViaDataKeyFileRefusedLocally1657(t *testing.T) {
	c := dataKeyCases1657()[1]
	for _, bad := range []string{"not a key!", strings.Repeat("A", 43), "QUJD"} {
		r := runDataKey1657(t, c, "", "--data-key-file", keyFile1657(t, 0o600, bad+"\n"))
		if r.err == nil || exitCodeFor(r.err) != exitcode.Usage || len(r.reqs) != 0 {
			t.Fatalf("a malformed key (%q) from a file must be refused locally: err=%v reqs=%d", bad, r.err, len(r.reqs))
		}
		if strings.Contains(r.err.Error(), bad) || strings.Contains(r.stderr, bad) {
			t.Fatalf("the refusal must not echo the key: %v / %q", r.err, r.stderr)
		}
	}
}

// The documented workflow itself: run the example's key-creation line in a real
// shell under the default umask 022 (with a stub openssl), then give the file to
// --data-key-file. Creating the file owner-only from the start is what makes the
// example work; a plain redirect would leave 0644 and be refused.
func TestEncryptExampleCreatesAKeyFileTheCommandAccepts1657(t *testing.T) {
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"memory", "encrypt"})
	if err != nil {
		t.Fatal(err)
	}
	var create string
	for _, line := range strings.Split(cmd.Example, "\n") {
		if strings.Contains(line, "openssl rand -base64 32") {
			create = strings.TrimSpace(line)
			if i := strings.Index(create, "#"); i >= 0 {
				create = strings.TrimSpace(create[:i])
			}
		}
	}
	if create == "" {
		t.Fatalf("the example no longer shows how to create a key file:\n%s", cmd.Example)
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "openssl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' '"+linkUserDataKey+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("sh", "-c", "umask 022; "+create)
	sh.Dir = dir
	sh.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("the example line failed: %v: %s", err, out)
	}
	keyPath := filepath.Join(dir, "diary.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("the example did not create diary.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("the example's key file is mode %04o, which --data-key-file refuses", perm)
	}
	r := runDataKey1657(t, dataKeyCases1657()[0], "", "--data-key-file", keyPath)
	if r.err != nil || len(r.reqs) == 0 {
		t.Fatalf("the command refused the file the example creates: err=%v reqs=%d", r.err, len(r.reqs))
	}
}
