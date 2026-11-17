package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- memory encrypt / link-user: --data-key-file, literal --data-key deprecated (#1657, ruling 0148) ----
//
// A raw data key in argv shows in `ps`, /proc/<pid>/cmdline and shell history.
// The key now comes from a mode-checked file (or stdin), a literal still works
// for one release but warns once on stderr (never echoing the key), and the
// refusal of a literal is a later, separate change.

const encryptKeyMemory1657 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type dataKeyCase1657 struct {
	name      string
	base      []string
	responses map[string]string
	opName    string
}

func dataKeyCases1657() []dataKeyCase1657 {
	return []dataKeyCase1657{
		{"encrypt", []string{"memory", "encrypt", encryptKeyMemory1657, "--yes", "--json"},
			map[string]string{"EncryptMemory": `{"data":{"encryptMemory":{"id":"` + encryptKeyMemory1657 + `","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`}, "EncryptMemory"},
		{"link-user", []string{"memory", "link-user", "acme.com::anon-7f3", "--external-user", "u1", "--yes", "--json"},
			linkUserFakes(linkedEncryptedMemoryJSON), "LinkMemoryToUser"},
	}
}

type dataKeyRun1657 struct {
	stdout, stderr string
	err            error
	reqs           []capturedLinkUserRequest
}

func runDataKey1657(t *testing.T, c dataKeyCase1657, stdin string, extra ...string) dataKeyRun1657 {
	t.Helper()
	gql, reqs := captureLinkUserGraphQL(t, c.responses)
	f, out := testFactory(t)
	if stdin != "" {
		f.IOStreams.In = strings.NewReader(stdin)
	}
	root := NewRootCmd(f)
	root.SetArgs(append(append(append([]string{}, c.base...), extra...), "--server", gql.URL))
	err := root.Execute()
	return dataKeyRun1657{out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err, append([]capturedLinkUserRequest{}, *reqs...)}
}

// keyFile1657 writes a key file at exactly mode (Chmod, so the umask cannot
// change what the test means).
func keyFile1657(t *testing.T, mode os.FileMode, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data.key")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

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

func TestDataKeyFileExclusiveWithDataKeyAndPassphraseFile1657(t *testing.T) {
	enc := dataKeyCases1657()[0]
	r := runDataKey1657(t, enc, "", "--data-key-file", keyFile1657(t, 0o600, "k\n"), "--data-key", "literal")
	if r.err == nil || !strings.Contains(r.err.Error(), "--data-key and --data-key-file are mutually exclusive") || len(r.reqs) != 0 {
		t.Fatalf("--data-key-file with --data-key: %v reqs=%d", r.err, len(r.reqs))
	}
	r = runDataKey1657(t, enc, "", "--data-key-file", keyFile1657(t, 0o600, "k\n"), "--passphrase-file", keyFile1657(t, 0o600, "p\n"))
	if r.err == nil || !strings.Contains(r.err.Error(), "--data-key-file and --passphrase-file are mutually exclusive") || len(r.reqs) != 0 || exitCodeFor(r.err) != exitcode.Usage {
		t.Fatalf("--data-key-file with --passphrase-file: %v reqs=%d", r.err, len(r.reqs))
	}
}

// --data-key-file - reads stdin, and the confirmation prompt is answered BEFORE
// stdin is consumed as the key (Confirm reads line by line).
func TestDataKeyFileStdinConfirmsBeforeReading1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			gql, reqs := captureLinkUserGraphQL(t, c.responses)
			f, out, _ := testFactoryTTY(t, "")
			f.IOStreams.In = iotest.OneByteReader(strings.NewReader("y\n" + linkUserDataKey + "\n"))
			args := []string{}
			for _, a := range c.base {
				if a != "--yes" {
					args = append(args, a)
				}
			}
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--data-key-file", "-", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("the prompt must be answered before the key is read: %v", err)
			}
			if len(*reqs) == 0 || !strings.Contains(out.String(), "{") {
				t.Fatalf("no request/output: reqs=%d out=%q", len(*reqs), out.String())
			}
			sent := false
			for _, r := range *reqs {
				if r.OpKey == linkUserDataKey || strings.Contains(r.Body, linkUserDataKey) {
					sent = true
				}
			}
			if !sent {
				t.Fatalf("the key read from stdin after the answer was not sent: %+v", *reqs)
			}
		})
	}
}

// A literal --data-key still works for one release, warning once on stderr
// about --data-key-file and never echoing the key; --data-key - does not warn.
func TestLiteralDataKeyWarnsOnceWithoutEchoingTheKey1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			r := runDataKey1657(t, c, "", "--data-key", linkUserDataKey)
			if r.err != nil || len(r.reqs) == 0 {
				t.Fatalf("a literal key must still work: err=%v reqs=%d", r.err, len(r.reqs))
			}
			if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n") + 1; r.stderr == "" || n != 1 {
				t.Fatalf("want exactly one stderr line, got %d: %q", n, r.stderr)
			}
			if !strings.Contains(r.stderr, "--data-key-file") || !strings.Contains(strings.ToLower(r.stderr), "deprecated") {
				t.Fatalf("the warning must name --data-key-file and say deprecated: %q", r.stderr)
			}
			if strings.Contains(r.stderr, linkUserDataKey) || strings.Contains(r.stdout, linkUserDataKey) {
				t.Fatalf("the key must never be echoed: stderr=%q stdout=%q", r.stderr, r.stdout)
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &parsed); err != nil {
				t.Fatalf("--json stdout must stay valid JSON (the warning is stderr only): %v\n%s", err, r.stdout)
			}
			quiet := runDataKey1657(t, c, linkUserDataKey+"\n", "--data-key", "-")
			if quiet.err != nil || quiet.stderr != "" {
				t.Fatalf("--data-key - is the supported form and must not warn: err=%v stderr=%q", quiet.err, quiet.stderr)
			}
			if quiet.stdout != r.stdout {
				t.Fatalf("stdout differs between the literal and stdin forms:\n%s\n%s", r.stdout, quiet.stdout)
			}
		})
	}
}

// The warning follows the refusals: a refused run sends nothing and the
// literal is not what is reported.
func TestLiteralDataKeyWarningDoesNotPrecedeAUsageRefusal1657(t *testing.T) {
	c := dataKeyCases1657()[0]
	r := runDataKey1657(t, c, "", "--data-key", "literal", "--passphrase-file", keyFile1657(t, 0o600, "p\n"))
	if r.err == nil || len(r.reqs) != 0 {
		t.Fatalf("want the existing exclusive refusal: %v reqs=%d", r.err, len(r.reqs))
	}
	if strings.Contains(r.stderr, "literal") {
		t.Fatalf("the value must never appear: %q", r.stderr)
	}
}

// Help names --data-key-file first and says a literal is deprecated.
func TestDataKeyHelpNamesTheFileFormFirst1657(t *testing.T) {
	for _, path := range [][]string{{"memory", "encrypt"}, {"memory", "link-user"}} {
		t.Run(path[1], func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetOut(out)
			root.SetArgs(append(append([]string{}, path...), "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := strings.Join(strings.Fields(out.String()), " ")
			file := strings.Index(help, "--data-key-file")
			if file < 0 {
				t.Fatalf("help must name --data-key-file:\n%s", out.String())
			}
			// Every mention of the bare literal flag comes after the file form.
			literal := strings.Index(strings.ReplaceAll(help, "--data-key-file", "--FILE"), "--data-key")
			if literal >= 0 && literal < file {
				t.Fatalf("--data-key-file must be named before --data-key:\n%s", out.String())
			}
			if !strings.Contains(strings.ToLower(help), "deprecated") {
				t.Fatalf("help must say a literal --data-key is deprecated:\n%s", out.String())
			}
			cmd, _, err := root.Find(path)
			if err != nil {
				t.Fatal(err)
			}
			if fl := cmd.Flags().Lookup("data-key-file"); fl == nil || !strings.Contains(fl.Usage, "stdin") {
				t.Fatalf("--data-key-file flag missing or undocumented: %+v", fl)
			}
			if fl := cmd.Flags().Lookup("data-key"); fl == nil || !strings.Contains(strings.ToLower(fl.Usage), "deprecated") {
				t.Fatalf("--data-key usage must say deprecated: %+v", fl)
			}
		})
	}
}
